package logic

import (
	"context"
	errors2 "errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"github.com/zeromicro/go-zero/core/jsonx"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

type MatchScanLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchScanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MatchScanLogic {
	return &MatchScanLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

const (
	scheduleUrl = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/schedule.json"
	simulateUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"
)

func (l *MatchScanLogic) MatchScan() error {
	//resp, err := l.svcCtx.RestyClient.R().
	//	SetHeader("User-Agent", simulateUA).
	//	Get(scheduleUrl)
	//if err != nil {
	//	return errors.Wrap(err, "failed to get schedule")
	//}
	//
	//if !resp.IsSuccess() {
	//	return errors.Errorf("failed to get schedule, status code: %d", resp.StatusCode())
	//}
	//
	//content := resp.String()
	f, err := os.OpenFile("./data/schedule.json", os.O_RDONLY, 0o644)
	if err != nil {
		return errors.Wrap(err, "failed to open schedule file")
	}
	defer f.Close()
	contentBytes, err := io.ReadAll(f)
	if err != nil {
		return errors.Wrap(err, "failed to read schedule file")
	}
	content := string(contentBytes)

	title := gjson.Get(content, "data.event.title").String()
	var matches []types.Match
	for _, node := range gjson.GetBytes(contentBytes, "data.event.zones.nodes").Array() {
		nodeName := node.Get("name").String()
		matches = slices.Concat(matches,
			lo.Map(slices.Concat(node.Get("groupMatches.nodes").Array(), node.Get("knockoutMatches.nodes").Array()),
				func(item gjson.Result, index int) types.Match {
					return types.Match{
						Id:     item.Get("id").String(),
						Order:  item.Get("orderNumber").Int(),
						Status: item.Get("status").String(),
						Result: item.Get("result").String(),
						BlueTeam: types.Team{
							Name:       item.Get("blueSide.player.team.name").String(),
							SchoolName: item.Get("blueSide.player.team.collegeName").String(),
							SchoolLogo: item.Get("blueSide.player.team.collegeLogo").String(),
							Score:      item.Get("blueSide.blueSideScore").Int(),
						},
						RedTeam: types.Team{
							Name:       item.Get("redSide.player.team.name").String(),
							SchoolName: item.Get("redSide.player.team.collegeName").String(),
							SchoolLogo: item.Get("redSide.player.team.collegeLogo").String(),
							Score:      item.Get("redSide.redSideScore").Int(),
						},
						BlueWinGameCount: item.Get("blueSideWinGameCount").Int(),
						RedWinGameCount:  item.Get("redSideWinGameCount").Int(),
						TotalRounds:      item.Get("planGameCount").Int(),
						MatchType:        item.Get("matchType").String(),
						MatchSlug:        item.Get("slug").String(),
						ZoneName:         nodeName,
						EventName:        title,
					}
				}))
	}

	l.Debugf("found %d matches", len(matches))

	err = errors2.Join(lo.Map(matches, func(m types.Match, index int) error {
		return l.matchCompare(&m)
	})...)
	if err != nil {
		return errors.Wrap(err, "failed to compare matches")
	}

	return nil
}

func (l *MatchScanLogic) matchCompare(m *types.Match) error {
	key := fmt.Sprintf("rm-monitor:match:%s", m.Id)
	lock := redis.NewRedisLock(l.svcCtx.RedisClient, key+":lock")
	lock.SetExpire(10)
	acquire, err := lock.AcquireCtx(l.ctx)
	if err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}
	if !acquire {
		// another process is handling this match
		return nil
	}
	defer func() {
		marshal, _ := jsonx.MarshalToString(m)
		if err := l.svcCtx.RedisClient.SetexCtx(l.ctx, key, marshal, 60); err != nil {
			l.Errorf("failed to set match: %s", err)
		}
		if _, err := lock.Release(); err != nil {
			l.Errorf("failed to release lock: %s", err)
		}
	}()
	prevStr, err := l.svcCtx.RedisClient.GetCtx(l.ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to get match")
	}
	if prevStr == "" {
		//if err = l.onMatchStart(m); err != nil {
		//	return errors.Wrap(err, "failed to handle match start")
		//}
		//
		//if err = l.onMatchNewRound(m); err != nil {
		//	return errors.Wrap(err, "failed to handle match new round")
		//}
		return nil
	}
	var prev types.Match
	_ = jsonx.UnmarshalFromString(prevStr, &prev)
	if prev.Status != m.Status {
		if m.Status == types.MatchStatusDone || m.Status == types.MatchStatusPending {
			if err = l.onMatchDone(m); err != nil {
				return errors.Wrap(err, "failed to handle match done")
			}
		}

		if m.Status == types.MatchStatusSTARTED {
			if err = l.onMatchStart(m); err != nil {
				return errors.Wrap(err, "failed to handle match start")
			}
		}
	}

	if m.Status == types.MatchStatusSTARTED {
		if prev.BlueWinGameCount != m.BlueWinGameCount || prev.RedWinGameCount != m.RedWinGameCount {
			if err = l.onMatchNewRound(m); err != nil {
				return errors.Wrap(err, "failed to handle match new round")
			}
		}
	}

	return nil
}

func (l *MatchScanLogic) onMatchStart(m *types.Match) error {
	l.Infof("match started: %+v", m)

	data, err := jsonx.MarshalToString(m)
	if err != nil {
		return errors.Wrap(err, "failed to marshal match")
	}

	return errors.Wrap(l.svcCtx.KqPusherClient.PushWithKey(l.ctx,
		m.GetMatchStartKey(),
		data),
		"failed to push match start")
}

func (l *MatchScanLogic) onMatchNewRound(m *types.Match) error {
	l.Infof("match new round: %+v", m)

	data, err := jsonx.MarshalToString(m)
	if err != nil {
		return errors.Wrap(err, "failed to marshal match")
	}

	return errors.Wrap(l.svcCtx.KqPusherClient.PushWithKey(l.ctx,
		m.GetMatchNewRoundKey(),
		data),
		"failed to push match new round")
}

func (l *MatchScanLogic) onMatchDone(m *types.Match) error {
	l.Infof("match done: %+v", m)

	data, err := jsonx.MarshalToString(m)
	if err != nil {
		return errors.Wrap(err, "failed to marshal match")
	}

	return errors.Wrap(l.svcCtx.KqPusherClient.PushWithKey(l.ctx,
		m.GetMatchDoneKey(),
		data),
		"failed to push match done")
}
