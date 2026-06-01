package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/record-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
)

const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const recordMetaFile = "record-meta.json"

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "record-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	var jobCtx jobcontract.RecordContext
	if err := jobcontract.ContextFromEnv(&jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if jobCtx.BaseDir == "" {
		jobCtx.BaseDir = c.RecordConf.WithDefaults().BaseDir
	}
	jobDir := recordJobDir(jobCtx.BaseDir, jobCtx.OutputPath, jobCtx.MatchRoundID, jobCtx.Role)
	if err := jobcontract.WriteContext(jobDir, jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if err := run(context.Background(), c, jobCtx, jobDir); err != nil {
		_ = jobcontract.WriteError(jobDir, "record", 0, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c config.Config, jobCtx jobcontract.RecordContext, jobDir string) error {
	if jobCtx.MatchRoundID == 0 {
		return errors.New("match_round_id is required")
	}
	client, err := db.Open(ctx, c.PostgresConf)
	if err != nil {
		return errors.Wrap(err, "open postgres")
	}
	defer client.Close()

	fullPath := storagepath.Resolve(jobCtx.BaseDir, jobCtx.OutputPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return errors.Wrap(err, "create output dir")
	}
	partPath := fullPath + ".part"
	_ = os.Remove(partPath)

	signalCtx, stopSignal := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stopSignal()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopRequested atomic.Bool
	stopDelay := time.Duration(c.RecordConf.WithDefaults().StopDelaySeconds) * time.Second
	go func() {
		<-signalCtx.Done()
		cancel()
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				r, err := client.MatchRound.Get(ctx, jobCtx.MatchRoundID)
				if err != nil {
					logx.Errorf("poll round status match_round_id=%d err=%v", jobCtx.MatchRoundID, err)
					continue
				}
				if r.Status == matchround.StatusENDED {
					stopRequested.Store(true)
					if stopDelay > 0 {
						timer := time.NewTimer(stopDelay)
						select {
						case <-runCtx.Done():
							timer.Stop()
							return
						case <-timer.C:
						}
					}
					cancel()
					return
				}
			}
		}
	}()

	recordStartedAt := time.Now()
	if jobCtx.KeepAudio {
		if err := writeRecordMeta(filepath.Dir(fullPath), recordMeta{
			Schema:                "rm-monitor/record-meta/v1",
			MatchRoundID:          jobCtx.MatchRoundID,
			Role:                  jobCtx.Role,
			SourceURL:             jobCtx.SourceURL,
			OutputPath:            jobCtx.OutputPath,
			RecordWallStartedAt:   recordStartedAt,
			MediaTimeZeroWallAt:   recordStartedAt,
			RecordWallCompletedAt: nil,
			FileSize:              0,
			Checksum:              "",
		}); err != nil {
			return errors.Wrap(err, "write initial record metadata")
		}
	}

	args := recordFFmpegArgs(jobCtx.SourceURL, partPath, jobCtx.KeepAudio)
	cmd := exec.CommandContext(runCtx, "ffmpeg", args...)
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
	logx.Infof("recording %s to %s", jobCtx.SourceURL, path.Clean(jobCtx.OutputPath))
	err = cmd.Run()
	if err != nil && !stopRequested.Load() {
		if jobCtx.KeepAudio {
			_ = removeRecordMeta(filepath.Dir(fullPath))
		}
		msg := commandError(err, stderr.String())
		return errors.New(msg)
	}
	if !stopRequested.Load() {
		if jobCtx.KeepAudio {
			_ = removeRecordMeta(filepath.Dir(fullPath))
		}
		return errors.New("ffmpeg exited before round stop was requested")
	}

	if err := os.Rename(partPath, fullPath); err != nil {
		if jobCtx.KeepAudio {
			_ = removeRecordMeta(filepath.Dir(fullPath))
		}
		return errors.Wrap(err, "commit output")
	}
	stat, statErr := os.Stat(fullPath)
	if statErr != nil {
		if jobCtx.KeepAudio {
			_ = removeRecordMeta(filepath.Dir(fullPath))
		}
		return errors.Wrap(statErr, "stat output")
	}
	sum, err := checksum(fullPath)
	if err != nil {
		if jobCtx.KeepAudio {
			_ = removeRecordMeta(filepath.Dir(fullPath))
		}
		return err
	}
	completedAt := time.Now()
	if jobCtx.KeepAudio {
		if err := writeRecordMeta(filepath.Dir(fullPath), recordMeta{
			Schema:                "rm-monitor/record-meta/v1",
			MatchRoundID:          jobCtx.MatchRoundID,
			Role:                  jobCtx.Role,
			SourceURL:             jobCtx.SourceURL,
			OutputPath:            jobCtx.OutputPath,
			RecordWallStartedAt:   recordStartedAt,
			RecordWallCompletedAt: &completedAt,
			MediaTimeZeroWallAt:   recordStartedAt,
			FileSize:              stat.Size(),
			Checksum:              sum,
		}); err != nil {
			_ = removeRecordMeta(filepath.Dir(fullPath))
			return errors.Wrap(err, "write final record metadata")
		}
	}
	result := jobcontract.RecordResult{
		Schema:       "rm-monitor/record-result/v1",
		MatchID:      jobCtx.MatchID,
		MatchRoundID: jobCtx.MatchRoundID,
		OutputPath:   jobCtx.OutputPath,
		Format:       "flv",
		Codec:        "copy",
		FileSize:     stat.Size(),
		Checksum:     sum,
		CompletedAt:  completedAt,
	}
	if err := jobcontract.WriteTempResult(result); err != nil {
		return err
	}
	if err := jobcontract.WriteArgoOutputs(map[string]any{
		"output_path": jobCtx.OutputPath,
		"role":        jobCtx.Role,
		"format":      result.Format,
		"codec":       result.Codec,
		"file_size":   result.FileSize,
		"checksum":    result.Checksum,
	}); err != nil {
		return err
	}
	return nil
}

type recordMeta struct {
	Schema                string     `json:"schema"`
	MatchRoundID          int        `json:"match_round_id"`
	Role                  string     `json:"role"`
	SourceURL             string     `json:"source_url"`
	OutputPath            string     `json:"output_path"`
	RecordWallStartedAt   time.Time  `json:"record_wall_started_at"`
	RecordWallCompletedAt *time.Time `json:"record_wall_completed_at"`
	MediaTimeZeroWallAt   time.Time  `json:"media_time_zero_wall_at"`
	FileSize              int64      `json:"file_size"`
	Checksum              string     `json:"checksum"`
}

func writeRecordMeta(roundDir string, meta recordMeta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(roundDir, recordMetaFile)
	tmp := path + ".part"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeRecordMeta(roundDir string) error {
	_ = os.Remove(filepath.Join(roundDir, recordMetaFile+".part"))
	err := os.Remove(filepath.Join(roundDir, recordMetaFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func isNetworkSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func roleKeepsAudio(audioRoles []string, role string) bool {
	for _, item := range audioRoles {
		if strings.TrimSpace(item) == role {
			return true
		}
	}
	return false
}

func recordJobDir(baseDir, outputPath string, roundID int, role string) string {
	fullPath := storagepath.Resolve(baseDir, outputPath)
	return filepath.Join(filepath.Dir(fullPath), jobcontract.DirName, fmt.Sprintf("record-%d-%s", roundID, safeName(role)))
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "role"
	}
	return out
}

func recordFFmpegArgs(sourceURL, outputPath string, keepAudio bool) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-nostdin",
		"-stats_period", "10",
	}
	if isNetworkSource(sourceURL) {
		args = append(args,
			"-user_agent", UA,
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
		"-map", "0:v:0",
	)
	if keepAudio {
		args = append(args, "-map", "0:a:0?", "-c:a", "copy")
	} else {
		args = append(args, "-an")
	}
	args = append(args,
		"-sn",
		"-dn",
		"-c:v", "copy",
		"-f", "flv",
		"-y", outputPath,
	)
	return args
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

func checksum(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
