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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"resty.dev/v3"

	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
	jobconfig "scutbot.cn/web/rm-monitor/stt-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	modeFlag   = flag.String("mode", "", "audio-recorder, recognizer, or backfill-subtitles")
)

const (
	modeAudioRecorder = "audio-recorder"
	modeRecognizer    = "recognizer"
	modeBackfill      = "backfill-subtitles"
	segmentSeconds    = 60
	ua                = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "stt-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c jobconfig.Config
	app.MustLoadConfig(*configFile, &c)
	var sttCtx jobcontract.STTContext
	if *modeFlag != modeBackfill {
		if err := jobcontract.ContextFromEnv(&sttCtx); err != nil {
			logx.Error(err)
			os.Exit(1)
		}
		if sttCtx.WhisperServerURL == "" {
			sttCtx.WhisperServerURL = c.WhisperServerUrl
		}
		jobDir := sttJobDir(sttCtx)
		if err := jobcontract.WriteContext(jobDir, sttCtx); err != nil {
			logx.Error(err)
			os.Exit(1)
		}
	}
	var runErr error
	switch *modeFlag {
	case modeAudioRecorder:
		runErr = runAudioRecorder(context.Background(), sttCtx)
	case modeRecognizer:
		runErr = runRecognizer(context.Background(), sttCtx)
	case modeBackfill:
		var summary subtitle.BackfillSummary
		summary, runErr = subtitle.Backfill(c.RecordConf, subtitle.BackfillOptions{Force: true, Rounds: true, Highlights: true})
		if runErr == nil {
			logx.Infof("subtitle backfill completed: round=%d highlight=%d", summary.RoundGenerated, summary.HighlightGenerated)
		}
	default:
		runErr = errors.Errorf("unknown mode %q", *modeFlag)
	}
	if runErr != nil {
		if *modeFlag != modeBackfill {
			_ = jobcontract.WriteError(sttJobDir(sttCtx), "stt", sttCtx.MatchRoundID, runErr)
		}
		logx.Error(runErr)
		os.Exit(1)
	}
}

func runAudioRecorder(ctx context.Context, sttCtx jobcontract.STTContext) error {
	info := roundInfoFromContext(sttCtx)
	if err := os.RemoveAll(info.AudioDir); err != nil {
		return errors.Wrap(err, "clean audio dir")
	}
	if err := os.Remove(info.STTPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old stt jsonl")
	}
	if err := os.MkdirAll(info.AudioDir, 0o755); err != nil {
		return errors.Wrap(err, "create audio dir")
	}
	sourceURL := strings.TrimSpace(sttCtx.SourceURL)
	if sourceURL == "" {
		err := errors.New("stt source_url is empty")
		writeMarker(info.AudioDir, ".ffmpeg.failed", err.Error())
		return err
	}

	jobCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	pattern := filepath.Join(info.AudioDir, "part-%05d.wav")
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-nostdin",
		"-stats_period", "10",
	}
	if isNetworkSource(sourceURL) {
		args = append(args,
			"-user_agent", ua,
			"-rw_timeout", "15000000",
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_on_network_error", "1",
			"-reconnect_on_http_error", "429,500,502,503,504",
			"-reconnect_delay_max", "5",
		)
	}
	args = append(args,
		"-i", sourceURL,
		"-map", "0:a:0?",
		"-vn",
		"-sn",
		"-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-f", "segment",
		"-segment_time", strconv.Itoa(segmentSeconds),
		"-reset_timestamps", "1",
		"-segment_format", "wav",
		"-y", pattern,
	)
	cmd := exec.CommandContext(jobCtx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 10 * time.Second
	logx.Infof("recording stt audio for round %d role %s", sttCtx.MatchRoundID, sttCtx.Role)
	err := cmd.Run()
	stderrText := stderr.String()
	if isNoAudio(stderrText) {
		writeMarker(info.AudioDir, ".ffmpeg.no_audio", stderrText)
		writeMarker(info.AudioDir, ".ffmpeg.done", "no audio")
		return nil
	}
	stopRequested := jobCtx.Err() != nil
	if err != nil && !stopRequested {
		msg := commandError(err, stderrText)
		writeMarker(info.AudioDir, ".ffmpeg.failed", msg)
		return errors.New(msg)
	}
	if !stopRequested {
		msg := "ffmpeg exited before dispatcher requested stop"
		writeMarker(info.AudioDir, ".ffmpeg.failed", msg)
		return errors.New(msg)
	}
	writeMarker(info.AudioDir, ".ffmpeg.done", "done")
	return nil
}

func runRecognizer(ctx context.Context, sttCtx jobcontract.STTContext) error {
	info := roundInfoFromContext(sttCtx)
	serverURL := strings.TrimSpace(sttCtx.WhisperServerURL)
	if serverURL == "" {
		return errors.New("WhisperServerUrl is empty")
	}
	if err := waitForDir(ctx, info.AudioDir); err != nil {
		return err
	}
	for index := 0; ; {
		if markerExists(info.AudioDir, ".ffmpeg.no_audio") {
			if index == 0 {
				if err := appendLine(info.STTPath, sttLine{Index: 0, Start: 0, End: 0, Status: "NO_AUDIO"}); err != nil {
					return err
				}
			}
			return finishSTT(sttCtx, info)
		}
		current := segmentPath(info.AudioDir, index)
		if fileExists(current) {
			if segmentComplete(info.AudioDir, index) {
				if err := recognizeSegment(ctx, serverURL, info.Prompt, current, info.STTPath, index); err != nil {
					return err
				}
				index++
				continue
			}
		} else if markerExists(info.AudioDir, ".ffmpeg.done") {
			return finishSTT(sttCtx, info)
		} else if markerExists(info.AudioDir, ".ffmpeg.failed") {
			return errors.New(readMarker(info.AudioDir, ".ffmpeg.failed"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
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
	AudioDir     string
	STTPath      string
	SubtitleName string
	Prompt       string
}

func roundInfoFromContext(sttCtx jobcontract.STTContext) roundInfo {
	return roundInfo{
		RoundDir:     sttCtx.RoundDir,
		AudioDir:     sttCtx.AudioDir,
		STTPath:      sttCtx.STTPath,
		SubtitleName: sttCtx.SubtitleName,
		Prompt:       sttCtx.Prompt,
	}
}

func finishSTT(sttCtx jobcontract.STTContext, info roundInfo) error {
	if err := writeRoundSubtitle(info); err != nil {
		return err
	}
	if err := os.RemoveAll(info.AudioDir); err != nil {
		return errors.Wrap(err, "clean audio dir")
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

func isNetworkSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
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

func writeMarker(dir, name, content string) {
	_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func readMarker(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func markerExists(dir, name string) bool {
	return fileExists(filepath.Join(dir, name))
}

func waitForDir(ctx context.Context, dir string) error {
	for {
		if stat, err := os.Stat(dir); err == nil && stat.IsDir() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func segmentPath(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("part-%05d.wav", index))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func segmentComplete(dir string, index int) bool {
	if fileExists(segmentPath(dir, index+1)) || markerExists(dir, ".ffmpeg.done") || markerExists(dir, ".ffmpeg.failed") {
		return stableFile(segmentPath(dir, index))
	}
	return false
}

func stableFile(path string) bool {
	first, err := os.Stat(path)
	if err != nil || first.IsDir() {
		return false
	}
	time.Sleep(time.Second)
	second, err := os.Stat(path)
	return err == nil && !second.IsDir() && first.Size() == second.Size()
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

func recognizeSegment(ctx context.Context, serverURL, prompt, wavPath, sttPath string, index int) error {
	start := float64(index * segmentSeconds)
	end := start + float64(segmentSeconds)
	stat, err := os.Stat(wavPath)
	if err != nil {
		return errors.Wrap(err, "stat segment")
	}
	if stat.Size() == 0 {
		return appendLine(sttPath, sttLine{Index: index, Start: start, End: start, Status: "EMPTY"})
	}
	result, seconds, err := recognizeFile(ctx, serverURL, prompt, wavPath)
	if err == nil {
		return appendRecognizedLine(sttPath, index, 0, start, result, seconds)
	}
	return appendLine(sttPath, sttLine{Index: index, Start: start, End: end, Status: "FAILED", ErrorMessage: err.Error()})
}

func appendRecognizedLine(path string, index, part int, start float64, result whisperResult, seconds float64) error {
	duration := result.Duration
	if duration <= 0 {
		duration = segmentSeconds
		if part != 0 {
			duration = float64(segmentSeconds) / 2
		}
	}
	if len(result.Segments) == 0 {
		return appendLine(path, sttLine{
			Index:      index,
			Part:       part,
			Start:      start,
			End:        start + duration,
			Status:     "SUCCEEDED",
			APISeconds: seconds,
			Text:       result.Text,
		})
	}
	lines := make([]sttLine, 0, len(result.Segments))
	for _, segment := range result.Segments {
		lines = append(lines, sttLine{
			Index:      index,
			Part:       part,
			SegmentID:  segment.ID,
			Start:      start + segment.Start,
			End:        start + segment.End,
			Status:     "SUCCEEDED",
			APISeconds: seconds,
			Text:       segment.Text,
		})
	}
	return appendLines(path, lines)
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
	seconds := time.Since(start).Seconds()
	if err != nil {
		return whisperResult{}, seconds, err
	}
	if resp.IsError() {
		return whisperResult{}, seconds, errors.Errorf("whisper server http %d: %s", resp.StatusCode(), resp.String())
	}
	return out, seconds, nil
}
