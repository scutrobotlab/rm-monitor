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
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/record-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	taskIDFlag = flag.Int("task", 0, "record task id")
)

const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

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

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopRequested atomic.Bool
	go watchCancel(jobCtx, client, taskID, &stopRequested, cancel)

	if err := client.RecordTask.UpdateOneID(taskID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(ctx); err != nil {
		return errors.Wrap(err, "mark running")
	}
	if err := writeMatchReadme(ctx, client, c, taskID); err != nil {
		logx.Errorf("write match readme before record failed: %v", err)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
	}
	if isNetworkSource(task.SourceURL) {
		args = append(args,
			"-user_agent", UA,
			"-rw_timeout", "15000000",
			"-reconnect", "1",
			"-reconnect_streamed", "1",
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
		"-y", fullPath,
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
		Exec(ctx); err != nil {
		return errors.Wrap(err, "mark record succeeded")
	}
	if err := upsertSourceArtifact(ctx, client, taskID, task.OutputPath, stat.Size(), sum); err != nil {
		return errors.Wrap(err, "upsert source artifact")
	}
	if err := writeMatchReadme(ctx, client, c, taskID); err != nil {
		logx.Errorf("write match readme after record failed: %v", err)
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

func writeMatchReadme(ctx context.Context, client *ent.Client, c config.Config, taskID int) error {
	task, err := loadTask(ctx, client, taskID)
	if err != nil {
		return err
	}
	r := task.Edges.MatchRound
	if r == nil || r.Edges.Match == nil {
		return errors.New("record task missing match round or match edge")
	}
	m := r.Edges.Match
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return err
	}
	recordConf := c.RecordConf.WithDefaults()
	matchDir, err := pathfmt.RenderMatchDir(recordConf.MatchNameTemplate, recordConf.MatchDirTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
		RoundNo:    r.RoundNo,
		Role:       task.Role,
	})
	if err != nil {
		return err
	}
	fullDir := filepath.Join(recordConf.BaseDir, filepath.FromSlash(matchDir))
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return err
	}
	unlock, err := lockDir(ctx, fullDir)
	if err != nil {
		return err
	}
	defer unlock()

	content := renderReadme(m, red, blue)
	tmp := filepath.Join(fullDir, ".README.md.tmp")
	dst := filepath.Join(fullDir, "README.md")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func lockDir(ctx context.Context, dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".README.md.lock")
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func renderReadme(m *ent.Match, red, blue *ent.Team) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %d. %s-%s VS %s-%s\n\n", m.Order, red.SchoolName, red.Name, blue.SchoolName, blue.Name)
	fmt.Fprintf(&buf, "- Event: %s\n", m.Event)
	fmt.Fprintf(&buf, "- Zone: %s\n", m.Zone)
	fmt.Fprintf(&buf, "- Match ID: %s\n", m.ID)
	fmt.Fprintf(&buf, "- Match Type: %s\n", m.MatchType)
	if m.MatchSlug != nil && *m.MatchSlug != "" {
		fmt.Fprintf(&buf, "- Match Slug: %s\n", *m.MatchSlug)
	}
	fmt.Fprintf(&buf, "- Red: %s - %s\n", red.SchoolName, red.Name)
	fmt.Fprintf(&buf, "- Blue: %s - %s\n", blue.SchoolName, blue.Name)
	fmt.Fprintf(&buf, "- Current Status: %s\n", m.LatestStatus)
	fmt.Fprintf(&buf, "- Winner: %s\n\n", matchWinner(m.Edges.Rounds))
	buf.WriteString("## Rounds\n\n")
	buf.WriteString("| Round | Status | Winner | Started At | Ended At |\n")
	buf.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range m.Edges.Rounds {
		fmt.Fprintf(&buf, "| %d | %s | %s | %s | %s |\n",
			r.RoundNo,
			r.Status,
			roundWinner(r),
			formatTime(r.StartedAt),
			formatOptionalTime(r.EndedAt),
		)
	}
	return buf.String()
}

func matchWinner(rounds []*ent.MatchRound) string {
	redWins, blueWins := 0, 0
	for _, r := range rounds {
		if r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			redWins++
		case matchround.WinnerBlue:
			blueWins++
		}
	}
	switch {
	case redWins > blueWins:
		return "red"
	case blueWins > redWins:
		return "blue"
	case redWins == 0 && blueWins == 0:
		return ""
	default:
		return "draw"
	}
}

func roundWinner(r *ent.MatchRound) string {
	if r.Winner == nil {
		return ""
	}
	return string(*r.Winner)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
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
