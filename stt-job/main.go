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
	if err := runSTT(context.Background(), sttCtx); err != nil {
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

func runSTT(ctx context.Context, sttCtx jobcontract.STTContext) error {
	info := roundInfoFromContext(sttCtx)
	if strings.TrimSpace(sttCtx.SourcePath) == "" {
		return errors.New("stt source_path is empty")
	}
	if err := os.Remove(info.STTPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old stt jsonl")
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
	SubtitleName string
	Prompt       string
}

func roundInfoFromContext(sttCtx jobcontract.STTContext) roundInfo {
	return roundInfo{
		RoundDir:     sttCtx.RoundDir,
		STTPath:      sttCtx.STTPath,
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
		MatchRoundID: sttCtx.MatchRoundID,
		STTPath:      sttCtx.STTPath,
		CompletedAt:  time.Now(),
	})
}

func sttJobDir(sttCtx jobcontract.STTContext) string {
	return filepath.Join(sttCtx.RoundDir, jobcontract.DirName, fmt.Sprintf("stt-%d", sttCtx.MatchRoundID))
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
