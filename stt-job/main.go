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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"resty.dev/v3"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/pkg/app"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/pkg/sttcoord"
	"scutbot.cn/web/rm-monitor/pkg/stttext"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
	jobconfig "scutbot.cn/web/rm-monitor/stt-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	modeFlag   = flag.String("mode", "", "audio-recorder, recognizer, or backfill-subtitles")
	roundFlag  = flag.Int("round", 0, "match round id")
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
	if *roundFlag == 0 && *modeFlag != modeBackfill {
		logx.Error("round id is required")
		os.Exit(1)
	}
	var c jobconfig.Config
	app.MustLoadConfig(*configFile, &c)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	var runErr error
	switch *modeFlag {
	case modeAudioRecorder:
		runErr = runAudioRecorder(context.Background(), client, c, *roundFlag)
	case modeRecognizer:
		runErr = runRecognizer(context.Background(), client, c, *roundFlag)
	case modeBackfill:
		var summary subtitle.BackfillSummary
		summary, runErr = subtitle.Backfill(c.RecordConf, subtitle.BackfillOptions{Force: true, Rounds: true, Highlights: true})
		if runErr == nil {
			logx.Infof("subtitle backfill completed: round=%d highlight=%d", summary.RoundGenerated, summary.HighlightGenerated)
		}
	default:
		runErr = errors.Errorf("unknown mode %q", *modeFlag)
	}
	if *modeFlag == modeRecognizer {
		status := sttcoord.StatusDone
		if runErr != nil {
			status = sttcoord.StatusFailed
		}
		if err := updateSTTStatus(context.Background(), client, c, *roundFlag, status); err != nil {
			if runErr == nil {
				runErr = err
			} else {
				logx.Errorf("update stt status failed: %v", err)
			}
		}
	}
	if runErr != nil {
		logx.Error(runErr)
		os.Exit(1)
	}
}

func updateSTTStatus(ctx context.Context, client *ent.Client, c jobconfig.Config, roundID int, status string) error {
	round, err := client.MatchRound.Query().
		Where(matchround.ID(roundID)).
		WithMatch().
		Only(ctx)
	if err != nil {
		return errors.Wrap(err, "load round for stt status")
	}
	m, err := round.Edges.MatchOrErr()
	if err != nil {
		return err
	}
	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	defer redisClient.Close()
	return sttcoord.Set(ctx, redisClient, m.ID, round.RoundNo, status)
}

func runAudioRecorder(ctx context.Context, client *ent.Client, c jobconfig.Config, roundID int) error {
	conf := c.RecordConf.WithDefaults()
	info, err := loadRoundInfo(ctx, client, conf, roundID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(info.AudioDir); err != nil {
		return errors.Wrap(err, "clean audio dir")
	}
	if err := os.Remove(info.STTPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "clean old stt jsonl")
	}
	if err := os.MkdirAll(info.AudioDir, 0o755); err != nil {
		return errors.Wrap(err, "create audio dir")
	}
	sourceURL := strings.TrimSpace(os.Getenv("STT_SOURCE_URL"))
	if sourceURL == "" {
		err := errors.New("STT_SOURCE_URL is empty")
		writeMarker(info.AudioDir, ".ffmpeg.failed", err.Error())
		return err
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopRequested atomic.Bool
	go watchRoundEnded(jobCtx, client, roundID, &stopRequested, cancel)

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
	logx.Infof("recording stt audio for round %d role %s", roundID, conf.STTRole)
	err = cmd.Run()
	stderrText := stderr.String()
	if isNoAudio(stderrText) {
		writeMarker(info.AudioDir, ".ffmpeg.no_audio", stderrText)
		writeMarker(info.AudioDir, ".ffmpeg.done", "no audio")
		return nil
	}
	if err != nil && !stopRequested.Load() {
		msg := commandError(err, stderrText)
		writeMarker(info.AudioDir, ".ffmpeg.failed", msg)
		return errors.New(msg)
	}
	if !stopRequested.Load() {
		latest, latestErr := loadRound(ctx, client, roundID)
		if latestErr != nil {
			writeMarker(info.AudioDir, ".ffmpeg.failed", latestErr.Error())
			return latestErr
		}
		if latest.Status == matchround.StatusSTARTED {
			msg := "ffmpeg exited before match round ended"
			writeMarker(info.AudioDir, ".ffmpeg.failed", msg)
			return errors.New(msg)
		}
	}
	writeMarker(info.AudioDir, ".ffmpeg.done", "done")
	return nil
}

func runRecognizer(ctx context.Context, client *ent.Client, c jobconfig.Config, roundID int) error {
	conf := c.RecordConf.WithDefaults()
	info, err := loadRoundInfo(ctx, client, conf, roundID)
	if err != nil {
		return err
	}
	serverURL := strings.TrimSpace(c.WhisperServerUrl)
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
			return os.RemoveAll(info.AudioDir)
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
			if err := writeRoundSubtitle(info); err != nil {
				return err
			}
			return os.RemoveAll(info.AudioDir)
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
	Round        *ent.MatchRound
	Match        *ent.Match
	RoundDir     string
	AudioDir     string
	STTPath      string
	SubtitleName string
	Prompt       string
}

func loadRoundInfo(ctx context.Context, client *ent.Client, conf common.RecordConf, roundID int) (roundInfo, error) {
	round, err := client.MatchRound.Query().
		Where(matchround.ID(roundID)).
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().WithBlueTeam()
		}).
		Only(ctx)
	if err != nil {
		return roundInfo{}, errors.Wrap(err, "load match round")
	}
	match, err := round.Edges.MatchOrErr()
	if err != nil {
		return roundInfo{}, err
	}
	red, err := match.Edges.RedTeamOrErr()
	if err != nil {
		return roundInfo{}, err
	}
	blue, err := match.Edges.BlueTeamOrErr()
	if err != nil {
		return roundInfo{}, err
	}
	rel, err := pathfmt.RenderMatchDir(conf.MatchNameTemplate, conf.MatchDirTemplate, pathfmt.Data{
		Event:      match.Event,
		Zone:       match.Zone,
		Order:      match.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
		RoundNo:    round.RoundNo,
		Role:       conf.STTRole,
	})
	if err != nil {
		return roundInfo{}, err
	}
	roundDir := storagepath.Resolve(conf.BaseDir, filepath.Join(rel, fmt.Sprintf("Round-%d", round.RoundNo)))
	return roundInfo{
		Round:        round,
		Match:        match,
		RoundDir:     roundDir,
		AudioDir:     filepath.Join(roundDir, "audio"),
		STTPath:      filepath.Join(roundDir, "stt.jsonl"),
		SubtitleName: fmt.Sprintf("%s.srt", conf.STTRole),
		Prompt: stttext.BuildPrompt(stttext.PromptData{
			Event:      match.Event,
			Zone:       match.Zone,
			MatchID:    match.ID,
			MatchType:  match.MatchType,
			Order:      match.Order,
			RoundNo:    round.RoundNo,
			Role:       conf.STTRole,
			RedSchool:  red.SchoolName,
			RedName:    red.Name,
			BlueSchool: blue.SchoolName,
			BlueName:   blue.Name,
		}),
	}, nil
}

func loadRound(ctx context.Context, client *ent.Client, roundID int) (*ent.MatchRound, error) {
	return client.MatchRound.Get(ctx, roundID)
}

func watchRoundEnded(ctx context.Context, client *ent.Client, roundID int, stopRequested *atomic.Bool, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			round, err := client.MatchRound.Get(ctx, roundID)
			if err == nil && round.Status == matchround.StatusENDED {
				stopRequested.Store(true)
				cancel()
				return
			}
		}
	}
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
