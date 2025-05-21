package record

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/zeromicro/go-queue/natsq"

	"github.com/zeromicro/go-zero/core/jsonx"

	"scutbot.cn/web/rm-monitor/monitor/types"
	types2 "scutbot.cn/web/rm-monitor/recorder/types"

	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"
)

type Task struct {
	ctx    context.Context
	cancel context.CancelFunc
	match  *types.Match
	role   string
	pusher *natsq.DefaultProducer
	output string
	name   string
	url    string
	logx.Logger
}

func NewTask(name, url, baseDir, role string, m *types.Match) *Task {
	ctx, cancel := context.WithCancel(context.Background())
	return &Task{
		ctx:     ctx,
		cancel:  cancel,
		name:    name,
		role:    role,
		url:     url,
		baseDir: baseDir,
		match:   m,
		Logger:  logx.WithContext(ctx),
	}
}

const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

func (t *Task) Start(output string) error {
	oout := output
	output = path.Join(t.baseDir, output)
	cmd := exec.CommandContext(t.ctx,
		"streamlink",
		fmt.Sprintf("hls://%s", t.url),
		"best",
		"-o", output+".mp4",
		"--hls-live-restart",
		"--ffmpeg-video-transcode",
		"h264",
		"--ffmpeg-copyts",
		"--ffmpeg-start-at-zero",
		"--force",
		"--progress", "no",
		"--http-header", "User-Agent="+UA,
	)

	t.Debugf("start %s", cmd.String())

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return errors.Wrapf(err, "failed to send interrupt signal to streamlink %s", t.url)
		}
		payload := types2.RecordCompletedEvent{
			Match: t.match,
			Path:  oout,
			Role:  t.role,
		}
		p, _ := jsonx.MarshalToString(payload)

		if err := t.pusher.Push(context.Background(), p); err != nil {
			return errors.Wrapf(err, "failed to push record completed event %s", t.url)
		}

		t.Infof("pushed record completed event %s", t.url)
		return nil
	}

	t.Infof("starting recording %s to %s", t.url, output)
	if err := cmd.Run(); err != nil {
		if errors.Is(err, &exec.ExitError{}) || errors.Is(err, context.Canceled) {
			t.Infof("stopped recording %s to %s", t.url, output)
		} else {
			t.Error(errors.Wrapf(err, "failed to start streamlink %s", t.url))
		}
	}

	return nil
}

func (t *Task) Stop() {
	t.cancel()
	// wait for the process to finish
	if err := t.ctx.Err(); err != nil {
		t.Error(errors.Wrapf(err, "failed to stop streamlink %s", t.url))
	}
	t.Info("stopped recording %s", t.url)
}
