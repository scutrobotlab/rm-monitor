package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/pkg/sttcoord"
)

// WriteMatchReadme writes the match manifest into the match directory.
func WriteMatchReadme(ctx context.Context, client *ent.Client, redisClient *redisx.Client, conf common.RecordConf, reportConf common.ReportConf, postgresDSN, matchID string) error {
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
		sttStatuses := waitForSTTStatuses(ctx, redisClient, m)
		if report, err := generateMatchReport(ctx, reportConf, m, red, blue, fullDir, sttStatuses); err != nil {
			logx.Errorf("generate match report %s failed: %v", m.ID, err)
		} else if strings.TrimSpace(report) != "" {
			if err := client.Match.UpdateOneID(m.ID).SetReport(report).Exec(ctx); err != nil {
				return errors.Wrap(err, "save match report")
			}
			m.Report = &report
			if postgresDSN != "" {
				_ = db.Notify(ctx, postgresDSN, db.MatchChangedChannel, m.ID)
			}
		}
	}
	unlock, err := lockDir(ctx, fullDir)
	if err != nil {
		return err
	}
	defer unlock()

	tmp := filepath.Join(fullDir, ".README.md.tmp")
	dst := filepath.Join(fullDir, "README.md")
	readme, err := renderReadme(m, red, blue)
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

const (
	sttWaitTimeout  = 30 * time.Minute
	sttPollInterval = 5 * time.Second
)

func waitForSTTStatuses(ctx context.Context, redisClient *redisx.Client, m *ent.Match) map[string]string {
	deadline := time.Now().Add(sttWaitTimeout)
	for {
		statuses, err := sttcoord.GetMatch(ctx, redisClient, m.ID)
		if err != nil {
			logx.Errorf("read stt status for match %s failed: %v", m.ID, err)
			return nil
		}
		if len(statuses) == 0 || len(pendingSTTRounds(m, statuses)) == 0 {
			return statuses
		}
		if time.Now().After(deadline) {
			logx.Errorf("wait stt status for match %s timed out, pending rounds: %v", m.ID, pendingSTTRounds(m, statuses))
			return statuses
		}
		select {
		case <-ctx.Done():
			logx.Errorf("wait stt status for match %s canceled: %v", m.ID, ctx.Err())
			return statuses
		case <-time.After(sttPollInterval):
		}
	}
}

func pendingSTTRounds(m *ent.Match, statuses map[string]string) []int {
	if m == nil || len(statuses) == 0 {
		return nil
	}
	pending := make([]int, 0)
	for _, r := range m.Edges.Rounds {
		if statuses[sttcoord.Field(r.RoundNo)] == sttcoord.StatusPending {
			pending = append(pending, r.RoundNo)
		}
	}
	return pending
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

type readmeData struct {
	Title    string
	InfoRows []tableRow
	Rounds   []readmeRoundRow
	Report   string
}

func renderReadme(m *ent.Match, red, blue *ent.Team) (string, error) {
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

func generateMatchReport(ctx context.Context, c common.ReportConf, m *ent.Match, red, blue *ent.Team, matchDir string, sttStatuses map[string]string) (string, error) {
	c = c.WithDefaults()
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.Model) == "" {
		return "", errors.New("report llm config is incomplete")
	}
	input, err := buildReportInput(m, red, blue, matchDir, sttStatuses)
	if err != nil {
		return "", err
	}
	return callReportLLM(ctx, c, input)
}

const reportPromptTemplate = `【角色】
你是 RoboMaster 机甲大师赛事战报编辑，熟悉机甲大师比赛表达方式。

【指令】
请根据下面的比赛信息和解说摘录，写一篇面向普通观众的中文 Markdown 简介战报。

【上下文】
这是一场 RoboMaster 机甲大师比赛。读者希望快速知道谁赢了、比分如何、每局大致发生了什么、比赛有什么看点。

【输入数据】
- 比赛：{{ .Title }}
- 赛事：{{ .Event }}
- 赛区：{{ .Zone }}
- 类型：{{ .MatchType }}
- 比分：{{ .Score }}
- 胜方：{{ .Winner }}
- 胜者去向：{{ .WinnerPlaceholder }}
- 败者去向：{{ .LoserPlaceholder }}
{{ range .Rounds }}
- Round {{ .RoundNo }}：{{ .Status }}，胜方 {{ .Winner }}，时间 {{ .StartedAt }} - {{ .EndedAt }}
{{- if .CommentaryLines }}
  解说摘录：
{{- range .CommentaryLines }}
  - {{ . }}
{{- end }}
{{ else }}
  解说摘录：无。
{{ end }}
{{ end }}

【输出格式】
- 只输出 Markdown 正文，不要用代码块包裹。
- 建议结构：
  1. 一级标题：一句话概括对阵和赛果。
  2. “比赛概况”：2-3 句话说明赛事、赛区、比分、胜方。
  3. “小局回顾”：按 Round 简短概括，每局 1 句话。
  4. “看点”：列出 2-3 条观众关心的比赛看点。

【期望】
- 简洁、直白、像赛事新闻稿，不要写成技术报告。
- 不要出现“AI、模型、系统、结构化数据、STT、识别文本、自动生成、数据待完善”等开发或内部实现表述。
- 不要编造输入中没有的具体机器人动作、击毁、经济、点位或战术细节。
- 胜者去向和败者去向来自赛程占位字段，可能为空，也可能只是“胜者1”这类内部占位；只有当它明确表达后续赛程、轮次或对阵安排时才纳入战报，不要强行解读。
- 如果解说摘录信息不足，就基于比分、胜方和小局结果写保守概括，不要说明“信息不足”。`

var reportPromptTmpl = template.Must(template.New("report-prompt").Parse(reportPromptTemplate))

type reportRoundData struct {
	RoundNo         int
	Status          string
	Winner          string
	StartedAt       string
	EndedAt         string
	CommentaryLines []string
}

type reportPromptData struct {
	Title             string
	Event             string
	Zone              string
	MatchType         string
	Score             string
	Winner            string
	WinnerPlaceholder string
	LoserPlaceholder  string
	Rounds            []reportRoundData
}

func buildReportInput(m *ent.Match, red, blue *ent.Team, matchDir string, sttStatuses map[string]string) (string, error) {
	data := reportPromptData{
		Title:             matchTitle(m, red, blue),
		Event:             m.Event,
		Zone:              m.Zone,
		MatchType:         m.MatchType,
		Score:             scoreText(m.Edges.Rounds),
		Winner:            matchWinnerText(m, red, blue),
		WinnerPlaceholder: placeholderText(m.WinnerPlaceholderName),
		LoserPlaceholder:  placeholderText(m.LoserPlaceholderName),
	}
	for _, r := range m.Edges.Rounds {
		row := reportRoundData{
			RoundNo:   r.RoundNo,
			Status:    displayRoundStatus(r.Status),
			Winner:    roundWinnerText(r, red, blue),
			StartedAt: formatDisplayTime(r.StartedAt),
			EndedAt:   formatOptionalDisplayTime(r.EndedAt),
		}
		if sttStatuses[sttcoord.Field(r.RoundNo)] == sttcoord.StatusFailed {
			data.Rounds = append(data.Rounds, row)
			continue
		}
		lines, err := readRoundSTT(matchDir, r.RoundNo)
		if err != nil {
			return "", err
		}
		for _, line := range lines {
			if line.Status != "SUCCEEDED" || strings.TrimSpace(line.Text) == "" {
				continue
			}
			row.CommentaryLines = append(row.CommentaryLines, fmt.Sprintf("%.0fs-%.0fs：%s", line.Start, line.End, strings.TrimSpace(line.Text)))
		}
		data.Rounds = append(data.Rounds, row)
	}
	var out strings.Builder
	if err := reportPromptTmpl.Execute(&out, data); err != nil {
		return "", errors.Wrap(err, "render report prompt")
	}
	return out.String(), nil
}

func readRoundSTT(matchDir string, roundNo int) ([]sttLine, error) {
	path := filepath.Join(matchDir, fmt.Sprintf("Round-%d", roundNo), "stt.jsonl")
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

func callReportLLM(ctx context.Context, c common.ReportConf, input string) (string, error) {
	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")),
		option.WithAPIKey(c.APIKey),
		option.WithHTTPClient(&http.Client{Timeout: time.Duration(c.TimeoutSeconds) * time.Second}),
	)
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(c.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("你是 RoboMaster 机甲大师赛事战报编辑。只输出面向观众的简洁 Markdown 战报，不输出代码块，不提及 AI、模型、系统、STT 或自动生成。"),
			openai.UserMessage(input),
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return "", err
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return "", errors.New("llm returned empty report")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
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
