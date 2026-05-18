package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/record-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	taskIDFlag = flag.Int("task", 0, "record task id")
)

const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const maxPrematureExitAttempts = 3

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "record-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	if *taskIDFlag == 0 {
		logx.Error("task id is required")
		os.Exit(1)
	}
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	if err := run(context.Background(), client, c, *taskIDFlag); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, client *ent.Client, c config.Config, taskID int) error {
	task, err := loadTask(ctx, client, taskID)
	if err != nil {
		return errors.Wrap(err, "get record task")
	}
	conf := c.RecordConf.WithDefaults()
	fullPath := storagepath.Resolve(conf.BaseDir, task.OutputPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return errors.Wrap(err, "create output dir")
	}
	partPath := fullPath + ".part"
	_ = os.Remove(partPath)

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopRequested atomic.Bool
	go watchCancel(jobCtx, client, taskID, &stopRequested, cancel)

	if err := client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(ctx); err != nil {
		return errors.Wrap(err, "mark running")
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-nostdin",
		"-stats_period", "10",
	}
	if isNetworkSource(task.SourceURL) {
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
		"-i", task.SourceURL,
		"-map", "0:v:0",
		"-an",
		"-sn",
		"-dn",
		"-c:v", "copy",
		"-f", "flv",
		"-y", partPath,
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
	logx.Infof("recording %s to %s", task.SourceURL, path.Clean(task.OutputPath))
	err = cmd.Run()
	if jobCtx.Err() != nil && !stopRequested.Load() {
		_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusCANCELED).SetErrorMessage(jobCtx.Err().Error()).Exec(ctx)
		return jobCtx.Err()
	}
	if err != nil && !stopRequested.Load() {
		msg := commandError(err, stderr.String())
		_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(msg).Exec(ctx)
		return errors.New(msg)
	}
	if !stopRequested.Load() {
		latest, latestErr := loadTask(ctx, client, taskID)
		if latestErr != nil {
			_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(latestErr.Error()).Exec(ctx)
			return errors.Wrap(latestErr, "reload record task after ffmpeg exit")
		}
		if latest.Edges.MatchRound != nil && latest.Edges.MatchRound.Status == matchround.StatusSTARTED {
			msg := "ffmpeg exited before match round ended"
			update := client.RecordTask.UpdateOneID(taskID).SetErrorMessage(msg)
			if latest.Attempts < maxPrematureExitAttempts {
				update.SetStatus(recordtask.StatusPENDING).ClearStartedAt()
			} else {
				update.SetStatus(recordtask.StatusFAILED)
			}
			_ = update.Exec(ctx)
			_ = db.Notify(ctx, c.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(taskID))
			return errors.New(msg)
		}
	}

	if err := os.Rename(partPath, fullPath); err != nil {
		_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "commit output")
	}
	stat, statErr := os.Stat(fullPath)
	if statErr != nil {
		_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(statErr.Error()).Exec(ctx)
		return errors.Wrap(statErr, "stat output")
	}
	sum, err := checksum(fullPath)
	if err != nil {
		_ = client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if err := client.RecordTask.UpdateOneID(taskID).
		SetStatus(recordtask.StatusSUCCEEDED).
		SetCompletedAt(time.Now()).
		SetFileSize(stat.Size()).
		SetChecksum(sum).
		ClearErrorMessage().
		Exec(ctx); err != nil {
		return errors.Wrap(err, "mark record succeeded")
	}
	if err := upsertSourceArtifact(ctx, client, taskID, task.OutputPath, stat.Size(), sum); err != nil {
		return errors.Wrap(err, "upsert source artifact")
	}
	return db.Notify(ctx, c.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(taskID))
}

func isNetworkSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func upsertSourceArtifact(ctx context.Context, client *ent.Client, taskID int, outputPath string, size int64, sum string) error {
	return client.MediaArtifact.Create().
		SetRecordTaskID(taskID).
		SetKind(mediaartifact.KindSource).
		SetPath(outputPath).
		SetFormat(mediaartifact.FormatFlv).
		SetCodec(mediaartifact.CodecCopy).
		SetFileSize(size).
		SetChecksum(sum).
		SetStatus(mediaartifact.StatusAVAILABLE).
		OnConflictColumns(mediaartifact.RecordTaskColumn, mediaartifact.FieldKind).
		UpdateNewValues().
		Exec(ctx)
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

func loadTask(ctx context.Context, client *ent.Client, taskID int) (*ent.RecordTask, error) {
	return client.RecordTask.Query().
		Where(recordtask.ID(taskID)).
		WithMatchRound(func(q *ent.MatchRoundQuery) {
			q.WithMatch(func(q *ent.MatchQuery) {
				q.WithRedTeam().WithBlueTeam().WithRounds(func(q *ent.MatchRoundQuery) {
					q.Order(matchround.ByRoundNo())
				})
			})
		}).
		Only(ctx)
}

func watchCancel(ctx context.Context, client *ent.Client, taskID int, stopRequested *atomic.Bool, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task, err := client.RecordTask.Get(ctx, taskID)
			if err == nil && task.Status == recordtask.StatusCANCEL_REQUESTED {
				stopRequested.Store(true)
				cancel()
				return
			}
		}
	}
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
