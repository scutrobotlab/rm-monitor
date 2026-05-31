package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/difyworkflow"
	"scutbot.cn/web/rm-monitor/pkg/highlight"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

// WriteMatchReadme writes the match manifest into the match directory.
func WriteMatchReadme(ctx context.Context, client *ent.Client, redisClient *redisx.Client, conf common.RecordConf, difyConf common.DifyConf, manifestConf common.ManifestConf, postgresDSN, matchID string) error {
	conf = conf.WithDefaults()
	m, err := client.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo()).
				WithRecordTasks(func(q *ent.RecordTaskQuery) {
					q.WithMediaArtifacts().WithUploadTask()
				})
		}).
		Only(ctx)
	if err != nil {
		return err
	}
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return err
	}
	matchDir, err := pathfmt.RenderMatchDir(conf.MatchNameTemplate, conf.MatchDirTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
	})
	if err != nil {
		return err
	}
	fullDir := filepath.Join(conf.BaseDir, filepath.FromSlash(matchDir))
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return err
	}
	if matchComplete(m) && (m.Report == nil || strings.TrimSpace(*m.Report) == "") {
		report, reportJSON, err := generateMatchReport(ctx, difyConf, manifestConf, m, red, blue, fullDir)
		if err != nil {
			return errors.Wrapf(err, "generate match report %s", m.ID)
		}
		if err := writeReportJSON(fullDir, reportJSON); err != nil {
			return err
		}
		if err := client.Match.UpdateOneID(m.ID).SetReport(report).Exec(ctx); err != nil {
			return errors.Wrap(err, "save match report")
		}
		m.Report = &report
		if postgresDSN != "" {
			_ = db.Notify(ctx, postgresDSN, db.MatchChangedChannel, m.ID)
		}
	}
	unlock, err := lockDir(ctx, fullDir)
	if err != nil {
		return err
	}
	defer unlock()

	tmp := filepath.Join(fullDir, ".README.md.tmp")
	dst := filepath.Join(fullDir, "README.md")
	readme, err := renderReadme(m, red, blue, fullDir)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(readme), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return errors.Wrap(err, "rename readme")
	}
	return nil
}

func matchComplete(m *ent.Match) bool {
	if m == nil || strings.ToUpper(m.LatestStatus) != "DONE" || len(m.Edges.Rounds) == 0 {
		return false
	}
	for _, r := range m.Edges.Rounds {
		if r.Status != matchround.StatusENDED {
			return false
		}
	}
	return true
}

func lockDir(ctx context.Context, dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".README.md.lock")
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(fmt.Sprintf("pid=%d time=%s\n", os.Getpid(), time.Now().Format(time.RFC3339)))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

const readmeTemplate = `# {{ .Title }}

## 比赛信息

| 项目 | 内容 |
| --- | --- |
{{ range .InfoRows }}| {{ .Key }} | {{ .Value }} |
{{ end }}

## 小局历程

| 小局 | 状态 | 胜方 | 开始时间 | 结束时间 |
| --- | --- | --- | --- | --- |
{{ range .Rounds }}| {{ .RoundNo }} | {{ .Status }} | {{ .Winner }} | {{ .StartedAt }} | {{ .EndedAt }} |
{{ end }}
{{ if .DanmuCharts }}

## 弹幕统计

{{ range .DanmuCharts }}### Round {{ .RoundNo }}
{{ if .DanmuCountImage }}![Round {{ .RoundNo }} 弹幕数量]({{ .DanmuCountImage }})
{{ end }}{{ if .OnlineCountImage }}![Round {{ .RoundNo }} 在线人数]({{ .OnlineCountImage }})
{{ end }}
{{ end }}{{ end }}
{{ if .Report }}

## 战报

{{ .Report }}
{{ end }}
`

var readmeTmpl = template.Must(template.New("readme").Parse(readmeTemplate))

type tableRow struct {
	Key   string
	Value string
}

type readmeRoundRow struct {
	RoundNo   int
	Status    string
	Winner    string
	StartedAt string
	EndedAt   string
}

type readmeRecordRow struct {
	RoundNo     int
	Role        string
	SourcePath  string
	ArchivePath string
	UploadURL   string
}

type readmeDanmuChartRow struct {
	RoundNo          int
	DanmuCountImage  string
	OnlineCountImage string
}

type readmeData struct {
	Title       string
	InfoRows    []tableRow
	Rounds      []readmeRoundRow
	DanmuCharts []readmeDanmuChartRow
	Report      string
}

func renderReadme(m *ent.Match, red, blue *ent.Team, matchDir string) (string, error) {
	data := readmeData{
		Title: markdownText(matchTitle(m, red, blue)),
		InfoRows: []tableRow{
			readmeInfoRow("赛事", m.Event),
			readmeInfoRow("赛区", m.Zone),
			readmeInfoRow("场次", fmt.Sprintf("%d", m.Order)),
			readmeInfoRow("类型", m.MatchType),
			readmeInfoRow("红方", teamName(red)),
			readmeInfoRow("蓝方", teamName(blue)),
			readmeInfoRow("状态", displayStatus(m)),
			readmeInfoRow("比分", scoreText(m.Edges.Rounds)),
			readmeInfoRow("胜方", matchWinnerText(m, red, blue)),
			readmeInfoRow("胜者去向", placeholderText(m.WinnerPlaceholderName)),
			readmeInfoRow("败者去向", placeholderText(m.LoserPlaceholderName)),
			readmeInfoRow("开始时间", formatDisplayTime(firstStartedAt(m.Edges.Rounds))),
			readmeInfoRow("结束时间", formatOptionalDisplayTime(lastEndedAt(m.Edges.Rounds))),
			readmeInfoRow("Match ID", m.ID),
		},
	}
	if m.MatchSlug != nil && *m.MatchSlug != "" {
		data.InfoRows = append(data.InfoRows, readmeInfoRow("Match Slug", *m.MatchSlug))
	}
	for _, r := range m.Edges.Rounds {
		data.Rounds = append(data.Rounds, readmeRoundRow{
			RoundNo:   r.RoundNo,
			Status:    markdownCell(displayRoundStatus(r.Status)),
			Winner:    markdownCell(roundWinnerText(r, red, blue)),
			StartedAt: markdownCell(formatDisplayTime(r.StartedAt)),
			EndedAt:   markdownCell(formatOptionalDisplayTime(r.EndedAt)),
		})
		if chart := danmuChartRow(matchDir, r.RoundNo); chart.DanmuCountImage != "" || chart.OnlineCountImage != "" {
			data.DanmuCharts = append(data.DanmuCharts, chart)
		}
	}
	if m.Report != nil {
		data.Report = strings.TrimSpace(*m.Report)
	}

	var out strings.Builder
	if err := readmeTmpl.Execute(&out, data); err != nil {
		return "", errors.Wrap(err, "render readme")
	}
	return out.String(), nil
}

func danmuChartRow(matchDir string, roundNo int) readmeDanmuChartRow {
	row := readmeDanmuChartRow{RoundNo: roundNo}
	roundDir := filepath.Join(matchDir, fmt.Sprintf("Round-%d", roundNo))
	if fileExists(filepath.Join(roundDir, "stats", "danmu-count.png")) {
		row.DanmuCountImage = filepath.ToSlash(filepath.Join(fmt.Sprintf("Round-%d", roundNo), "stats", "danmu-count.png"))
	}
	if fileExists(filepath.Join(roundDir, "stats", "online-count.png")) {
		row.OnlineCountImage = filepath.ToSlash(filepath.Join(fmt.Sprintf("Round-%d", roundNo), "stats", "online-count.png"))
	}
	return row
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readmeInfoRow(key, value string) tableRow {
	return tableRow{Key: markdownCell(key), Value: markdownCell(value)}
}

type sttLine struct {
	Index        int     `json:"index"`
	SegmentID    int     `json:"segment_id"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Status       string  `json:"status"`
	Text         string  `json:"text"`
	ErrorMessage string  `json:"error_message"`
}

type reportPayload struct {
	Schema string               `json:"schema"`
	Match  reportMatchPayload   `json:"match"`
	Rounds []reportRoundPayload `json:"rounds"`
}

type reportMatchPayload struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Event             string `json:"event"`
	Zone              string `json:"zone"`
	Order             int    `json:"order"`
	MatchType         string `json:"match_type"`
	Score             string `json:"score"`
	Winner            string `json:"winner"`
	RedTeam           string `json:"red_team"`
	BlueTeam          string `json:"blue_team"`
	RoundCount        int    `json:"round_count"`
	WinnerPlaceholder string `json:"winner_placeholder,omitempty"`
	LoserPlaceholder  string `json:"loser_placeholder,omitempty"`
}

type reportRoundPayload struct {
	RoundNo         int               `json:"round_no"`
	Status          string            `json:"status"`
	Winner          string            `json:"winner"`
	StartedAt       string            `json:"started_at,omitempty"`
	EndedAt         string            `json:"ended_at,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	STTLines        []reportSTTLine   `json:"stt_lines,omitempty"`
	STTTruncated    bool              `json:"stt_truncated,omitempty"`
	Danmu           *danmuSummary     `json:"danmu,omitempty"`
	Online          *onlineSummary    `json:"online,omitempty"`
	OCRSettlement   json.RawMessage   `json:"ocr_settlement,omitempty"`
	Highlights      []json.RawMessage `json:"highlights,omitempty"`
}

type reportSTTLine struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type danmuSummary struct {
	BucketSeconds int              `json:"bucket_seconds"`
	Total         int              `json:"total"`
	Peaks         []reportPeakInfo `json:"peaks"`
}

type onlineSummary struct {
	Samples int      `json:"samples"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Last    *float64 `json:"last,omitempty"`
}

type reportPeakInfo struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	PeakSeconds  float64 `json:"peak_seconds"`
	PeakCount    int     `json:"peak_count"`
	Score        float64 `json:"score"`
}

func generateMatchReport(ctx context.Context, difyConf common.DifyConf, manifestConf common.ManifestConf, m *ent.Match, red, blue *ent.Team, matchDir string) (string, json.RawMessage, error) {
	payload, err := buildReportPayload(m, red, blue, matchDir)
	if err != nil {
		return "", nil, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.Wrap(err, "marshal report payload")
	}
	client, err := difyworkflow.New(difyConf)
	if err != nil {
		return "", nil, err
	}
	result, err := client.RunWorkflow(ctx, manifestConf.ReportWorkflowAPIKey, "rm-monitor:manifest:"+m.ID, map[string]any{
		"payload": string(payloadJSON),
	})
	if err != nil {
		return "", nil, err
	}
	report, err := difyworkflow.StringOutput(result.Outputs, "report_markdown")
	if err != nil {
		return "", nil, err
	}
	reportJSON, err := difyworkflow.RawOutput(result.Outputs, "report_json")
	if err != nil {
		return "", nil, err
	}
	return report, reportJSON, nil
}

func writeReportJSON(matchDir string, raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("report_json is not valid json")
	}
	tmp := filepath.Join(matchDir, ".report.json.tmp")
	dst := filepath.Join(matchDir, "report.json")
	if err := os.WriteFile(tmp, append([]byte(raw), '\n'), 0o644); err != nil {
		return err
	}
	return errors.Wrap(os.Rename(tmp, dst), "rename report.json")
}

func buildReportPayload(m *ent.Match, red, blue *ent.Team, matchDir string) (reportPayload, error) {
	payload := reportPayload{
		Schema: "rm-monitor/dify-report-input/v1",
		Match: reportMatchPayload{
			ID:                m.ID,
			Title:             matchTitle(m, red, blue),
			Event:             m.Event,
			Zone:              m.Zone,
			Order:             m.Order,
			MatchType:         m.MatchType,
			Score:             scoreText(m.Edges.Rounds),
			Winner:            matchWinnerText(m, red, blue),
			RedTeam:           teamName(red),
			BlueTeam:          teamName(blue),
			RoundCount:        len(m.Edges.Rounds),
			WinnerPlaceholder: placeholderText(m.WinnerPlaceholderName),
			LoserPlaceholder:  placeholderText(m.LoserPlaceholderName),
		},
	}
	for _, r := range m.Edges.Rounds {
		roundDir := filepath.Join(matchDir, fmt.Sprintf("Round-%d", r.RoundNo))
		sttLines, sttTruncated, err := readReportSTT(roundDir, nil)
		if err != nil {
			return payload, err
		}
		danmu, peaks := readDanmuSummary(roundDir)
		if len(peaks) > 0 {
			sttLines, sttTruncated, err = readReportSTT(roundDir, peaks)
			if err != nil {
				return payload, err
			}
		}
		row := reportRoundPayload{
			RoundNo:         r.RoundNo,
			Status:          displayRoundStatus(r.Status),
			Winner:          roundWinnerText(r, red, blue),
			StartedAt:       formatDisplayTime(r.StartedAt),
			EndedAt:         formatOptionalDisplayTime(r.EndedAt),
			DurationSeconds: roundDurationSeconds(r),
			STTLines:        sttLines,
			STTTruncated:    sttTruncated,
			Danmu:           danmu,
			Online:          readOnlineSummary(roundDir),
			OCRSettlement:   readOptionalJSON(filepath.Join(roundDir, "settlement.json")),
			Highlights:      readHighlightJSONs(roundDir),
		}
		payload.Rounds = append(payload.Rounds, row)
	}
	return payload, nil
}

func readRoundSTT(matchDir string, roundNo int) ([]sttLine, error) {
	roundDir := filepath.Join(matchDir, fmt.Sprintf("Round-%d", roundNo))
	path := preferredSTTPath(roundDir)
	return readSTTFile(path)
}

func preferredSTTPath(roundDir string) string {
	cleaned := filepath.Join(roundDir, "stt.jsonl")
	if _, err := os.Stat(cleaned); err == nil {
		return cleaned
	}
	return filepath.Join(roundDir, "stt.raw.jsonl")
}

func readSTTFile(path string) ([]sttLine, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "read stt jsonl")
	}
	rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
	out := make([]sttLine, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}
		var line sttLine
		if err := json.Unmarshal([]byte(row), &line); err != nil {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

const maxReportSTTLinesPerRound = 120

func readReportSTT(roundDir string, peaks []reportPeakInfo) ([]reportSTTLine, bool, error) {
	lines, err := readSTTFile(preferredSTTPath(roundDir))
	if err != nil {
		return nil, false, err
	}
	var succeeded []reportSTTLine
	for _, line := range lines {
		if line.Status != "SUCCEEDED" || strings.TrimSpace(line.Text) == "" {
			continue
		}
		succeeded = append(succeeded, reportSTTLine{
			Start: line.Start,
			End:   line.End,
			Text:  strings.TrimSpace(line.Text),
		})
	}
	if len(succeeded) <= maxReportSTTLinesPerRound {
		return succeeded, false, nil
	}
	selected := make(map[int]bool)
	addRange := func(start, end int) {
		if start < 0 {
			start = 0
		}
		if end > len(succeeded) {
			end = len(succeeded)
		}
		for i := start; i < end; i++ {
			selected[i] = true
		}
	}
	addRange(0, 20)
	addRange(len(succeeded)-20, len(succeeded))
	for _, peak := range peaks {
		for i, line := range succeeded {
			if line.End >= peak.PeakSeconds-30 && line.Start <= peak.PeakSeconds+30 {
				addRange(i-2, i+3)
			}
		}
	}
	out := make([]reportSTTLine, 0, maxReportSTTLinesPerRound)
	for i, line := range succeeded {
		if selected[i] {
			out = append(out, line)
		}
		if len(out) >= maxReportSTTLinesPerRound {
			break
		}
	}
	return out, true, nil
}

func roundDurationSeconds(r *ent.MatchRound) float64 {
	if r == nil || r.EndedAt == nil {
		return 0
	}
	return r.EndedAt.Sub(r.StartedAt).Seconds()
}

func readDanmuSummary(roundDir string) (*danmuSummary, []reportPeakInfo) {
	stats, err := highlight.LoadDanmuStats(filepath.Join(roundDir, "stats", "danmu-count.json"))
	if err != nil {
		return nil, nil
	}
	var total int
	for _, p := range stats.Points {
		if p.Total > total {
			total = p.Total
		}
	}
	candidates := highlight.FindCandidates(stats, highlight.OnlineStats{}, common.HighlightConf{})
	countByT := make(map[float64]int, len(stats.Points))
	for _, p := range stats.Points {
		countByT[p.T] = p.Count
	}
	peaks := make([]reportPeakInfo, 0, len(candidates))
	for _, c := range candidates {
		peaks = append(peaks, reportPeakInfo{
			StartSeconds: c.Start,
			EndSeconds:   c.End,
			PeakSeconds:  c.Peak,
			PeakCount:    countByT[c.Peak],
			Score:        c.Score,
		})
	}
	return &danmuSummary{
		BucketSeconds: stats.BucketSeconds,
		Total:         total,
		Peaks:         peaks,
	}, peaks
}

func readOnlineSummary(roundDir string) *onlineSummary {
	stats, err := highlight.LoadOnlineStats(filepath.Join(roundDir, "stats", "online-count.json"))
	if err != nil || len(stats.Points) == 0 {
		return nil
	}
	var out onlineSummary
	for _, p := range stats.Points {
		if p.OnlineCount == nil {
			continue
		}
		v := *p.OnlineCount
		out.Samples++
		out.Last = floatPtr(v)
		if out.Min == nil || v < *out.Min {
			out.Min = floatPtr(v)
		}
		if out.Max == nil || v > *out.Max {
			out.Max = floatPtr(v)
		}
	}
	if out.Samples == 0 {
		return nil
	}
	return &out
}

func floatPtr(v float64) *float64 {
	return &v
}

func readOptionalJSON(path string) json.RawMessage {
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		return nil
	}
	return json.RawMessage(raw)
}

func readHighlightJSONs(roundDir string) []json.RawMessage {
	matches, err := filepath.Glob(filepath.Join(roundDir, "highlights", "*", "highlight.json"))
	if err != nil {
		return nil
	}
	out := make([]json.RawMessage, 0, len(matches))
	for _, path := range matches {
		if raw := readOptionalJSON(path); len(raw) > 0 {
			out = append(out, raw)
		}
	}
	return out
}

func matchTitle(m *ent.Match, red, blue *ent.Team) string {
	return fmt.Sprintf("%d. %s VS %s", m.Order, teamName(red), teamName(blue))
}

func teamName(t *ent.Team) string {
	if t == nil {
		return ""
	}
	switch {
	case t.SchoolName != "" && t.Name != "":
		return t.SchoolName + "-" + t.Name
	case t.SchoolName != "":
		return t.SchoolName
	default:
		return t.Name
	}
}

func matchWinner(m *ent.Match) string {
	if m == nil {
		return ""
	}
	switch m.Result {
	case match.ResultRED:
		return "red"
	case match.ResultBLUE:
		return "blue"
	case match.ResultDRAW:
		return "draw"
	default:
		return ""
	}
}

func matchWinnerText(m *ent.Match, red, blue *ent.Team) string {
	switch matchWinner(m) {
	case "red":
		return "红方（" + teamName(red) + "）"
	case "blue":
		return "蓝方（" + teamName(blue) + "）"
	case "draw":
		return "平局"
	default:
		return ""
	}
}

func placeholderText(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func scoreText(rounds []*ent.MatchRound) string {
	redWins, blueWins := 0, 0
	for _, r := range rounds {
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
	return fmt.Sprintf("红 %d - %d 蓝", redWins, blueWins)
}

func roundWinner(r *ent.MatchRound) string {
	if r.Winner == nil {
		return ""
	}
	return string(*r.Winner)
}

func roundWinnerText(r *ent.MatchRound, red, blue *ent.Team) string {
	switch roundWinner(r) {
	case string(matchround.WinnerRed):
		return "红方（" + teamName(red) + "）"
	case string(matchround.WinnerBlue):
		return "蓝方（" + teamName(blue) + "）"
	case string(matchround.WinnerDraw):
		return "平局"
	default:
		return ""
	}
}

func displayStatus(m *ent.Match) string {
	hasRound := false
	allEnded := true
	for _, r := range m.Edges.Rounds {
		hasRound = true
		if r.Status == matchround.StatusSTARTED {
			return "进行中"
		}
		if r.Status != matchround.StatusENDED {
			allEnded = false
		}
	}
	if hasRound && allEnded {
		return "已结束"
	}
	switch strings.ToUpper(m.LatestStatus) {
	case "DONE", "ENDED", "FINISHED":
		return "已结束"
	case "STARTED", "RUNNING":
		return "进行中"
	case "PENDING", "WAITING":
		return "未开始"
	default:
		return m.LatestStatus
	}
}

func displayRoundStatus(status matchround.Status) string {
	switch status {
	case matchround.StatusSTARTED:
		return "进行中"
	case matchround.StatusENDED:
		return "已结束"
	default:
		return string(status)
	}
}

func firstStartedAt(rounds []*ent.MatchRound) time.Time {
	var first time.Time
	for _, r := range rounds {
		if r.StartedAt.IsZero() {
			continue
		}
		if first.IsZero() || r.StartedAt.Before(first) {
			first = r.StartedAt
		}
	}
	return first
}

func lastEndedAt(rounds []*ent.MatchRound) *time.Time {
	var last *time.Time
	for _, r := range rounds {
		if r.EndedAt == nil || r.EndedAt.IsZero() {
			continue
		}
		if last == nil || r.EndedAt.After(*last) {
			v := *r.EndedAt
			last = &v
		}
	}
	return last
}

func formatDisplayTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatOptionalDisplayTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatDisplayTime(*t)
}

func markdownText(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}

func markdownCell(s string) string {
	s = markdownText(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "|", `\|`)
}
