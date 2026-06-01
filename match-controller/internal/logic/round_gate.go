package logic

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

type RoundGateLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRoundGateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RoundGateLogic {
	return &RoundGateLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RoundGateLogic) Run(matchID string, roundNo, planGameCount int, roleSpecsJSON, chatRoomID string) error {
	if matchID == "" {
		return errors.New("match id is required")
	}
	if roundNo <= 0 {
		return errors.New("round number is required")
	}
	if planGameCount <= 0 {
		planGameCount = 5
	}
	if roundNo > planGameCount {
		return l.writeInactive("", "beyond-plan")
	}
	var specs []roleSpec
	if roleSpecsJSON != "" {
		if err := json.Unmarshal([]byte(roleSpecsJSON), &specs); err != nil {
			return errors.Wrap(err, "decode role specs")
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		active, err := l.tryRun(matchID, roundNo, specs, chatRoomID)
		if err != nil {
			return err
		}
		if active {
			return nil
		}
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (l *RoundGateLogic) tryRun(matchID string, roundNo int, specs []roleSpec, chatRoomID string) (bool, error) {
	m, err := l.svcCtx.DB.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		Only(l.ctx)
	if err != nil {
		return false, errors.Wrap(err, "query match")
	}
	scanned := scannedMatchFromEnt(m)
	roundDir, err := NewMatchScanLogic(l.ctx, l.svcCtx).roundDir(scanned, roundNo)
	if err != nil {
		return false, err
	}
	r, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(matchID)), matchround.RoundNo(roundNo)).
		Only(l.ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			if m.LatestStatus == "DONE" {
				return true, l.writeInactive(roundDir, "match-done")
			}
			return false, nil
		}
		return false, errors.Wrap(err, "query round")
	}
	if r.Status != matchround.StatusSTARTED {
		if r.Status == matchround.StatusENDED {
			return false, errors.Errorf("round %d ended before gate observed STARTED", roundNo)
		}
		return false, nil
	}
	args, err := NewMatchScanLogic(l.ctx, l.svcCtx).roundWorkflowArguments(scanned, r, roundDir, specs, chatRoomID)
	if err != nil {
		return false, err
	}
	args["round_active"] = "true"
	args["round_skip_reason"] = ""
	args["round_dir"] = roundDir
	args["round_no"] = strconv.Itoa(roundNo)
	return true, jobcontract.WriteArgoOutputs(anyMap(args))
}

func (l *RoundGateLogic) writeInactive(roundDir, reason string) error {
	return jobcontract.WriteArgoOutputs(map[string]any{
		"round_active":          false,
		"round_skip_reason":     reason,
		"round_dir":             roundDir,
		"record_contexts":       "[]",
		"danmu_enabled":         false,
		"danmu_context":         "{}",
		"main_source_available": false,
		"analyze_context":       "{}",
		"stt_context":           "{}",
		"lark_record_enabled":   false,
		"lark_record_context":   "{}",
		"transcode_context":     "{}",
		"highlight_context":     "{}",
	})
}

func scannedMatchFromEnt(m *ent.Match) scannedMatch {
	out := scannedMatch{
		ID:              m.ID,
		Event:           m.Event,
		Zone:            m.Zone,
		Order:           m.Order,
		Status:          m.LatestStatus,
		MatchType:       m.MatchType,
		TotalRounds:     m.TotalRounds,
		Result:          string(m.Result),
		WinnerPlacehold: optionalString(m.WinnerPlaceholderName),
		LoserPlacehold:  optionalString(m.LoserPlaceholderName),
	}
	if m.MatchSlug != nil {
		out.MatchSlug = *m.MatchSlug
	}
	if m.Edges.RedTeam != nil {
		out.RedTeam = scannedTeamFromEnt(m.Edges.RedTeam)
	}
	if m.Edges.BlueTeam != nil {
		out.BlueTeam = scannedTeamFromEnt(m.Edges.BlueTeam)
	}
	return out
}

func scannedTeamFromEnt(t *ent.Team) scannedTeam {
	return scannedTeam{ID: t.ID, Name: t.Name, SchoolName: t.SchoolName, SchoolLogo: t.SchoolLogo}
}

func optionalString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func anyMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
