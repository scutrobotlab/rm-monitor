package record

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"
)

type Task struct {
	ctx    context.Context
	cancel context.CancelFunc
	output string
	name   string
	url    string
	logx.Logger
}

func NewTask(name, url, output string) *Task {
	ctx, cancel := context.WithCancel(context.Background())
	return &Task{
		ctx:    ctx,
		cancel: cancel,
		name:   name,
		url:    url,
		output: output,
		Logger: logx.WithContext(ctx),
	}
}

const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

func (t *Task) Start() error {
	cmd := exec.CommandContext(t.ctx,
		"streamlink",
		fmt.Sprintf("hls://%s", t.url),
		"best",
		"-o", t.output+".mp4",
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
		return cmd.Process.Signal(os.Interrupt)
	}

	t.Infof("starting recording %s to %s", t.url, t.output)
	if err := cmd.Run(); err != nil {
		if errors.Is(err, &exec.ExitError{}) || errors.Is(err, context.Canceled) {
			t.Infof("stopped recording %s to %s", t.url, t.output)
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
	t.Info("stopped recording %s to %s", t.url, t.output)
}
