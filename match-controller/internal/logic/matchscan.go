package logic

import (
	"context"
	"encoding/json"
	errors2 "errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/match-controller/internal/priority"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/match-controller/types"
	"scutbot.cn/web/rm-monitor/pkg/argowf"
	"scutbot.cn/web/rm-monitor/pkg/bitableupload"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/recording"
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
	simulateUA                      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"
	controllerHealthKey             = "rm-monitor:health:match-controller:last_success"
	controllerHealthTTLSeconds      = 5 * 60
	controllerHealthTimestampLayout = time.RFC3339
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
	Result           string
	WinnerPlacehold  string
	LoserPlacehold   string
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

type roleSpec struct {
	Role      string `json:"role"`
	SourceURL string `json:"source_url"`
	KeepAudio bool   `json:"keep_audio"`
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
	if err := l.svcCtx.RedisClient.SetexCtx(l.ctx, controllerHealthKey, time.Now().Format(controllerHealthTimestampLayout), controllerHealthTTLSeconds); err != nil {
		return errors.Wrap(err, "update monitor health heartbeat")
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
				Result:           normalizeMatchResult(item.Get("result").String()),
				WinnerPlacehold:  item.Get("winnerPlaceholdName").String(),
				LoserPlacehold:   item.Get("loserPlaceholdName").String(),
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

	latestStatus, err := l.latestStatusForUpsert(m)
	if err != nil {
		return err
	}
	matchPriority := priority.ForSchools(l.svcCtx.Config.Priority, m.RedTeam.SchoolName, m.BlueTeam.SchoolName)
	if err := l.upsertMatchChangedOnly(m, latestStatus, matchPriority); err != nil {
		return errors.Wrap(err, "upsert match")
	}
	if latestStatus == types.MatchStatusSTARTED {
		if err := l.ensureMatchWorkflow(m); err != nil {
			return err
		}
	}

	if ok {
		if err := l.reconcileRounds(prev, m); err != nil {
			return err
		}
	}
	if err := l.convergeMatchLatestStatus(m); err != nil {
		return err
	}
	return l.saveLastProcessed(m)
}

func (l *MatchScanLogic) latestStatusForUpsert(m scannedMatch) (string, error) {
	if m.Status != types.MatchStatusSTARTED && matchDecided(m) {
		return "DONE", nil
	}
	return m.Status, nil
}

func (l *MatchScanLogic) upsertTeam(t scannedTeam) error {
	existing, err := l.svcCtx.DB.Team.Get(l.ctx, t.ID)
	if err != nil {
		if ent.IsNotFound(err) {
			return l.svcCtx.DB.Team.Create().
				SetID(t.ID).
				SetName(t.Name).
				SetSchoolName(t.SchoolName).
				SetSchoolLogo(t.SchoolLogo).
				Exec(l.ctx)
		}
		return err
	}
	if !teamNeedsUpdate(existing, t) {
		return nil
	}
	return l.svcCtx.DB.Team.UpdateOneID(t.ID).
		SetName(t.Name).
		SetSchoolName(t.SchoolName).
		SetSchoolLogo(t.SchoolLogo).
		Exec(l.ctx)
}

func (l *MatchScanLogic) upsertMatchChangedOnly(m scannedMatch, latestStatus string, matchPriority int) error {
	existing, err := l.svcCtx.DB.Match.Query().
		Where(match.ID(m.ID)).
		WithRedTeam().
		WithBlueTeam().
		Only(l.ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			create := l.svcCtx.DB.Match.Create().
				SetID(m.ID).
				SetEvent(m.Event).
				SetZone(m.Zone).
				SetOrder(m.Order).
				SetMatchType(m.MatchType).
				SetTotalRounds(m.TotalRounds).
				SetPriority(matchPriority).
				SetResult(match.Result(m.Result)).
				SetWinnerPlaceholderName(m.WinnerPlacehold).
				SetLoserPlaceholderName(m.LoserPlacehold).
				SetLatestStatus(latestStatus).
				SetRedTeamID(m.RedTeam.ID).
				SetBlueTeamID(m.BlueTeam.ID)
			if m.MatchSlug != "" {
				create.SetMatchSlug(m.MatchSlug)
			}
			return create.Exec(l.ctx)
		}
		return err
	}
	if !matchNeedsUpdate(existing, m, latestStatus, matchPriority) {
		return nil
	}
	update := l.svcCtx.DB.Match.UpdateOneID(m.ID).
		SetEvent(m.Event).
		SetZone(m.Zone).
		SetOrder(m.Order).
		SetMatchType(m.MatchType).
		SetTotalRounds(m.TotalRounds).
		SetPriority(matchPriority).
		SetResult(match.Result(m.Result)).
		SetWinnerPlaceholderName(m.WinnerPlacehold).
		SetLoserPlaceholderName(m.LoserPlacehold).
		SetLatestStatus(latestStatus).
		SetRedTeamID(m.RedTeam.ID).
		SetBlueTeamID(m.BlueTeam.ID)
	if m.MatchSlug != "" {
		update.SetMatchSlug(m.MatchSlug)
	}
	return update.Exec(l.ctx)
}

func teamNeedsUpdate(existing *ent.Team, next scannedTeam) bool {
	return existing.Name != next.Name ||
		existing.SchoolName != next.SchoolName ||
		existing.SchoolLogo != next.SchoolLogo
}

func matchNeedsUpdate(existing *ent.Match, next scannedMatch, latestStatus string, priorityValue int) bool {
	redTeamID := ""
	if existing.Edges.RedTeam != nil {
		redTeamID = existing.Edges.RedTeam.ID
	}
	blueTeamID := ""
	if existing.Edges.BlueTeam != nil {
		blueTeamID = existing.Edges.BlueTeam.ID
	}
	return existing.Event != next.Event ||
		existing.Zone != next.Zone ||
		existing.Order != next.Order ||
		existing.MatchType != next.MatchType ||
		(next.MatchSlug != "" && !optionalStringValueEqual(existing.MatchSlug, next.MatchSlug)) ||
		existing.TotalRounds != next.TotalRounds ||
		existing.Priority != priorityValue ||
		existing.Result != match.Result(next.Result) ||
		!optionalStringValueEqual(existing.WinnerPlaceholderName, next.WinnerPlacehold) ||
		!optionalStringValueEqual(existing.LoserPlaceholderName, next.LoserPlacehold) ||
		existing.LatestStatus != latestStatus ||
		redTeamID != next.RedTeam.ID ||
		blueTeamID != next.BlueTeam.ID
}

func optionalStringValueEqual(existing *string, next string) bool {
	return existing != nil && *existing == next
}

func (l *MatchScanLogic) reconcileRounds(prev processedSnapshot, cur scannedMatch) error {
	prevTotal := prev.RedWinGameCount + prev.BlueWinGameCount
	curTotal := cur.RedWinGameCount + cur.BlueWinGameCount
	if prev.Status == types.MatchStatusSTARTED {
		endTo := curTotal
		if cur.TotalRounds > 0 && endTo > cur.TotalRounds {
			endTo = cur.TotalRounds
		}
		winners := winnersFromDelta(prev, cur, endTo-prevTotal)
		for i, winner := range winners {
			if err := l.ensureEndedRound(cur.ID, prevTotal+1+i, winner); err != nil {
				return err
			}
		}
	}
	if cur.Status != types.MatchStatusSTARTED {
		return l.convergeCompletedRounds(cur)
	}
	if matchDecided(cur) {
		return nil
	}
	if cur.Status == types.MatchStatusSTARTED {
		return l.ensureStartedRound(cur, cur.RoundNo())
	}
	return nil
}

func (l *MatchScanLogic) syncPendingTaskPriority(matchID string, priorityValue int) error {
	// Argo owns task scheduling in the new architecture. Priority is stored on
	// match rows and passed to workflow parameters instead of being synced into
	// queue tables.
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
		return nil
	}
	_, err = l.svcCtx.DB.MatchRound.Create().
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
	return nil
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
		_, err := l.svcCtx.DB.MatchRound.Create().
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
		return nil
	}
	if r.Status == matchround.StatusENDED && r.Winner != nil && *r.Winner == winner {
		return nil
	}
	if err := l.svcCtx.DB.MatchRound.UpdateOneID(r.ID).
		SetStatus(matchround.StatusENDED).
		SetWinner(winner).
		SetEndedAt(now).
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "end round")
	}
	return nil
}

func (l *MatchScanLogic) ensureMatchWorkflow(m scannedMatch) error {
	conf := l.svcCtx.Config.ArgoConf.WithDefaults()
	if !conf.Enabled || l.svcCtx.ArgoClient == nil {
		return nil
	}
	roleSpecs, chatRoomID, err := l.roleSpecsForMatch(m)
	if err != nil {
		return errors.Wrap(err, "build match workflow role specs")
	}
	planGameCount := m.TotalRounds
	if planGameCount <= 0 {
		planGameCount = 5
	}
	if planGameCount > 5 {
		planGameCount = 5
	}
	_, err = l.svcCtx.ArgoClient.EnsureWorkflowFromTemplate(l.ctx, argowf.WorkflowTemplateRef{
		Namespace:    conf.Namespace,
		Name:         argowf.MatchWorkflowName(m.Event, m.Zone, m.Order, m.ID),
		TemplateName: conf.MatchWorkflowTemplate,
		Labels: map[string]string{
			"app.kubernetes.io/name": "rm-monitor",
			"rm-monitor/match-id":    m.ID,
			"rm-monitor/workflow":    "match",
		},
		Annotations: map[string]string{
			"rm-monitor/event":      m.Event,
			"rm-monitor/zone":       m.Zone,
			"rm-monitor/order":      strconv.Itoa(m.Order),
			"rm-monitor/match-type": m.MatchType,
		},
		Arguments: map[string]string{
			"match_id":        m.ID,
			"event":           m.Event,
			"zone":            m.Zone,
			"order":           strconv.Itoa(m.Order),
			"match_type":      m.MatchType,
			"match_slug":      m.MatchSlug,
			"total_rounds":    strconv.Itoa(m.TotalRounds),
			"plan_game_count": strconv.Itoa(planGameCount),
			"priority":        strconv.Itoa(priority.ForSchools(l.svcCtx.Config.Priority, m.RedTeam.SchoolName, m.BlueTeam.SchoolName)),
			"role_specs":      mustJSON(roleSpecs),
			"chat_room_id":    chatRoomID,
			"red_team_id":     m.RedTeam.ID,
			"red_team":        m.RedTeam.Name,
			"red_school":      m.RedTeam.SchoolName,
			"blue_team_id":    m.BlueTeam.ID,
			"blue_team":       m.BlueTeam.Name,
			"blue_school":     m.BlueTeam.SchoolName,
			"latest_status":   m.Status,
			"record_base_dir": l.svcCtx.Config.RecordConf.WithDefaults().BaseDir,
		},
	})
	return err
}

func (l *MatchScanLogic) roleSpecsForMatch(m scannedMatch) ([]roleSpec, string, error) {
	recordConf := l.svcCtx.Config.RecordConf.WithDefaults()
	liveCtx, err := recording.LiveContextForZone(l.ctx, l.svcCtx.RestyClient, recordConf.LiveInfoURL, m.Zone, recordConf.Res)
	if err != nil {
		return nil, "", err
	}
	urls := filterBlacklistedRoles(liveCtx.URLs, recordConf.RoleBlackList)
	roles := make([]string, 0, len(urls))
	for role := range urls {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	specs := make([]roleSpec, 0, len(roles))
	for _, role := range roles {
		specs = append(specs, roleSpec{
			Role:      role,
			SourceURL: urls[role],
			KeepAudio: roleKeepsAudio(recordConf.AudioRoles, role),
		})
	}
	if len(specs) == 0 {
		return nil, liveCtx.ChatRoomID, errors.New("no recordable live roles after blacklist")
	}
	return specs, liveCtx.ChatRoomID, nil
}

func (l *MatchScanLogic) roundWorkflowArguments(m scannedMatch, r *ent.MatchRound, roundDir string, specs []roleSpec, chatRoomID string) (map[string]string, error) {
	recordConf := l.svcCtx.Config.RecordConf.WithDefaults()
	analyzeConf := l.svcCtx.Config.AnalyzeConf.WithDefaults()
	highlightConf := l.svcCtx.Config.HighlightConf.WithDefaults()
	mainRole := preferredMainRole(recordConf, analyzeConf, highlightConf)
	hasMainRole := false
	hasDefaultMainRole := false
	for _, spec := range specs {
		role := strings.TrimSpace(spec.Role)
		hasMainRole = hasMainRole || role == strings.TrimSpace(mainRole)
		hasDefaultMainRole = hasDefaultMainRole || role == "主视角"
	}
	if !hasMainRole && hasDefaultMainRole {
		mainRole = "主视角"
	}
	fpvRecordContexts := make([]jobcontract.RecordContext, 0, len(specs))
	fpvTranscodeContexts := make([]jobcontract.TranscodeContext, 0, len(specs))
	fpvLarkRecordContexts := make([]jobcontract.LarkRecordContext, 0, len(specs))
	outputByRole := make(map[string]string, len(specs))
	var mainRecordContext jobcontract.RecordContext
	var mainSourcePath string
	uploadConf := l.svcCtx.Config.UploadConf.WithDefaults()
	larkRecordEnabled := strings.TrimSpace(uploadConf.BitableAppToken) != ""
	for _, spec := range specs {
		role := spec.Role
		output, err := l.outputPath(recordConf, m, r.RoundNo, role)
		if err != nil {
			return nil, err
		}
		outputByRole[role] = output
		recordCtx := jobcontract.RecordContext{
			Schema:       "rm-monitor/record-context/v1",
			MatchID:      m.ID,
			MatchRoundID: r.ID,
			RoundNo:      r.RoundNo,
			Role:         role,
			SourceURL:    spec.SourceURL,
			OutputPath:   output,
			BaseDir:      recordConf.BaseDir,
			KeepAudio:    spec.KeepAudio,
		}
		if strings.TrimSpace(role) == strings.TrimSpace(mainRole) {
			mainRecordContext = recordCtx
			mainSourcePath = output
			continue
		}
		fpvRecordContexts = append(fpvRecordContexts, recordCtx)
		fpvTranscodeContexts = append(fpvTranscodeContexts, transcodeContextForRole(m, r, recordConf, output, roundDir, role))
		if larkRecordEnabled {
			fpvLarkRecordContexts = append(fpvLarkRecordContexts, larkRecordContextForRole(m, r, recordConf, uploadConf, output, role))
		}
	}
	if mainSourcePath == "" {
		mainSourcePath = outputByRole["主视角"]
	}
	args := map[string]string{
		"main_record_context":      "{}",
		"fpv_record_available":     strconv.FormatBool(len(fpvRecordContexts) > 0),
		"fpv_record_contexts":      mustJSON(fpvRecordContexts),
		"danmu_enabled":            strconv.FormatBool(l.svcCtx.Config.DanmuConf.Enabled && strings.TrimSpace(chatRoomID) != ""),
		"main_source_available":    "false",
		"analyze_enabled":          "false",
		"analyze_context":          "{}",
		"stt_enabled":              "false",
		"stt_context":              "{}",
		"main_lark_record_enabled": strconv.FormatBool(larkRecordEnabled),
		"main_lark_record_context": "{}",
		"fpv_lark_record_enabled":  strconv.FormatBool(len(fpvLarkRecordContexts) > 0),
		"fpv_lark_record_contexts": mustJSON(fpvLarkRecordContexts),
		"main_transcode_context":   "{}",
		"fpv_transcode_available":  strconv.FormatBool(len(fpvTranscodeContexts) > 0),
		"fpv_transcode_contexts":   mustJSON(fpvTranscodeContexts),
		"highlight_enabled":        "false",
		"highlight_context":        "{}",
		"danmu_context": mustJSON(jobcontract.DanmuContext{
			Schema:       "rm-monitor/danmu-context/v1",
			MatchRoundID: r.ID,
			ChatRoomID:   chatRoomID,
			RoundDir:     roundDir,
			StartedAt:    r.StartedAt,
		}),
	}
	if mainSourcePath != "" {
		args["main_source_available"] = "true"
		args["main_record_context"] = mustJSON(mainRecordContext)
		sourceAbs := filepath.Join(recordConf.BaseDir, filepath.FromSlash(mainSourcePath))
		ocrServerConf := l.svcCtx.Config.OCRServerConf.WithDefaults()
		if analyzeConf.Enabled {
			args["analyze_enabled"] = "true"
			args["analyze_context"] = mustJSON(jobcontract.AnalyzeContext{
				Schema:            "rm-monitor/analyze-context/v1",
				MatchRoundID:      r.ID,
				SourcePath:        sourceAbs,
				RoundDir:          roundDir,
				Role:              analyzeConf.Role,
				OCRServerURL:      ocrServerConf.BaseURL,
				OCRTimeoutSeconds: ocrServerConf.TimeoutSeconds,
				Scan: jobcontract.AnalyzeScanContext{
					FPS:                           analyzeConf.Scan.FPS,
					Width:                         analyzeConf.Scan.Width,
					Height:                        analyzeConf.Scan.Height,
					MaxStartScanSeconds:           analyzeConf.Scan.MaxStartScanSeconds,
					MaxSettlementScanSeconds:      analyzeConf.Scan.MaxSettlementScanSeconds,
					SettlementChunkSeconds:        analyzeConf.Scan.SettlementChunkSeconds,
					MinSettlementSecond:           analyzeConf.Scan.MinSettlementSecond,
					MinRoundSeconds:               analyzeConf.Scan.MinRoundSeconds,
					SettlementTailSeconds:         analyzeConf.Scan.SettlementTailSeconds,
					SettlementRefineWindowSeconds: analyzeConf.Scan.SettlementRefineWindowSeconds,
					SettlementRefineFPS:           analyzeConf.Scan.SettlementRefineFPS,
				},
			})
		}
		if strings.TrimSpace(recordConf.STTRole) != "" {
			args["stt_enabled"] = "true"
			args["stt_context"] = mustJSON(jobcontract.STTContext{
				Schema:            "rm-monitor/stt-context/v1",
				MatchRoundID:      r.ID,
				MatchID:           m.ID,
				RoundNo:           r.RoundNo,
				Role:              mainRole,
				SourcePath:        sourceAbs,
				RoundDir:          roundDir,
				STTPath:           filepath.Join(roundDir, "stt.jsonl"),
				SubtitleName:      mainRole + ".srt",
				WhisperServerURLs: resolveWhisperServerURLs(l.svcCtx.Config.WhisperServerUrls),
			})
		}
		if larkRecordEnabled {
			args["main_lark_record_context"] = mustJSON(larkRecordContextForRole(m, r, recordConf, uploadConf, mainSourcePath, mainRole))
		}
		args["main_transcode_context"] = mustJSON(transcodeContextForRole(m, r, recordConf, mainSourcePath, roundDir, mainRole))
		if highlightConf.Enabled {
			args["highlight_enabled"] = "true"
			args["highlight_context"] = mustJSON(map[string]any{
				"schema":         "rm-monitor/highlight-context/v1",
				"match_id":       m.ID,
				"match_round_id": r.ID,
				"round_no":       r.RoundNo,
				"source_path":    mainSourcePath,
				"round_dir":      roundDir,
				"role":           highlightConf.Role,
				"event":          m.Event,
				"zone":           m.Zone,
				"order":          m.Order,
				"match_type":     m.MatchType,
				"red_school":     m.RedTeam.SchoolName,
				"red_name":       m.RedTeam.Name,
				"blue_school":    m.BlueTeam.SchoolName,
				"blue_name":      m.BlueTeam.Name,
				"priority":       priority.ForSchools(l.svcCtx.Config.Priority, m.RedTeam.SchoolName, m.BlueTeam.SchoolName),
			})
		}
	}
	return args, nil
}

func preferredMainRole(recordConf common.RecordConf, analyzeConf common.AnalyzeConf, highlightConf common.HighlightConf) string {
	for _, role := range []string{recordConf.STTRole, analyzeConf.Role, highlightConf.Role, "主视角"} {
		if v := strings.TrimSpace(role); v != "" {
			return v
		}
	}
	return "主视角"
}

func larkRecordContextForRole(m scannedMatch, r *ent.MatchRound, recordConf common.RecordConf, uploadConf common.UploadConf, sourcePath, role string) jobcontract.LarkRecordContext {
	return jobcontract.LarkRecordContext{
		Schema:              "rm-monitor/lark-record-context/v1",
		MatchID:             m.ID,
		MatchRoundID:        r.ID,
		RoundNo:             r.RoundNo,
		Role:                role,
		SourcePath:          sourcePath,
		BaseDir:             recordConf.BaseDir,
		BitableAppToken:     uploadConf.BitableAppToken,
		BitableTableName:    bitableupload.TableName(m.Event, m.Zone),
		AttachmentFieldName: bitableupload.FieldAttachment,
		RecordFields: map[string]any{
			bitableupload.FieldRole:     role,
			bitableupload.FieldMatch:    m.Order,
			bitableupload.FieldRound:    r.RoundNo,
			bitableupload.FieldType:     m.MatchType,
			bitableupload.FieldRedTeam:  bitableupload.TeamName(&ent.Team{Name: m.RedTeam.Name, SchoolName: m.RedTeam.SchoolName}),
			bitableupload.FieldBlueTeam: bitableupload.TeamName(&ent.Team{Name: m.BlueTeam.Name, SchoolName: m.BlueTeam.SchoolName}),
		},
	}
}

func (l *MatchScanLogic) outputPath(conf common.RecordConf, m scannedMatch, roundNo int, role string) (string, error) {
	return pathfmt.Render(conf.MatchNameTemplate, conf.PathTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  m.RedTeam.SchoolName,
		RedName:    m.RedTeam.Name,
		BlueSchool: m.BlueTeam.SchoolName,
		BlueName:   m.BlueTeam.Name,
		RoundNo:    roundNo,
		Role:       role,
	})
}

func transcodeContextForRole(m scannedMatch, r *ent.MatchRound, conf common.RecordConf, sourcePath, roundDir, role string) jobcontract.TranscodeContext {
	archivePath := strings.TrimSuffix(sourcePath, filepath.Ext(sourcePath)) + ".mp4"
	return jobcontract.TranscodeContext{
		Schema:              "rm-monitor/transcode-context/v1",
		MatchID:             m.ID,
		MatchRoundID:        r.ID,
		RoundNo:             r.RoundNo,
		SourcePath:          filepath.Join(conf.BaseDir, filepath.FromSlash(sourcePath)),
		ArchivePath:         filepath.Join(conf.BaseDir, filepath.FromSlash(archivePath)),
		BaseDir:             conf.BaseDir,
		SourceRetentionDays: 7,
		RoundDir:            roundDir,
		Role:                role,
	}
}

func filterBlacklistedRoles(urls map[string]string, blacklist []string) map[string]string {
	if len(blacklist) == 0 {
		return urls
	}
	blocked := make(map[string]struct{}, len(blacklist))
	for _, role := range blacklist {
		blocked[strings.TrimSpace(role)] = struct{}{}
	}
	out := make(map[string]string, len(urls))
	for role, url := range urls {
		if _, ok := blocked[strings.TrimSpace(role)]; ok {
			continue
		}
		out[role] = url
	}
	return out
}

func roleKeepsAudio(audioRoles []string, role string) bool {
	for _, item := range audioRoles {
		if strings.TrimSpace(item) == strings.TrimSpace(role) {
			return true
		}
	}
	return false
}

func resolveWhisperServerURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, item := range urls {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func (l *MatchScanLogic) roundDir(m scannedMatch, roundNo int) (string, error) {
	recordConf := l.svcCtx.Config.RecordConf.WithDefaults()
	matchDir, err := pathfmt.RenderMatchDir(recordConf.MatchNameTemplate, recordConf.MatchDirTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  m.RedTeam.SchoolName,
		RedName:    m.RedTeam.Name,
		BlueSchool: m.BlueTeam.SchoolName,
		BlueName:   m.BlueTeam.Name,
	})
	if err != nil {
		return "", err
	}
	return recordConf.BaseDir + "/" + matchDir + "/Round-" + strconv.Itoa(roundNo), nil
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
			return out
		}
	}
	return out
}

func normalizeMatchResult(value string) string {
	switch value {
	case "RED", "BLUE", "DRAW":
		return value
	default:
		return "UNKNOWN"
	}
}

func matchDecided(m scannedMatch) bool {
	required := winsRequired(m.TotalRounds)
	return required > 0 && (m.RedWinGameCount >= required || m.BlueWinGameCount >= required)
}

func winsRequired(totalRounds int) int {
	if totalRounds <= 0 {
		return 0
	}
	return totalRounds/2 + 1
}

func (l *MatchScanLogic) convergeCompletedRounds(cur scannedMatch) error {
	expected := cur.RedWinGameCount + cur.BlueWinGameCount
	if expected <= 0 {
		return nil
	}
	if cur.TotalRounds > 0 && expected > cur.TotalRounds {
		expected = cur.TotalRounds
	}
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(cur.ID)), matchround.RoundNoLTE(expected)).
		Order(matchround.ByRoundNo()).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query completed rounds")
	}
	winners := authoritativeWinners(rounds, cur.RedWinGameCount, cur.BlueWinGameCount)
	for i, winner := range winners {
		if err := l.ensureEndedRound(cur.ID, i+1, winner); err != nil {
			return err
		}
	}
	return nil
}

func (l *MatchScanLogic) convergeMatchLatestStatus(cur scannedMatch) error {
	if cur.Status == types.MatchStatusSTARTED {
		return nil
	}
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.HasMatchWith(match.ID(cur.ID))).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query match rounds for status convergence")
	}
	if len(rounds) == 0 {
		return nil
	}
	redWins, blueWins := 0, 0
	for _, r := range rounds {
		if r.Status != matchround.StatusENDED {
			return nil
		}
		if r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			redWins++
		case matchround.WinnerBlue:
			blueWins++
		}
	}
	required := winsRequired(cur.TotalRounds)
	completeByScore := required > 0 && (redWins >= required || blueWins >= required)
	completeByRoundCount := cur.TotalRounds > 0 && len(rounds) >= cur.TotalRounds
	if !completeByScore && !completeByRoundCount {
		return nil
	}
	m, err := l.svcCtx.DB.Match.Query().Where(match.ID(cur.ID)).Only(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query match for status convergence")
	}
	needsStatus := m.LatestStatus != "DONE"
	needsResult := m.Result == match.ResultUNKNOWN
	if !needsStatus && !needsResult {
		return nil
	}
	update := l.svcCtx.DB.Match.UpdateOneID(cur.ID)
	if needsStatus {
		update.SetLatestStatus("DONE")
	}
	if needsResult {
		switch {
		case redWins > blueWins:
			update.SetResult(match.ResultRED)
		case blueWins > redWins:
			update.SetResult(match.ResultBLUE)
		default:
			update.SetResult(match.ResultDRAW)
		}
	}
	if err := update.Exec(l.ctx); err != nil {
		return errors.Wrap(err, "set converged match status")
	}
	return nil
}

func authoritativeWinners(rounds []*ent.MatchRound, redWins, blueWins int) []matchround.Winner {
	total := redWins + blueWins
	out := make([]matchround.Winner, total)
	used := make([]bool, total)
	redUsed, blueUsed := 0, 0
	for _, r := range rounds {
		idx := r.RoundNo - 1
		if idx < 0 || idx >= total || r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			if redUsed < redWins {
				out[idx] = matchround.WinnerRed
				used[idx] = true
				redUsed++
			}
		case matchround.WinnerBlue:
			if blueUsed < blueWins {
				out[idx] = matchround.WinnerBlue
				used[idx] = true
				blueUsed++
			}
		}
	}
	for i := total - 1; i >= 0; i-- {
		if used[i] {
			continue
		}
		switch {
		case blueUsed < blueWins:
			out[i] = matchround.WinnerBlue
			blueUsed++
		case redUsed < redWins:
			out[i] = matchround.WinnerRed
			redUsed++
		default:
			out[i] = matchround.WinnerDraw
		}
	}
	return out
}
