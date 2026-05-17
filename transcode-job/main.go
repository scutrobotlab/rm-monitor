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
	pathpkg "path"
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
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
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
	sourceRel, err := remoteArtifactPath(transcodeConf.BaseDir, source.Path)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	archiveRel := strings.TrimSuffix(sourceRel, pathpkg.Ext(sourceRel)) + ".mp4"
	tmpArchiveRel := fmt.Sprintf("%s.tmp-%d", archiveRel, taskID)

	workDir := filepath.Join(transcodeConf.LocalWorkDir, strconv.Itoa(taskID))
	sourcePath := filepath.Join(workDir, "source.flv")
	archivePath := filepath.Join(workDir, "archive.mp4")
	if err := os.RemoveAll(workDir); err != nil {
		return errors.Wrap(err, "clean transcode work dir")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return errors.Wrap(err, "create transcode work dir")
	}
	defer os.RemoveAll(workDir)

	rcloneEnv, err := rcloneConfigEnv(ctx, transcodeConf)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if err := ensureSVTAV1(ctx); err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if err := client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusRUNNING).SetStartedAt(time.Now()).Exec(ctx); err != nil {
		return errors.Wrap(err, "mark transcode running")
	}
	if err := rcloneRun(ctx, rcloneEnv, "copyto", remoteRef(transcodeConf, sourceRel), sourcePath); err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "download source")
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
		"-f", "mp4",
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
	if stat.Size() == 0 {
		err := errors.New("archive output is empty")
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	sum, err := checksum(archivePath)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	if dir := pathpkg.Dir(archiveRel); dir != "." {
		if err := rcloneRun(ctx, rcloneEnv, "mkdir", remoteRef(transcodeConf, dir)); err != nil {
			_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
			return errors.Wrap(err, "create remote archive dir")
		}
	}
	if err := rcloneRun(ctx, rcloneEnv, "copyto", archivePath, remoteRef(transcodeConf, tmpArchiveRel)); err != nil {
		_ = rcloneRun(ctx, rcloneEnv, "deletefile", remoteRef(transcodeConf, tmpArchiveRel))
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "upload archive")
	}
	if err := rcloneRun(ctx, rcloneEnv, "moveto", remoteRef(transcodeConf, tmpArchiveRel), remoteRef(transcodeConf, archiveRel)); err != nil {
		_ = rcloneRun(ctx, rcloneEnv, "deletefile", remoteRef(transcodeConf, tmpArchiveRel))
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "publish archive")
	}
	remoteBytes, err := rcloneSize(ctx, rcloneEnv, transcodeConf, archiveRel)
	if err != nil {
		_ = client.TranscodeTask.UpdateOneID(taskID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "stat remote archive")
	}
	if remoteBytes != stat.Size() {
		err := errors.Errorf("remote archive size mismatch: local=%d remote=%d", stat.Size(), remoteBytes)
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

func remoteArtifactPath(baseDir, artifactPath string) (string, error) {
	p := pathpkg.Clean(filepath.ToSlash(strings.TrimSpace(artifactPath)))
	if p == "." || p == "/" {
		return "", errors.New("artifact path is empty")
	}
	if strings.HasPrefix(p, "/") {
		base := pathpkg.Clean(filepath.ToSlash(baseDir))
		if base == "." || base == "/" {
			return "", errors.Errorf("cannot convert absolute artifact path %q with base %q", artifactPath, baseDir)
		}
		if p == base {
			return "", errors.Errorf("artifact path %q points to base dir", artifactPath)
		}
		prefix := strings.TrimSuffix(base, "/") + "/"
		if !strings.HasPrefix(p, prefix) {
			return "", errors.Errorf("artifact path %q is outside base dir %q", artifactPath, baseDir)
		}
		p = strings.TrimPrefix(p, prefix)
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", errors.Errorf("artifact path %q escapes records root", artifactPath)
	}
	return p, nil
}

func remoteRef(conf common.TranscodeConf, rel string) string {
	return fmt.Sprintf("%s:%s", conf.WebDAVRemoteName, filepath.ToSlash(rel))
}

func rcloneConfigEnv(ctx context.Context, conf common.TranscodeConf) ([]string, error) {
	if strings.TrimSpace(conf.WebDAVURL) == "" {
		return nil, errors.New("TranscodeConf.WebDAVURL is required")
	}
	user := strings.TrimSpace(os.Getenv("RCLONE_WEBDAV_USER"))
	pass := os.Getenv("RCLONE_WEBDAV_PASS")
	if user == "" || pass == "" {
		return nil, errors.New("RCLONE_WEBDAV_USER and RCLONE_WEBDAV_PASS are required")
	}
	obscured, err := rcloneOutput(ctx, nil, "obscure", pass)
	if err != nil {
		return nil, errors.Wrap(err, "obscure webdav password")
	}
	prefix := "RCLONE_CONFIG_" + strings.ToUpper(strings.ReplaceAll(conf.WebDAVRemoteName, "-", "_"))
	env := append([]string{}, os.Environ()...)
	env = append(env,
		prefix+"_TYPE=webdav",
		prefix+"_URL="+conf.WebDAVURL,
		prefix+"_VENDOR=other",
		prefix+"_USER="+user,
		prefix+"_PASS="+strings.TrimSpace(obscured),
	)
	return env, nil
}

func rcloneSize(ctx context.Context, env []string, conf common.TranscodeConf, rel string) (int64, error) {
	out, err := rcloneOutput(ctx, env, "size", "--json", remoteRef(conf, rel))
	if err != nil {
		return 0, err
	}
	var data struct {
		Bytes int64 `json:"bytes"`
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return 0, errors.Wrap(err, "parse rclone size")
	}
	if data.Count != 1 || data.Bytes <= 0 {
		return 0, errors.Errorf("unexpected remote archive size: count=%d bytes=%d", data.Count, data.Bytes)
	}
	return data.Bytes, nil
}

func rcloneRun(ctx context.Context, env []string, args ...string) error {
	_, err := rcloneOutput(ctx, env, args...)
	return err
}

func rcloneOutput(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "rclone", args...)
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return "", errors.New(commandError(err, out.String()))
	}
	return out.String(), nil
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
