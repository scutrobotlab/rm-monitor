package main

import (
	"bufio"
	"bytes"
	"context"
	stdsql "database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/errors"
	"resty.dev/v3"

	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/pkg/sttcoord"
	"scutbot.cn/web/rm-monitor/pkg/stttext"
)

const segmentSeconds = 60

type toolConfig struct {
	PostgresConf     common.PostgresConf
	RedisConf        common.RedisConf
	RecordConf       common.RecordConf
	WhisperServerUrl string `json:",optional"`
}

type row struct {
	MatchID          string
	RoundID          int
	RoundNo          int
	SourcePath       string
	HasSuccessfulSTT bool
	Event            string
	Zone             string
	MatchType        string
	Order            int
	RedSchool        string
	RedName          string
	BlueSchool       string
	BlueName         string
}

type recoverResult struct {
	Row        row
	Metric     recoverMetric
	Err        error
	Skipped    bool
	SkipReason string
}

type recoverMetric struct {
	Segments     int
	APISeconds   float64
	WallSeconds  float64
	AudioSeconds float64
}

type sttLine struct {
	Index        int     `json:"index"`
	Part         int     `json:"part,omitempty"`
	SegmentID    int     `json:"segment_id,omitempty"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Status       string  `json:"status"`
	APISeconds   float64 `json:"api_seconds,omitempty"`
	Text         string  `json:"text,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

type whisperResult struct {
	Duration float64          `json:"duration"`
	Text     string           `json:"text"`
	Segments []whisperSegment `json:"segments"`
}

type whisperSegment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func main() {
	var (
		configFile       = flag.String("f", "etc/config.yml", "config file, normally stt-job config")
		roundIDsFlag     = flag.String("round", "", "comma separated match_round ids")
		matchIDsFlag     = flag.String("match", "", "comma separated match ids")
		limit            = flag.Int("limit", 10, "candidate limit")
		concurrency      = flag.Int("concurrency", 1, "parallel round recoveries")
		listOnly         = flag.Bool("list", false, "only list candidates")
		force            = flag.Bool("force", false, "rebuild stt even when stt.jsonl already has SUCCEEDED")
		failedOnly       = flag.Bool("failed-only", false, "only recover rounds with an existing stt.jsonl that has no SUCCEEDED rows")
		noReportReset    = flag.Bool("no-report-reset", false, "do not clear matches.report after recovered stt")
		noHighlightReset = flag.Bool("no-highlight-reset", false, "do not delete non-succeeded highlight clips after recovered stt")
		benchmarkSource  = flag.String("benchmark-source", "", "source FLV path for one 60s whisper benchmark")
	)
	flag.Parse()

	var c toolConfig
	if err := app.LoadConfig(*configFile, &c); err != nil {
		fatal(err)
	}
	c.RecordConf = c.RecordConf.WithDefaults()
	if strings.TrimSpace(c.WhisperServerUrl) == "" {
		fatal(errors.New("WhisperServerUrl is required"))
	}

	ctx := context.Background()
	if strings.TrimSpace(*benchmarkSource) != "" {
		if err := runBenchmark(ctx, c, *benchmarkSource); err != nil {
			fatal(err)
		}
		return
	}

	client, err := db.Open(ctx, c.PostgresConf)
	if err != nil {
		fatal(err)
	}
	defer client.Close()

	sqlDB, err := stdsql.Open("pgx", c.PostgresConf.DSN)
	if err != nil {
		fatal(err)
	}
	defer sqlDB.Close()

	rows, err := queryRows(ctx, sqlDB, c.RecordConf.STTRole, parseIntList(*roundIDsFlag), parseStringList(*matchIDsFlag), *limit)
	if err != nil {
		fatal(err)
	}
	if len(rows) == 0 {
		fmt.Println("no candidate rounds found")
		return
	}
	for i := range rows {
		rows[i].HasSuccessfulSTT = hasSuccessfulSTT(c, rows[i].SourcePath)
	}
	if *failedOnly {
		rows = filterFailedSTT(c, rows)
	}
	printRows(rows)
	if *listOnly {
		return
	}

	results := recoverRows(ctx, c, rows, *force, max(1, *concurrency))
	recovered := make([]row, 0, len(results))
	var total recoverMetric
	for _, result := range results {
		if result.Skipped {
			fmt.Printf("skip round=%d: %s\n", result.Row.RoundID, result.SkipReason)
			continue
		}
		if result.Err != nil {
			fatal(errors.Wrapf(result.Err, "recover round %d", result.Row.RoundID))
		}
		fmt.Printf("done round=%d segments=%d api_seconds=%.3f wall_seconds=%.3f audio_seconds=%.1f\n",
			result.Row.RoundID, result.Metric.Segments, result.Metric.APISeconds, result.Metric.WallSeconds, result.Metric.AudioSeconds)
		total.Segments += result.Metric.Segments
		total.APISeconds += result.Metric.APISeconds
		total.WallSeconds += result.Metric.WallSeconds
		total.AudioSeconds += result.Metric.AudioSeconds
		recovered = append(recovered, result.Row)
	}
	if len(recovered) == 0 {
		fmt.Println("no rounds need stt recovery")
		return
	}
	if err := markSTTDone(ctx, c, recovered); err != nil {
		fatal(err)
	}
	if !*noReportReset {
		if err := resetReportsAndHighlights(ctx, sqlDB, recovered, !*noHighlightReset); err != nil {
			fatal(err)
		}
		fmt.Println("cleared matches.report; record-dispatcher will regenerate manifest/report and lark-notifier will patch cards after manifest-job notifies match_changed")
	}
	fmt.Printf("summary recovered_rounds=%d segments=%d audio_minutes=%.1f api_seconds=%.3f summed_wall_seconds=%.3f avg_api_seconds_per_segment=%.3f concurrency=%d\n",
		len(recovered), total.Segments, total.AudioSeconds/60, total.APISeconds, total.WallSeconds, avg(total.APISeconds, float64(total.Segments)), max(1, *concurrency))
	fmt.Println("stt recovery finished")
}

func recoverRows(ctx context.Context, c toolConfig, rows []row, force bool, concurrency int) []recoverResult {
	jobs := make(chan row)
	results := make(chan recoverResult)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				if r.HasSuccessfulSTT && !force {
					results <- recoverResult{Row: r, Skipped: true, SkipReason: "stt.jsonl already has SUCCEEDED"}
					continue
				}
				fmt.Printf("recover stt: match=%s round=%d roundNo=%d\n", r.MatchID, r.RoundID, r.RoundNo)
				metric, err := recoverRound(ctx, c, r)
				results <- recoverResult{Row: r, Metric: metric, Err: err}
			}
		}()
	}
	go func() {
		for _, r := range rows {
			jobs <- r
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	out := make([]recoverResult, 0, len(rows))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func queryRows(ctx context.Context, db *stdsql.DB, role string, roundIDs []int, matchIDs []string, limit int) ([]row, error) {
	args := []any{}
	filters := []string{}
	if len(roundIDs) > 0 {
		holders := make([]string, 0, len(roundIDs))
		for _, id := range roundIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "mr.id in ("+strings.Join(holders, ",")+")")
	}
	if len(matchIDs) > 0 {
		holders := make([]string, 0, len(matchIDs))
		for _, id := range matchIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		filters = append(filters, "m.id in ("+strings.Join(holders, ",")+")")
	}
	where := ""
	if len(filters) > 0 {
		where = " and (" + strings.Join(filters, " or ") + ")"
	}
	args = append(args, strings.TrimSpace(role), limit)
	roleArg := len(args) - 1
	limitArg := len(args)
	q := fmt.Sprintf(`
select m.id, mr.id, mr.round_no, ma.path, m.event, m.zone, m.match_type, m."order",
       red.name, red.school_name, blue.name, blue.school_name
from matches m
join match_rounds mr on mr.match_rounds=m.id
join teams red on red.id=m.red_team_id
join teams blue on blue.id=m.blue_team_id
join record_tasks rec on rec.match_round_record_tasks=mr.id
join media_artifacts ma on ma.record_task_media_artifacts=rec.id
where mr.status='ENDED'
  and rec.role=$%d
  and ma.kind='source'
  and ma.status='AVAILABLE'
  %s
order by m.created_at desc, mr.round_no asc
limit $%d`, roleArg, where, limitArg)
	result, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []row{}
	for result.Next() {
		var r row
		if err := result.Scan(&r.MatchID, &r.RoundID, &r.RoundNo, &r.SourcePath, &r.Event, &r.Zone, &r.MatchType, &r.Order, &r.RedName, &r.RedSchool, &r.BlueName, &r.BlueSchool); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, result.Err()
}

func recoverRound(ctx context.Context, c toolConfig, r row) (recoverMetric, error) {
	started := time.Now()
	source := resolveRecordPath(c.RecordConf.BaseDir, r.SourcePath)
	roundDir := filepath.Dir(source)
	audioDir := filepath.Join(roundDir, "audio")
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if err := os.RemoveAll(audioDir); err != nil {
		return recoverMetric{}, err
	}
	if err := os.Remove(sttPath); err != nil && !os.IsNotExist(err) {
		return recoverMetric{}, err
	}
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		return recoverMetric{}, err
	}
	pattern := filepath.Join(audioDir, "part-%05d.wav")
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin", "-y",
		"-i", source,
		"-map", "0:a:0?", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le",
		"-f", "segment", "-segment_time", strconv.Itoa(segmentSeconds),
		"-reset_timestamps", "1", "-segment_format", "wav",
		pattern,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return recoverMetric{}, errors.Wrapf(err, "ffmpeg segment audio: %s", tail(stderr.String(), 2048))
	}
	segments, err := filepath.Glob(filepath.Join(audioDir, "part-*.wav"))
	if err != nil {
		return recoverMetric{}, err
	}
	if len(segments) == 0 {
		if err := appendLines(sttPath, []sttLine{{Index: 0, Start: 0, End: 0, Status: "NO_AUDIO"}}); err != nil {
			return recoverMetric{}, err
		}
		return recoverMetric{WallSeconds: time.Since(started).Seconds()}, os.RemoveAll(audioDir)
	}
	var metric recoverMetric
	prompt := sttPrompt(c.RecordConf, r)
	for i, segment := range segments {
		seconds, duration, err := recognizeSegment(ctx, c.WhisperServerUrl, prompt, segment, sttPath, i)
		metric.Segments++
		metric.APISeconds += seconds
		metric.AudioSeconds += duration
		if err != nil {
			return metric, err
		}
	}
	metric.WallSeconds = time.Since(started).Seconds()
	return metric, os.RemoveAll(audioDir)
}

func recognizeSegment(ctx context.Context, serverURL, prompt, wavPath, sttPath string, index int) (float64, float64, error) {
	start := float64(index * segmentSeconds)
	result, seconds, err := recognizeFile(ctx, serverURL, prompt, wavPath)
	duration := result.Duration
	if duration <= 0 {
		duration = segmentSeconds
	}
	if err != nil {
		return seconds, duration, appendLines(sttPath, []sttLine{{
			Index:        index,
			Start:        start,
			End:          start + segmentSeconds,
			Status:       "FAILED",
			ErrorMessage: err.Error(),
		}})
	}
	if len(result.Segments) == 0 {
		return seconds, duration, appendLines(sttPath, []sttLine{{
			Index:      index,
			Start:      start,
			End:        start + duration,
			Status:     "SUCCEEDED",
			APISeconds: seconds,
			Text:       result.Text,
		}})
	}
	lines := make([]sttLine, 0, len(result.Segments))
	for _, s := range result.Segments {
		lines = append(lines, sttLine{
			Index:      index,
			SegmentID:  s.ID,
			Start:      start + s.Start,
			End:        start + s.End,
			Status:     "SUCCEEDED",
			APISeconds: seconds,
			Text:       s.Text,
		})
	}
	return seconds, duration, appendLines(sttPath, lines)
}

func recognizeFile(ctx context.Context, serverURL, prompt, wavPath string) (whisperResult, float64, error) {
	var out whisperResult
	client := resty.New().
		SetRetryCount(3).
		SetRetryWaitTime(time.Second).
		SetRetryMaxWaitTime(5 * time.Second).
		SetAllowNonIdempotentRetry(true).
		AddRetryConditions(func(resp *resty.Response, err error) bool {
			return err != nil || resp.StatusCode() == 429 || resp.StatusCode() >= 500
		}).
		SetTimeout(180 * time.Second)
	start := time.Now()
	form := map[string]string{
		"temperature":     "0.0",
		"response_format": "verbose_json",
	}
	if strings.TrimSpace(prompt) != "" {
		form["prompt"] = prompt
	}
	resp, err := client.R().
		SetContext(ctx).
		SetFile("file", wavPath).
		SetMultipartFormData(form).
		SetResult(&out).
		Post(strings.TrimRight(serverURL, "/") + "/inference")
	elapsed := time.Since(start).Seconds()
	if err != nil {
		return whisperResult{}, elapsed, err
	}
	if resp.IsError() {
		return whisperResult{}, elapsed, errors.Errorf("whisper server http %d: %s", resp.StatusCode(), resp.String())
	}
	return out, elapsed, nil
}

func runBenchmark(ctx context.Context, c toolConfig, source string) error {
	source = resolveRecordPath(c.RecordConf.BaseDir, source)
	tmp, err := os.MkdirTemp("", "rm-monitor-stt-benchmark-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wav := filepath.Join(tmp, "sample60.wav")
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin", "-y",
		"-i", source, "-t", "60",
		"-map", "0:a:0?", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav,
	}
	if err := exec.CommandContext(ctx, "ffmpeg", args...).Run(); err != nil {
		return errors.Wrap(err, "extract benchmark wav")
	}
	result, seconds, err := recognizeFile(ctx, c.WhisperServerUrl, stttext.GenericPrompt, wav)
	if err != nil {
		return err
	}
	fmt.Printf("elapsed_seconds=%.3f\n", seconds)
	fmt.Printf("text=%s\n", strings.TrimSpace(result.Text))
	return nil
}

func appendLines(path string, lines []sttLine) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, line := range lines {
		b, err := json.Marshal(line)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return f.Sync()
}

func sttPrompt(conf common.RecordConf, r row) string {
	return stttext.BuildPrompt(stttext.PromptData{
		Event:      r.Event,
		Zone:       r.Zone,
		MatchID:    r.MatchID,
		MatchType:  r.MatchType,
		Order:      r.Order,
		RoundNo:    r.RoundNo,
		Role:       conf.STTRole,
		RedSchool:  r.RedSchool,
		RedName:    r.RedName,
		BlueSchool: r.BlueSchool,
		BlueName:   r.BlueName,
	})
}

func markSTTDone(ctx context.Context, c toolConfig, rows []row) error {
	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	defer redisClient.Close()
	for _, r := range rows {
		if err := sttcoord.Set(ctx, redisClient, r.MatchID, r.RoundNo, sttcoord.StatusDone); err != nil {
			return err
		}
	}
	return nil
}

func resetReportsAndHighlights(ctx context.Context, db *stdsql.DB, rows []row, resetHighlights bool) error {
	matchSet := map[string]struct{}{}
	roundIDs := []int{}
	for _, r := range rows {
		matchSet[r.MatchID] = struct{}{}
		roundIDs = append(roundIDs, r.RoundID)
	}
	for matchID := range matchSet {
		if _, err := db.ExecContext(ctx, "update matches set report=null, updated_at=now() where id=$1", matchID); err != nil {
			return err
		}
	}
	if resetHighlights && len(roundIDs) > 0 {
		args := make([]any, 0, len(roundIDs))
		holders := make([]string, 0, len(roundIDs))
		for _, id := range roundIDs {
			args = append(args, id)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
		}
		_, err := db.ExecContext(ctx, "delete from highlight_clips where match_round_highlight_clips in ("+strings.Join(holders, ",")+") and status <> 'SUCCEEDED'", args...)
		return err
	}
	return nil
}

func hasSuccessfulSTT(c toolConfig, sourcePath string) bool {
	sttPath := filepath.Join(filepath.Dir(resolveRecordPath(c.RecordConf.BaseDir, sourcePath)), "stt.jsonl")
	f, err := os.Open(sttPath)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"status":"SUCCEEDED"`) {
			return true
		}
	}
	return false
}

func filterFailedSTT(c toolConfig, rows []row) []row {
	out := make([]row, 0, len(rows))
	for _, r := range rows {
		if r.HasSuccessfulSTT {
			continue
		}
		if !fileExists(filepath.Join(filepath.Dir(resolveRecordPath(c.RecordConf.BaseDir, r.SourcePath)), "stt.jsonl")) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func printRows(rows []row) {
	fmt.Println("candidates:")
	for _, r := range rows {
		fmt.Printf("match=%s round=%d roundNo=%d stt_ok=%t source=%s\n", r.MatchID, r.RoundID, r.RoundNo, r.HasSuccessfulSTT, r.SourcePath)
	}
}

func resolveRecordPath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return storagepath.Resolve(baseDir, path)
}

func parseIntList(raw string) []int {
	parts := parseStringList(raw)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.Atoi(p)
		if err == nil {
			out = append(out, id)
		}
	}
	return out
}

func parseStringList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func avg(sum, count float64) float64 {
	if count <= 0 {
		return 0
	}
	return sum / count
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
