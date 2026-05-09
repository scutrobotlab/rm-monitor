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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/transcode-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	taskIDFlag = flag.Int("task", 0, "transcode task id")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "transcode-job", Mode: "console", Encoding: "plain"})
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
	task, err := client.TranscodeTask.Query().
		Where(transcodetask.ID(taskID)).
		WithSourceArtifact(func(q *ent.MediaArtifactQuery) {
			q.WithRecordTask()
		}).
		Only(ctx)
	if err != nil {
		return errors.Wrap(err, "get transcode task")
	}
	source := task.Edges.SourceArtifact
	if source == nil || source.Edges.RecordTask == nil {
		return errors.New("transcode task missing source artifact or record task")
	}
	transcodeConf := c.TranscodeConf.WithDefaults()
	sourcePath := storagepath.Resolve(transcodeConf.BaseDir, source.Path)
	archiveRel := strings.TrimSuffix(source.Path, filepath.Ext(source.Path)) + ".mp4"
	archivePath := storagepath.Resolve(transcodeConf.BaseDir, archiveRel)
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return errors.Wrap(err, "create archive dir")
	}
	if err := ensureSVTAV1(ctx); err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if err := client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusRUNNING).SetStartedAt(time.Now()).Exec(ctx); err != nil {
		return errors.Wrap(err, "mark transcode running")
	}
	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "info",
		"-i", sourcePath,
		"-map", "0:v:0",
		"-an",
		"-sn",
		"-dn",
		"-c:v", "libsvtav1",
		"-preset", "8",
		"-b:v", "1000k",
		"-g", "125",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-y", archivePath,
	)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		msg := commandError(err, stderr.String())
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(msg).Exec(ctx)
		return errors.New(msg)
	}
	stat, err := os.Stat(archivePath)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "stat archive")
	}
	sum, err := checksum(archivePath)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if err := client.MediaArtifact.Create().
		SetRecordTaskID(source.Edges.RecordTask.ID).
		SetKind(mediaartifact.KindArchive).
		SetPath(archiveRel).
		SetFormat(mediaartifact.FormatMp4).
		SetCodec(mediaartifact.CodecAv1).
		SetFileSize(stat.Size()).
		SetChecksum(sum).
		SetStatus(mediaartifact.StatusAVAILABLE).
		OnConflictColumns(mediaartifact.RecordTaskColumn, mediaartifact.FieldKind).
		UpdateNewValues().
		Exec(ctx); err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "upsert archive artifact")
	}
	archive, err := client.MediaArtifact.Query().
		Where(
			mediaartifact.HasRecordTaskWith(recordtask.ID(source.Edges.RecordTask.ID)),
			mediaartifact.KindEQ(mediaartifact.KindArchive),
		).
		Only(ctx)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "query archive artifact")
	}
	now := time.Now()
	if err := client.MediaArtifact.UpdateOneID(source.ID).
		SetDeletableAt(now.AddDate(0, 0, transcodeConf.SourceRetentionDays)).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "set source retention")
	}
	if err := client.TranscodeTask.UpdateOneID(taskID).
		SetArchiveArtifactID(archive.ID).
		SetStatus(transcodetask.StatusSUCCEEDED).
		SetCompletedAt(now).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "mark transcode succeeded")
	}
	return db.Notify(ctx, c.PostgresConf.DSN, db.TranscodeTaskChangedChannel, strconv.Itoa(taskID))
}

func ensureSVTAV1(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrap(err, "check ffmpeg encoders")
	}
	if !bytes.Contains(out, []byte("libsvtav1")) {
		return errors.New("ffmpeg libsvtav1 encoder is not available")
	}
	return nil
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
