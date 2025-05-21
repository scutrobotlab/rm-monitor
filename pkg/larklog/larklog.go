package larklog

import (
	"context"

	"github.com/zeromicro/go-zero/core/logc"
)

type LarkLog struct{}

func (l LarkLog) Debug(ctx context.Context, i ...interface{}) {
	logc.Debug(ctx, i)
}

func (l LarkLog) Info(ctx context.Context, i ...interface{}) {
	logc.Info(ctx, i)
}

func (l LarkLog) Warn(ctx context.Context, i ...interface{}) {
	logc.Error(ctx, i)
}

func (l LarkLog) Error(ctx context.Context, i ...interface{}) {
	logc.Error(ctx, i)
}

func NewLarkLog() *LarkLog {
	return &LarkLog{}
}
