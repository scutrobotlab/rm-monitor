package logic

import (
	"context"
	"encoding/json"
	errors2 "errors"
	"fmt"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/team"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
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
	simulateUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"
)

type scannedMatch struct {
	ID               string
	Event            string
	Zone             string
	Order            int
	Status           string
	MatchType        string
	MatchSlug        string
	TotalRounds      int
	RedWinGameCount  int
	BlueWinGameCount int
	RedTeam          scannedTeam
	BlueTeam         scannedTeam
}

type scannedTeam struct {
	ID         string
	Name       string
	SchoolName string
	SchoolLogo string
}

type processedSnapshot struct {
	Status           string    `json:"status"`
	RedWinGameCount  int       `json:"red_win_game_count"`
	BlueWinGameCount int       `json:"blue_win_game_count"`
	RoundNo          int       `json:"round_no"`
	ObservedAt       time.Time `json:"observed_at"`
}

func (m scannedMatch) RoundNo() int {
	return m.RedWinGameCount + m.BlueWinGameCount + 1
}

func (l *MatchScanLogic) MatchScan() error {
	conf := l.svcCtx.Config.MonitorConf.WithDefaults()
	resp, err := l.svcCtx.RestyClient.R().
		SetHeader("User-Agent", simulateUA).
		Get(conf.ScheduleURL)
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
	return nil
}

func parseMatches(contentBytes []byte) []scannedMatch {
	event := gjson.GetBytes(contentBytes, "data.event.title").String()
	var out []scannedMatch
	for _, node := range gjson.GetBytes(contentBytes, "data.event.zones.nodes").Array() {
		zone := node.Get("name").String()
		for _, item := range append(node.Get("groupMatches.nodes").Array(), node.Get("knockoutMatches.nodes").Array()...) {
			out = append(out, scannedMatch{
				ID:               item.Get("id").String(),
				Event:            event,
				Zone:             zone,
				Order:            int(item.Get("orderNumber").Int()),
				Status:           item.Get("status").String(),
				MatchType:        item.Get("matchType").String(),
				MatchSlug:        item.Get("slug").String(),
				TotalRounds:      int(item.Get("planGameCount").Int()),
				RedWinGameCount:  int(item.Get("redSideWinGameCount").Int()),
				BlueWinGameCount: int(item.Get("blueSideWinGameCount").Int()),
				RedTeam:          parseTeam(item.Get("redSide.player")),
				BlueTeam:         parseTeam(item.Get("blueSide.player")),
			})
		}
	}
	return out
}

func parseTeam(player gjson.Result) scannedTeam {
	teamNode := player.Get("team")
	id := teamNode.Get("id").String()
	if id == "" {
		id = player.Get("teamId").String()
	}
	return scannedTeam{
		ID:         id,
		Name:       teamNode.Get("name").String(),
		SchoolName: teamNode.Get("collegeName").String(),
		SchoolLogo: teamNode.Get("collegeLogo").String(),
	}
}

func (l *MatchScanLogic) upsertMatch(m scannedMatch) error {
	if m.ID == "" || m.RedTeam.ID == "" || m.BlueTeam.ID == "" {
		return nil
	}

	prev, ok, err := l.loadLastProcessed(m.ID)
	if err != nil {
		return err
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
		SetRedTeamID(m.RedTeam.ID).
		SetBlueTeamID(m.BlueTeam.ID)
	if m.MatchSlug != "" {
		create.SetMatchSlug(m.MatchSlug)
	}
	if err := create.OnConflictColumns(match.FieldID).UpdateNewValues().Exec(l.ctx); err != nil {
		return errors.Wrap(err, "upsert match")
	}

	if ok {
		if err := l.reconcileRounds(prev, m); err != nil {
			return err
		}
	}
	return l.saveLastProcessed(m)
}

func (l *MatchScanLogic) upsertTeam(t scannedTeam) error {
	return l.svcCtx.DB.Team.Create().
		SetID(t.ID).
		SetName(t.Name).
		SetSchoolName(t.SchoolName).
		SetSchoolLogo(t.SchoolLogo).
		OnConflictColumns(team.FieldID).
		UpdateNewValues().
		Exec(l.ctx)
}

func (l *MatchScanLogic) reconcileRounds(prev processedSnapshot, cur scannedMatch) error {
	prevTotal := prev.RedWinGameCount + prev.BlueWinGameCount
	curTotal := cur.RedWinGameCount + cur.BlueWinGameCount
	if prev.Status == types.MatchStatusSTARTED {
		endTo := curTotal
		if cur.Status != types.MatchStatusSTARTED && endTo == prevTotal {
			endTo = prevTotal + 1
		}
		if cur.TotalRounds > 0 && endTo > cur.TotalRounds {
			endTo = cur.TotalRounds
		}
		winners := winnersFromDelta(prev, cur, endTo-prevTotal)
		for roundNo := prevTotal + 1; roundNo <= endTo; roundNo++ {
			if err := l.ensureEndedRound(cur.ID, roundNo, winners[roundNo-(prevTotal+1)]); err != nil {
				return err
			}
		}
	}
	if cur.Status == types.MatchStatusSTARTED {
		return l.ensureStartedRound(cur, cur.RoundNo())
	}
	return nil
}

func (l *MatchScanLogic) ensureStartedRound(m scannedMatch, roundNo int) error {
	if roundNo <= 0 {
		return nil
	}
	if m.TotalRounds > 0 && roundNo > m.TotalRounds {
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

func (l *MatchScanLogic) ensureEndedRound(matchID string, roundNo int, winner matchround.Winner) error {
	if roundNo <= 0 {
		return nil
	}
	r, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(matchID)), matchround.RoundNo(roundNo)).
		Only(l.ctx)
	if err != nil && !ent.IsNotFound(err) {
		return errors.Wrap(err, "query ended round")
	}
	now := time.Now()
	if r == nil {
		created, err := l.svcCtx.DB.MatchRound.Create().
			SetMatchID(matchID).
			SetRoundNo(roundNo).
			SetStatus(matchround.StatusENDED).
			SetWinner(winner).
			SetEndedAt(now).
			Save(l.ctx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return nil
			}
			return errors.Wrap(err, "create ended round")
		}
		return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.MatchRoundChangedChannel, strconv.Itoa(created.ID))
	}
	if r.Status == matchround.StatusENDED {
		return nil
	}
	if err := l.svcCtx.DB.MatchRound.UpdateOneID(r.ID).
		SetStatus(matchround.StatusENDED).
		SetWinner(winner).
		SetEndedAt(now).
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "end round")
	}
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.MatchRoundChangedChannel, strconv.Itoa(r.ID))
}

func (l *MatchScanLogic) loadLastProcessed(matchID string) (processedSnapshot, bool, error) {
	val, err := l.svcCtx.RedisClient.GetCtx(l.ctx, lastProcessedKey(matchID))
	if err != nil {
		return processedSnapshot{}, false, errors.Wrap(err, "get last processed match snapshot")
	}
	if val == "" {
		return processedSnapshot{}, false, nil
	}
	var out processedSnapshot
	if err := json.Unmarshal([]byte(val), &out); err != nil {
		return processedSnapshot{}, false, errors.Wrap(err, "decode last processed match snapshot")
	}
	return out, true, nil
}

func (l *MatchScanLogic) saveLastProcessed(m scannedMatch) error {
	b, err := json.Marshal(processedSnapshot{
		Status:           m.Status,
		RedWinGameCount:  m.RedWinGameCount,
		BlueWinGameCount: m.BlueWinGameCount,
		RoundNo:          m.RoundNo(),
		ObservedAt:       time.Now(),
	})
	if err != nil {
		return errors.Wrap(err, "encode last processed match snapshot")
	}
	return l.svcCtx.RedisClient.SetexCtx(l.ctx, lastProcessedKey(m.ID), string(b), 24*60*60)
}

func lastProcessedKey(matchID string) string {
	return fmt.Sprintf("rm-monitor:monitor:last_processed:%s", matchID)
}

func winnersFromDelta(prev processedSnapshot, cur scannedMatch, count int) []matchround.Winner {
	if count <= 0 {
		return nil
	}
	redDelta := cur.RedWinGameCount - prev.RedWinGameCount
	blueDelta := cur.BlueWinGameCount - prev.BlueWinGameCount
	out := make([]matchround.Winner, 0, count)
	for i := 0; i < count; i++ {
		switch {
		case redDelta > 0:
			out = append(out, matchround.WinnerRed)
			redDelta--
		case blueDelta > 0:
			out = append(out, matchround.WinnerBlue)
			blueDelta--
		default:
			out = append(out, matchround.WinnerDraw)
		}
	}
	return out
}
