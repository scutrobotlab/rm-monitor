package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"resty.dev/v3"

	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/difyworkflow"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/stttext"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
	jobconfig "scutbot.cn/web/rm-monitor/stt-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	modeFlag   = flag.String("mode", "", "empty for stt or backfill-subtitles")

	simplifierOnce sync.Once
	simplifier     *stttext.Converter
	simplifierErr  error
)

const modeBackfill = "backfill-subtitles"

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "stt-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c jobconfig.Config
	app.MustLoadConfig(*configFile, &c)

	if *modeFlag == modeBackfill {
		runBackfill(c.RecordConf)
		return
	}
	if strings.TrimSpace(*modeFlag) != "" {
		logx.Error(errors.Errorf("unknown mode %q", *modeFlag))
		os.Exit(1)
	}

	var sttCtx jobcontract.STTContext
	if err := jobcontract.ContextFromEnv(&sttCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	sttCtx.WhisperServerURLs = resolveWhisperServerURLs(sttCtx.WhisperServerURLs, c.WhisperServerUrls)
	if len(sttCtx.WhisperServerURLs) == 0 {
		logx.Error(errors.New("WhisperServerURLs is empty"))
		os.Exit(1)
	}
	jobDir := sttJobDir(sttCtx)
	if err := jobcontract.WriteContext(jobDir, sttCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if err := runSTT(context.Background(), sttCtx, c); err != nil {
		_ = jobcontract.WriteError(jobDir, "stt", sttCtx.MatchRoundID, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func runBackfill(conf common.RecordConf) {
	summary, err := subtitle.Backfill(conf, subtitle.BackfillOptions{Force: true, Rounds: true, Highlights: true})
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	logx.Infof("subtitle backfill completed: round=%d highlight=%d", summary.RoundGenerated, summary.HighlightGenerated)
}

func runSTT(ctx context.Context, sttCtx jobcontract.STTContext, conf jobconfig.Config) error {
	info := roundInfoFromContext(sttCtx)
	if strings.TrimSpace(sttCtx.SourcePath) == "" {
		return errors.New("stt source_path is empty")
	}
	if err := os.Remove(info.STTPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old stt jsonl")
	}
	if err := os.Remove(info.RawSTTPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old raw stt jsonl")
	}
	if err := os.Remove(filepath.Join(info.RoundDir, info.SubtitleName)); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old subtitle")
	}
	tmpDir, err := os.MkdirTemp("", "rm-monitor-stt-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	audioPath := filepath.Join(tmpDir, "audio.m4a")
	if err := extractAudio(ctx, sttCtx.SourcePath, audioPath); err != nil {
		if isNoAudio(err.Error()) {
			if err := appendLine(info.STTPath, sttLine{Index: 0, Start: 0, End: 0, Status: "NO_AUDIO"}); err != nil {
				return err
			}
			return finishSTT(sttCtx, info)
		}
		return err
	}
	result, seconds, err := recognizeFile(ctx, sttCtx.WhisperServerURLs, sttCtx.Prompt, audioPath)
	if err != nil {
		return err
	}
	if err := writeRecognizedLines(info.STTPath, result, seconds); err != nil {
		return err
	}
	if conf.STTQualityConf.UseQuality {
		if err := qualityCleanSTT(ctx, sttCtx, info, conf); err != nil {
			return err
		}
	}
	return finishSTT(sttCtx, info)
}

func extractAudio(ctx context.Context, sourcePath, audioPath string) error {
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-i", sourcePath,
		"-vn",
		"-map", "0:a:0",
		"-c:a", "copy",
		"-y", audioPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return errors.New(commandError(err, stderr.String()))
	}
	return nil
}

func writeRoundSubtitle(info roundInfo) error {
	err := subtitle.WriteSRTFromJSONL(info.STTPath, filepath.Join(info.RoundDir, info.SubtitleName), subtitle.Options{})
	if errors.Is(err, subtitle.ErrNoCues) {
		return nil
	}
	return err
}

type roundInfo struct {
	RoundDir     string
	STTPath      string
	RawSTTPath   string
	SubtitleName string
	Prompt       string
}

func roundInfoFromContext(sttCtx jobcontract.STTContext) roundInfo {
	rawPath := filepath.Join(sttCtx.RoundDir, "stt.raw.jsonl")
	return roundInfo{
		RoundDir:     sttCtx.RoundDir,
		STTPath:      sttCtx.STTPath,
		RawSTTPath:   rawPath,
		SubtitleName: sttCtx.SubtitleName,
		Prompt:       sttCtx.Prompt,
	}
}

func finishSTT(sttCtx jobcontract.STTContext, info roundInfo) error {
	if err := writeRoundSubtitle(info); err != nil {
		return err
	}
	return jobcontract.WriteResult(sttJobDir(sttCtx), jobcontract.STTResult{
		Schema:       "rm-monitor/stt-result/v1",
		STTTaskID:    sttCtx.STTTaskID,
		MatchRoundID: sttCtx.MatchRoundID,
		STTPath:      sttCtx.STTPath,
		SubtitlePath: filepath.Join(info.RoundDir, info.SubtitleName),
		CompletedAt:  time.Now(),
	})
}

func sttJobDir(sttCtx jobcontract.STTContext) string {
	id := sttCtx.STTTaskID
	if id == 0 {
		id = sttCtx.MatchRoundID
	}
	return filepath.Join(sttCtx.RoundDir, jobcontract.DirName, fmt.Sprintf("stt-%d", id))
}

func isNoAudio(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "output file does not contain any stream") ||
		strings.Contains(lower, "stream map '0:a:0' matches no streams") ||
		strings.Contains(lower, "matches no streams")
}

func commandError(err error, stderr string) string {
	const max = 2048
	msg := err.Error()
	if stderr != "" {
		if len(stderr) > max {
			stderr = stderr[len(stderr)-max:]
		}
		msg = fmt.Sprintf("%s: %s", msg, stderr)
	}
	return msg
}

type sttLine struct {
	Index        int     `json:"index"`
	SegmentID    int     `json:"segment_id,omitempty"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Status       string  `json:"status"`
	APISeconds   float64 `json:"api_seconds,omitempty"`
	Text         string  `json:"text,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

func writeRecognizedLines(path string, result whisperResult, seconds float64) error {
	if len(result.Segments) == 0 {
		text, err := simplifyText(result.Text)
		if err != nil {
			return err
		}
		duration := result.Duration
		if duration <= 0 {
			duration = 0
		}
		return appendLine(path, sttLine{Index: 0, Start: 0, End: duration, Status: "SUCCEEDED", APISeconds: seconds, Text: text})
	}
	lines := make([]sttLine, 0, len(result.Segments))
	for i, segment := range result.Segments {
		text, err := simplifyText(segment.Text)
		if err != nil {
			return err
		}
		lines = append(lines, sttLine{
			Index:      i,
			SegmentID:  segment.ID,
			Start:      segment.Start,
			End:        segment.End,
			Status:     "SUCCEEDED",
			APISeconds: seconds,
			Text:       text,
		})
	}
	return appendLines(path, lines)
}

type sttQualityPayload struct {
	Schema       string    `json:"schema"`
	MatchID      string    `json:"match_id"`
	MatchRoundID int       `json:"match_round_id"`
	RoundNo      int       `json:"round_no"`
	Role         string    `json:"role"`
	Prompt       string    `json:"prompt,omitempty"`
	Lines        []sttLine `json:"lines"`
}

type sttQualityOutput struct {
	Usable       bool                `json:"usable"`
	QualityScore float64             `json:"quality_score,omitempty"`
	Issues       []string            `json:"issues,omitempty"`
	Summary      string              `json:"summary,omitempty"`
	Segments     []sttQualitySegment `json:"segments"`
}

type sttQualitySegment struct {
	Index        int      `json:"index"`
	SegmentID    int      `json:"segment_id,omitempty"`
	Start        *float64 `json:"start,omitempty"`
	End          *float64 `json:"end,omitempty"`
	Status       string   `json:"status,omitempty"`
	Text         string   `json:"text,omitempty"`
	Keep         *bool    `json:"keep,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	ErrorMessage string   `json:"error_message,omitempty"`
}

func qualityCleanSTT(ctx context.Context, sttCtx jobcontract.STTContext, info roundInfo, conf jobconfig.Config) error {
	rawLines, err := readSTTLines(info.STTPath)
	if err != nil {
		return err
	}
	if len(rawLines) == 0 {
		return errors.New("stt quality input is empty")
	}
	client, err := difyworkflow.New(conf.DifyConf)
	if err != nil {
		return errors.Wrap(err, "init dify stt quality client")
	}
	payload := sttQualityPayload{
		Schema:       "rm-monitor/dify-stt-quality-input/v1",
		MatchID:      sttCtx.MatchID,
		MatchRoundID: sttCtx.MatchRoundID,
		RoundNo:      sttCtx.RoundNo,
		Role:         sttCtx.Role,
		Prompt:       sttCtx.Prompt,
		Lines:        rawLines,
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := client.RunWorkflow(ctx, conf.STTQualityConf.WorkflowAPIKey, fmt.Sprintf("rm-monitor:stt:%d", sttCtx.MatchRoundID), map[string]any{
		"payload": string(payloadRaw),
	})
	if err != nil {
		return errors.Wrap(err, "run dify stt quality workflow")
	}
	cleanedRaw, err := difyworkflow.RawOutput(result.Outputs, "cleaned_json")
	if err != nil {
		return err
	}
	var cleaned sttQualityOutput
	if err := json.Unmarshal(cleanedRaw, &cleaned); err != nil {
		return errors.Wrap(err, "decode dify stt quality output")
	}
	lines, err := qualityOutputToLines(rawLines, cleaned)
	if err != nil {
		return err
	}
	cleanTmp := info.STTPath + ".clean.tmp"
	if err := os.Remove(cleanTmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := appendLines(cleanTmp, lines); err != nil {
		return errors.Wrap(err, "write cleaned stt jsonl")
	}
	if err := os.Rename(info.STTPath, info.RawSTTPath); err != nil {
		return errors.Wrap(err, "rename raw stt jsonl")
	}
	if err := os.Rename(cleanTmp, info.STTPath); err != nil {
		return errors.Wrap(err, "commit cleaned stt jsonl")
	}
	return writeQualityArtifact(info.RoundDir, cleanedRaw)
}

func readSTTLines(path string) ([]sttLine, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
	out := make([]sttLine, 0, len(rows))
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		var line sttLine
		if err := json.Unmarshal([]byte(row), &line); err != nil {
			return nil, errors.Wrap(err, "decode stt jsonl")
		}
		out = append(out, line)
	}
	return out, nil
}

func qualityOutputToLines(rawLines []sttLine, cleaned sttQualityOutput) ([]sttLine, error) {
	byIndex := make(map[int]sttLine, len(rawLines))
	for _, line := range rawLines {
		byIndex[line.Index] = line
	}
	out := make([]sttLine, 0, len(cleaned.Segments))
	for _, segment := range cleaned.Segments {
		keep := true
		if segment.Keep != nil {
			keep = *segment.Keep
		}
		if !keep {
			continue
		}
		base, ok := byIndex[segment.Index]
		if !ok {
			base = sttLine{Index: segment.Index, SegmentID: segment.SegmentID, Status: "SUCCEEDED"}
		}
		if segment.Start != nil {
			base.Start = *segment.Start
		}
		if segment.End != nil {
			base.End = *segment.End
		}
		if segment.SegmentID != 0 {
			base.SegmentID = segment.SegmentID
		}
		if strings.TrimSpace(segment.Status) != "" {
			base.Status = strings.TrimSpace(segment.Status)
		} else if base.Status == "" {
			base.Status = "SUCCEEDED"
		}
		text, err := simplifyText(segment.Text)
		if err != nil {
			return nil, err
		}
		base.Text = strings.TrimSpace(text)
		base.ErrorMessage = strings.TrimSpace(segment.ErrorMessage)
		if base.Status == "SUCCEEDED" && base.Text == "" {
			continue
		}
		out = append(out, base)
	}
	if len(out) == 0 {
		out = append(out, sttLine{Index: 0, Start: 0, End: 0, Status: "NO_USABLE_STT", ErrorMessage: strings.Join(cleaned.Issues, ",")})
	}
	return out, nil
}

func writeQualityArtifact(roundDir string, raw json.RawMessage) error {
	path := filepath.Join(roundDir, "stt.quality.json")
	tmp := path + ".tmp"
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		pretty.Write(raw)
	}
	if err := os.WriteFile(tmp, pretty.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func simplifyText(text string) (string, error) {
	simplifierOnce.Do(func() {
		simplifier, simplifierErr = stttext.NewSimplifier()
	})
	if simplifierErr != nil {
		return "", errors.Wrap(simplifierErr, "init stt t2s converter")
	}
	out, err := simplifier.Simplify(text)
	if err != nil {
		return "", errors.Wrap(err, "simplify stt text")
	}
	return out, nil
}

func appendLine(path string, line sttLine) error {
	return appendLines(path, []sttLine{line})
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

type whisperResult struct {
	Task     string           `json:"task"`
	Language string           `json:"language"`
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

func recognizeFile(ctx context.Context, serverURLs []string, prompt, audioPath string) (whisperResult, float64, error) {
	if len(serverURLs) == 0 {
		return whisperResult{}, 0, errors.New("empty WhisperServerURLs")
	}
	serverURLs = dedupeWhisperServerURLs(serverURLs)
	var lastErr error
	for _, serverURL := range serverURLs {
		out, seconds, err := recognizeFileOnce(ctx, serverURL, prompt, audioPath)
		if err == nil {
			return out, seconds, nil
		}
		lastErr = err
		logx.Infof("whisper server %s failed: %v", serverURL, err)
	}
	return whisperResult{}, 0, errors.Wrap(lastErr, "all whisper servers failed")
}

func recognizeFileOnce(ctx context.Context, serverURL, prompt, audioPath string) (whisperResult, float64, error) {
	var out whisperResult
	client := resty.New().
		SetRetryCount(3).
		SetRetryWaitTime(time.Second).
		SetRetryMaxWaitTime(5 * time.Second).
		SetAllowNonIdempotentRetry(true).
		AddRetryConditions(func(resp *resty.Response, err error) bool {
			return err != nil || resp.StatusCode() == 429 || resp.StatusCode() >= 500
		}).
		SetTimeout(30 * time.Minute)
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
		SetFile("file", audioPath).
		SetMultipartFormData(form).
		SetResult(&out).
		Post(serverURL)
	seconds := time.Since(start).Seconds()
	if err != nil {
		return whisperResult{}, seconds, err
	}
	if resp.IsError() {
		return whisperResult{}, seconds, errors.Errorf("whisper server http %d: %s", resp.StatusCode(), resp.String())
	}
	return out, seconds, nil
}

func resolveWhisperServerURLs(urlLists ...[]string) []string {
	var urls []string
	for _, list := range urlLists {
		urls = append(urls, list...)
	}
	return dedupeWhisperServerURLs(urls)
}

func dedupeWhisperServerURLs(urls ...[]string) []string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, list := range urls {
		for _, url := range list {
			trimmed := strings.TrimSpace(url)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}
