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
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/ocr-job/internal/config"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "ocr-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	var ocrCtx jobcontract.OCRContext
	if err := jobcontract.ContextFromEnv(&ocrCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	jobDir := ocrJobDir(ocrCtx)
	if err := jobcontract.WriteContext(jobDir, ocrCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	if err := run(context.Background(), c, ocrCtx, jobDir); err != nil {
		_ = jobcontract.WriteError(jobDir, "ocr", ocrCtx.MatchRoundID, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c config.Config, ocrCtx jobcontract.OCRContext, jobDir string) error {
	if ocrCtx.SourceURL == "" {
		return errors.New("source_url is required")
	}

	runCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	frameInterval := c.FrameInterval
	if ocrCtx.FrameInterval > 0 {
		frameInterval = ocrCtx.FrameInterval
	}
	similarityThr := c.SimilarityThr
	if ocrCtx.SimilarityThr > 0 {
		similarityThr = ocrCtx.SimilarityThr
	}

	pythonBin := c.PythonBin
	scriptDir := c.ScriptDir

	args := []string{
		filepath.Join(scriptDir, "ocr_engine.py"),
		"--source-url", ocrCtx.SourceURL,
		"--output-dir", jobDir,
		"--round-dir", ocrCtx.RoundDir,
		"--frame-interval", fmt.Sprintf("%d", frameInterval),
		"--similarity-thr", fmt.Sprintf("%.2f", similarityThr),
	}
	if c.TemplatePath != "" {
		args = append(args, "--template-path", c.TemplatePath)
	}

	cmd := exec.CommandContext(runCtx, pythonBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	logx.Infof("starting ocr engine for round %d role %s", ocrCtx.MatchRoundID, ocrCtx.Role)
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return errors.New("ocr job cancelled by signal")
		}
		msg := commandError(err, stderr.String())
		return errors.New(msg)
	}

	var result OCRResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return errors.Wrap(err, "decode ocr result")
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return jobcontract.WriteResult(jobDir, jobcontract.OCRResult{
		Schema:         "rm-monitor/ocr-result/v1",
		MatchRoundID:   ocrCtx.MatchRoundID,
		SettlementPath: result.SettlementPath,
		OcrDataPath:    result.OcrDataPath,
		ReportTextPath: result.ReportTextPath,
		CompletedAt:    time.Now(),
	})
}

type OCRResult struct {
	SettlementPath string `json:"settlement_path"`
	OcrDataPath    string `json:"ocr_data_path"`
	ReportTextPath string `json:"report_text_path"`
	Error          string `json:"error,omitempty"`
}

func ocrJobDir(ocrCtx jobcontract.OCRContext) string {
	return filepath.Join(ocrCtx.RoundDir, jobcontract.DirName, fmt.Sprintf("ocr-%d", ocrCtx.MatchRoundID))
}

func isNetworkSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
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
