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
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/transcode-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "transcode-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	var jobCtx jobcontract.TranscodeContext
	if err := jobcontract.ContextFromEnv(&jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if jobCtx.BaseDir == "" {
		jobCtx.BaseDir = c.TranscodeConf.WithDefaults().BaseDir
	}
	if jobCtx.SourceRetentionDays == 0 {
		jobCtx.SourceRetentionDays = c.TranscodeConf.WithDefaults().SourceRetentionDays
	}
	jobDir, err := transcodeJobDir(jobCtx)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if err := jobcontract.WriteContext(jobDir, jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if err := run(context.Background(), jobCtx, jobDir); err != nil {
		_ = jobcontract.WriteError(jobDir, "transcode", jobCtx.TaskID, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, jobCtx jobcontract.TranscodeContext, jobDir string) error {
	if jobCtx.TaskID == 0 {
		return errors.New("task_id is required")
	}
	sourceRel, sourceMountedPath, err := artifactPath(jobCtx.BaseDir, jobCtx.SourcePath)
	if err != nil {
		return err
	}
	archiveRel := strings.TrimSpace(jobCtx.ArchivePath)
	if archiveRel == "" {
		archiveRel = strings.TrimSuffix(sourceRel, pathpkg.Ext(sourceRel)) + ".mp4"
	}
	archiveRel, archiveMountedPath, err := artifactPath(jobCtx.BaseDir, archiveRel)
	if err != nil {
		return err
	}
	tmpArchiveRel := fmt.Sprintf("%s.tmp-%d", archiveRel, jobCtx.TaskID)
	tmpArchiveMountedPath := filepath.Join(jobCtx.BaseDir, filepath.FromSlash(tmpArchiveRel))

	if err := ensureSVTAV1(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(archiveMountedPath), 0o755); err != nil {
		return errors.Wrap(err, "create archive dir")
	}
	_ = os.Remove(tmpArchiveMountedPath)

	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "info",
		"-i", sourceMountedPath,
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
		"-y", tmpArchiveMountedPath,
	)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpArchiveMountedPath)
		msg := commandError(err, stderr.String())
		return errors.New(msg)
	}
	stat, err := os.Stat(tmpArchiveMountedPath)
	if err != nil {
		return errors.Wrap(err, "stat archive")
	}
	if stat.Size() == 0 {
		err := errors.New("archive output is empty")
		_ = os.Remove(tmpArchiveMountedPath)
		return err
	}
	sum, err := checksum(tmpArchiveMountedPath)
	if err != nil {
		_ = os.Remove(tmpArchiveMountedPath)
		return err
	}
	if err := os.Rename(tmpArchiveMountedPath, archiveMountedPath); err != nil {
		_ = os.Remove(tmpArchiveMountedPath)
		return errors.Wrap(err, "publish archive")
	}
	published, err := os.Stat(archiveMountedPath)
	if err != nil {
		return errors.Wrap(err, "stat published archive")
	}
	if published.Size() != stat.Size() {
		err := errors.Errorf("published archive size mismatch: local=%d published=%d", stat.Size(), published.Size())
		return err
	}

	return jobcontract.WriteResult(jobDir, jobcontract.TranscodeResult{
		Schema:           "rm-monitor/transcode-result/v1",
		TaskID:           jobCtx.TaskID,
		SourceArtifactID: jobCtx.SourceArtifactID,
		RecordTaskID:     jobCtx.RecordTaskID,
		ArchivePath:      archiveRel,
		Format:           "mp4",
		Codec:            "av1",
		FileSize:         stat.Size(),
		Checksum:         sum,
		CompletedAt:      time.Now(),
	})
}

func transcodeJobDir(jobCtx jobcontract.TranscodeContext) (string, error) {
	archiveRel := strings.TrimSpace(jobCtx.ArchivePath)
	if archiveRel == "" {
		sourceRel, _, err := artifactPath(jobCtx.BaseDir, jobCtx.SourcePath)
		if err != nil {
			return "", err
		}
		archiveRel = strings.TrimSuffix(sourceRel, pathpkg.Ext(sourceRel)) + ".mp4"
	}
	archiveRel, _, err := artifactPath(jobCtx.BaseDir, archiveRel)
	if err != nil {
		return "", err
	}
	return filepath.Join(jobCtx.BaseDir, filepath.FromSlash(pathpkg.Dir(archiveRel)), jobcontract.DirName, fmt.Sprintf("transcode-%d", jobCtx.TaskID)), nil
}

func artifactPath(baseDir, artifactPath string) (string, string, error) {
	p := pathpkg.Clean(filepath.ToSlash(strings.TrimSpace(artifactPath)))
	if p == "." || p == "/" {
		return "", "", errors.New("artifact path is empty")
	}
	base := pathpkg.Clean(filepath.ToSlash(baseDir))
	if base == "." || base == "/" {
		return "", "", errors.Errorf("invalid base dir %q", baseDir)
	}
	if strings.HasPrefix(p, "/") {
		if p == base {
			return "", "", errors.Errorf("artifact path %q points to base dir", artifactPath)
		}
		prefix := strings.TrimSuffix(base, "/") + "/"
		if !strings.HasPrefix(p, prefix) {
			return "", "", errors.Errorf("artifact path %q is outside base dir %q", artifactPath, baseDir)
		}
		p = strings.TrimPrefix(p, prefix)
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", "", errors.Errorf("artifact path %q escapes records root", artifactPath)
	}
	return p, filepath.Join(baseDir, filepath.FromSlash(p)), nil
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
