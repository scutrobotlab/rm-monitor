package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

type LogConf struct {
	ServiceName string
	Mode        string
	Encoding    string
}

type Logger struct {
	ctx context.Context
}

func MustSetup(conf LogConf) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("service", conf.ServiceName))
}

func WithContext(ctx context.Context) Logger {
	return Logger{ctx: ctx}
}

func Debug(args ...any) {
	slog.Default().Debug(fmt.Sprint(args...))
}

func Info(args ...any) {
	slog.Default().Info(fmt.Sprint(args...))
}

func Error(args ...any) {
	slog.Default().Error(fmt.Sprint(args...))
}

func Debugf(format string, args ...any) {
	slog.Default().Debug(fmt.Sprintf(format, args...))
}

func Infof(format string, args ...any) {
	slog.Default().Info(fmt.Sprintf(format, args...))
}

func Errorf(format string, args ...any) {
	slog.Default().Error(fmt.Sprintf(format, args...))
}

func (l Logger) Debugf(format string, args ...any) {
	slog.Default().DebugContext(l.ctx, fmt.Sprintf(format, args...))
}

func (l Logger) Infof(format string, args ...any) {
	slog.Default().InfoContext(l.ctx, fmt.Sprintf(format, args...))
}

func (l Logger) Errorf(format string, args ...any) {
	slog.Default().ErrorContext(l.ctx, fmt.Sprintf(format, args...))
}

func (l Logger) Error(args ...any) {
	slog.Default().ErrorContext(l.ctx, fmt.Sprint(args...))
}
