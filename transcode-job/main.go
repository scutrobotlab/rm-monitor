package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
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
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
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
		_ = jobcontract.WriteError(jobDir, "transcode", 0, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, jobCtx jobcontract.TranscodeContext, jobDir string) error {
	if strings.TrimSpace(jobCtx.SourcePath) == "" {
		return errors.New("source_path is required")
	}
	if err := applyRoundBoundary(&jobCtx); err != nil {
		return err
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
	tmpArchiveRel := fmt.Sprintf("%s.tmp-%d", archiveRel, jobCtx.MatchRoundID)
	tmpArchiveMountedPath := filepath.Join(jobCtx.BaseDir, filepath.FromSlash(tmpArchiveRel))

	if err := ensureSVTAV1(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(archiveMountedPath), 0o755); err != nil {
		return errors.Wrap(err, "create archive dir")
	}
	_ = os.Remove(tmpArchiveMountedPath)

	ffmpegArgs := []string{
		"-hide_banner",
		"-loglevel", "info",
	}
	if jobCtx.TrimStartSeconds != nil && *jobCtx.TrimStartSeconds > 0 {
		ffmpegArgs = append(ffmpegArgs, "-ss", fmt.Sprintf("%.3f", *jobCtx.TrimStartSeconds))
	}
	ffmpegArgs = append(ffmpegArgs,
		"-i", sourceMountedPath,
	)
	if jobCtx.TrimStartSeconds != nil && jobCtx.TrimEndSeconds != nil && *jobCtx.TrimEndSeconds > *jobCtx.TrimStartSeconds {
		ffmpegArgs = append(ffmpegArgs, "-t", fmt.Sprintf("%.3f", *jobCtx.TrimEndSeconds-*jobCtx.TrimStartSeconds))
	}
	ffmpegArgs = append(ffmpegArgs,
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
	cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
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
	if err := cropRoundDanmu(jobCtx); err != nil {
		return err
	}
	if err := cropRoundSubtitle(jobCtx); err != nil {
		return err
	}

	result := jobcontract.TranscodeResult{
		Schema:       "rm-monitor/transcode-result/v1",
		MatchID:      jobCtx.MatchID,
		MatchRoundID: jobCtx.MatchRoundID,
		ArchivePath:  archiveRel,
		Format:       "mp4",
		Codec:        "av1",
		FileSize:     stat.Size(),
		Checksum:     sum,
		CompletedAt:  time.Now(),
	}
	if err := jobcontract.WriteTempResult(result); err != nil {
		return err
	}
	return jobcontract.WriteArgoOutputs(map[string]any{
		"archive_path": result.ArchivePath,
		"format":       result.Format,
		"codec":        result.Codec,
		"file_size":    result.FileSize,
		"checksum":     result.Checksum,
	})
}

type bilibiliXML struct {
	XMLName xml.Name `xml:"i"`
	Danmaku []danmuD `xml:"d"`
}

type danmuD struct {
	XMLName xml.Name   `xml:"d"`
	P       string     `xml:"p,attr"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
}

func cropRoundDanmu(jobCtx jobcontract.TranscodeContext) error {
	roundDir := strings.TrimSpace(jobCtx.RoundDir)
	if roundDir == "" {
		return nil
	}
	role := strings.TrimSpace(jobCtx.Role)
	if role == "" {
		role = "主视角"
	}
	rawPath := filepath.Join(roundDir, role+".raw.danmuku.xml")
	finalPath := filepath.Join(roundDir, role+".danmuku.xml")
	if _, err := os.Stat(rawPath); err != nil {
		if !os.IsNotExist(err) {
			return errors.Wrap(err, "stat raw danmu xml")
		}
		if err := os.Rename(finalPath, rawPath); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errors.Wrap(err, "move final danmu xml to raw")
		}
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "read raw danmu xml")
	}
	var doc bilibiliXML
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return errors.Wrap(err, "parse raw danmu xml")
	}
	start := 0.0
	end := 0.0
	hasWindow := jobCtx.TrimStartSeconds != nil && jobCtx.TrimEndSeconds != nil && *jobCtx.TrimEndSeconds > *jobCtx.TrimStartSeconds
	if hasWindow {
		start = *jobCtx.TrimStartSeconds
		end = *jobCtx.TrimEndSeconds
	}
	filtered := make([]danmuD, 0, len(doc.Danmaku))
	for _, item := range doc.Danmaku {
		p := strings.Split(item.P, ",")
		if len(p) == 0 {
			continue
		}
		t, err := strconv.ParseFloat(strings.TrimSpace(p[0]), 64)
		if err != nil {
			continue
		}
		if hasWindow {
			if t < start || t > end {
				continue
			}
			t -= start
		}
		p[0] = fmt.Sprintf("%.3f", t)
		item.P = strings.Join(p, ",")
		filtered = append(filtered, item)
	}
	doc.Danmaku = filtered
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshal final danmu xml")
	}
	tmp := finalPath + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(xml.Header), out...), 0o644); err != nil {
		return errors.Wrap(err, "write final danmu xml")
	}
	return errors.Wrap(os.Rename(tmp, finalPath), "publish final danmu xml")
}

func cropRoundSubtitle(jobCtx jobcontract.TranscodeContext) error {
	roundDir := strings.TrimSpace(jobCtx.RoundDir)
	if roundDir == "" {
		return nil
	}
	start, end, ok := trimWindow(jobCtx)
	if !ok {
		return nil
	}
	role := strings.TrimSpace(jobCtx.Role)
	if role == "" {
		role = "主视角"
	}
	rawPath := filepath.Join(roundDir, role+".raw.srt")
	finalPath := filepath.Join(roundDir, role+".srt")
	if _, err := os.Stat(rawPath); err != nil {
		if !os.IsNotExist(err) {
			return errors.Wrap(err, "stat raw subtitle")
		}
		if err := os.Rename(finalPath, rawPath); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errors.Wrap(err, "move final subtitle to raw")
		}
	}
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	err := subtitle.WriteSRTFromJSONL(sttPath, finalPath, subtitle.Options{Start: &start, End: &end})
	if errors.Is(err, subtitle.ErrNoCues) {
		if removeErr := os.Remove(finalPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Wrap(removeErr, "remove empty final subtitle")
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return errors.Wrap(err, "write cropped subtitle")
}

func trimWindow(jobCtx jobcontract.TranscodeContext) (float64, float64, bool) {
	if jobCtx.TrimStartSeconds == nil || jobCtx.TrimEndSeconds == nil {
		return 0, 0, false
	}
	start := *jobCtx.TrimStartSeconds
	end := *jobCtx.TrimEndSeconds
	return start, end, end > start
}

func applyRoundBoundary(jobCtx *jobcontract.TranscodeContext) error {
	if jobCtx == nil || (jobCtx.TrimStartSeconds != nil && jobCtx.TrimEndSeconds != nil) {
		return nil
	}
	roundDir := strings.TrimSpace(jobCtx.RoundDir)
	if roundDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(roundDir, "round.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "read round analysis")
	}
	var doc struct {
		Boundary struct {
			StartSeconds float64 `json:"start_seconds"`
			EndSeconds   float64 `json:"end_seconds"`
		} `json:"boundary"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return errors.Wrap(err, "parse round analysis")
	}
	if doc.Boundary.EndSeconds <= doc.Boundary.StartSeconds {
		return nil
	}
	start := doc.Boundary.StartSeconds
	end := doc.Boundary.EndSeconds
	jobCtx.TrimStartSeconds = &start
	jobCtx.TrimEndSeconds = &end
	return nil
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
	return filepath.Join(jobCtx.BaseDir, filepath.FromSlash(pathpkg.Dir(archiveRel)), jobcontract.DirName, fmt.Sprintf("transcode-%d", jobCtx.MatchRoundID)), nil
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
