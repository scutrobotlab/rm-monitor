package logic

import (
	"context"
	"encoding/json"
	errors2 "errors"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/team"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/pkg/db"
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
	currentUrl  = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/current_and_next_matches.json"
	simulateUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"
)

type scannedMatch struct {
	ID               string
	Event            string
	Zone             string
	Order            int
	Status           string
	Result           string
	MatchType        string
	MatchSlug        string
	TotalRounds      int
	RedWinGameCount  int
	BlueWinGameCount int
	RedTeam          scannedTeam
	BlueTeam         scannedTeam
	Raw              map[string]any
}

type scannedTeam struct {
	ID         string
	Name       string
	SchoolName string
	SchoolLogo string
	Raw        map[string]any
}

func (m scannedMatch) RoundNo() int {
	return m.RedWinGameCount + m.BlueWinGameCount + 1
}

func (l *MatchScanLogic) MatchScan() error {
	resp, err := l.svcCtx.RestyClient.R().
		SetHeader("User-Agent", simulateUA).
		Get(scheduleUrl)
	if err != nil {
		return errors.Wrap(err, "failed to get schedule")
	}
	if !resp.IsSuccess() {
		return errors.Errorf("failed to get schedule, status code: %d", resp.StatusCode())
	}

	matches := parseMatches(resp.Bytes())
	l.Debugf("found %d matches", len(matches))

	err = errors2.Join(lo.Map(matches, func(m scannedMatch, index int) error {
		return l.upsertMatch(m)
	})...)
	if err != nil {
		return errors.Wrap(err, "failed to upsert matches")
	}

	currentMatchesResp, err := l.svcCtx.RestyClient.R().
		SetHeader("User-Agent", simulateUA).
		Get(currentUrl)
	if err != nil {
		return errors.Wrap(err, "failed to get current matches")
	}
	if !currentMatchesResp.IsSuccess() {
		return errors.Errorf("failed to get current matches, status code: %d", currentMatchesResp.StatusCode())
	}
	return nil
}

func parseMatches(contentBytes []byte) []scannedMatch {
	event := gjson.GetBytes(contentBytes, "data.event.title").String()
	var out []scannedMatch
	for _, node := range gjson.GetBytes(contentBytes, "data.event.zones.nodes").Array() {
		zone := node.Get("name").String()
		for _, item := range append(node.Get("groupMatches.nodes").Array(), node.Get("knockoutMatches.nodes").Array()...) {
			var raw map[string]any
			_ = json.Unmarshal([]byte(item.Raw), &raw)
			out = append(out, scannedMatch{
				ID:               item.Get("id").String(),
				Event:            event,
				Zone:             zone,
				Order:            int(item.Get("orderNumber").Int()),
				Status:           item.Get("status").String(),
				Result:           item.Get("result").String(),
				MatchType:        item.Get("matchType").String(),
				MatchSlug:        item.Get("slug").String(),
				TotalRounds:      int(item.Get("planGameCount").Int()),
				RedWinGameCount:  int(item.Get("redSideWinGameCount").Int()),
				BlueWinGameCount: int(item.Get("blueSideWinGameCount").Int()),
				RedTeam:          parseTeam(item.Get("redSide.player")),
				BlueTeam:         parseTeam(item.Get("blueSide.player")),
				Raw:              raw,
			})
		}
	}
	return out
}

func parseTeam(player gjson.Result) scannedTeam {
	teamNode := player.Get("team")
	var raw map[string]any
	_ = json.Unmarshal([]byte(teamNode.Raw), &raw)
	id := teamNode.Get("id").String()
	if id == "" {
		id = player.Get("teamId").String()
	}
	return scannedTeam{
		ID:         id,
		Name:       teamNode.Get("name").String(),
		SchoolName: teamNode.Get("collegeName").String(),
		SchoolLogo: teamNode.Get("collegeLogo").String(),
		Raw:        raw,
	}
}

func (l *MatchScanLogic) upsertMatch(m scannedMatch) error {
	if m.ID == "" || m.RedTeam.ID == "" || m.BlueTeam.ID == "" {
		return nil
	}

	prev, err := l.svcCtx.DB.Match.Query().Where(match.ID(m.ID)).Only(l.ctx)
	if err != nil && !ent.IsNotFound(err) {
		return errors.Wrap(err, "query previous match")
	}

	if err := l.upsertTeam(m.RedTeam); err != nil {
		return err
	}
	if err := l.upsertTeam(m.BlueTeam); err != nil {
		return err
	}

	create := l.svcCtx.DB.Match.Create().
		SetID(m.ID).
		SetEvent(m.Event).
		SetZone(m.Zone).
		SetOrder(m.Order).
		SetMatchType(m.MatchType).
		SetTotalRounds(m.TotalRounds).
		SetLatestStatus(m.Status).
		SetRawPayload(m.Raw).
		SetRedTeamID(m.RedTeam.ID).
		SetBlueTeamID(m.BlueTeam.ID)
	if m.MatchSlug != "" {
		create.SetMatchSlug(m.MatchSlug)
	}
	if err := create.OnConflictColumns(match.FieldID).UpdateNewValues().Exec(l.ctx); err != nil {
		return errors.Wrap(err, "upsert match")
	}

	if prev == nil {
		if m.Status == types.MatchStatusSTARTED {
			return l.ensureStartedRound(m, m.RoundNo())
		}
		return nil
	}

	prevScan := matchFromRaw(prev.RawPayload)
	if prev.LatestStatus == types.MatchStatusSTARTED && m.Status != types.MatchStatusSTARTED {
		return l.endRound(m.ID, prevScan.RoundNo(), winnerFromDelta(prevScan, m))
	}
	if m.Status != types.MatchStatusSTARTED {
		return nil
	}

	if prev.LatestStatus != types.MatchStatusSTARTED {
		return l.ensureStartedRound(m, m.RoundNo())
	}
	if prevScan.RedWinGameCount != m.RedWinGameCount || prevScan.BlueWinGameCount != m.BlueWinGameCount {
		if err := l.endRound(m.ID, prevScan.RoundNo(), winnerFromDelta(prevScan, m)); err != nil {
			return err
		}
		return l.ensureStartedRound(m, m.RoundNo())
	}
	return l.ensureStartedRound(m, m.RoundNo())
}

func (l *MatchScanLogic) upsertTeam(t scannedTeam) error {
	return l.svcCtx.DB.Team.Create().
		SetID(t.ID).
		SetName(t.Name).
		SetSchoolName(t.SchoolName).
		SetSchoolLogo(t.SchoolLogo).
		SetRawPayload(t.Raw).
		OnConflictColumns(team.FieldID).
		UpdateNewValues().
		Exec(l.ctx)
}

func (l *MatchScanLogic) ensureStartedRound(m scannedMatch, roundNo int) error {
	if roundNo <= 0 {
		return nil
	}
	r, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(m.ID)), matchround.RoundNo(roundNo)).
		Only(l.ctx)
	if err != nil && !ent.IsNotFound(err) {
		return errors.Wrap(err, "query match round")
	}
	if r != nil {
		if r.Status == matchround.StatusSTARTED {
			return nil
		}
		return nil
	}
	created, err := l.svcCtx.DB.MatchRound.Create().
		SetMatchID(m.ID).
		SetRoundNo(roundNo).
		SetStatus(matchround.StatusSTARTED).
		Save(l.ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil
		}
		return errors.Wrap(err, "create match round")
	}
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.MatchRoundChangedChannel, strconv.Itoa(created.ID))
}

func (l *MatchScanLogic) endRound(matchID string, roundNo int, winner *matchround.Winner) error {
	r, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(matchID)), matchround.RoundNo(roundNo)).
		Only(l.ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "query ending round")
	}
	if r.Status == matchround.StatusENDED {
		return nil
	}
	u := l.svcCtx.DB.MatchRound.UpdateOneID(r.ID).
		SetStatus(matchround.StatusENDED).
		SetEndedAt(time.Now())
	if winner != nil {
		u.SetWinner(*winner)
	}
	if err := u.Exec(l.ctx); err != nil {
		return errors.Wrap(err, "end round")
	}
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.MatchRoundChangedChannel, strconv.Itoa(r.ID))
}

func matchFromRaw(raw map[string]any) scannedMatch {
	b, _ := json.Marshal(raw)
	node := gjson.ParseBytes(b)
	return scannedMatch{
		Status:           node.Get("status").String(),
		RedWinGameCount:  int(node.Get("redSideWinGameCount").Int()),
		BlueWinGameCount: int(node.Get("blueSideWinGameCount").Int()),
	}
}

func winnerFromDelta(prev, cur scannedMatch) *matchround.Winner {
	switch {
	case cur.BlueWinGameCount > prev.BlueWinGameCount:
		w := matchround.WinnerBlue
		return &w
	case cur.RedWinGameCount > prev.RedWinGameCount:
		w := matchround.WinnerRed
		return &w
	default:
		w := matchround.WinnerDraw
		return &w
	}
}
