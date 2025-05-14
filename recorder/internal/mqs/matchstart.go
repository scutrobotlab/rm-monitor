package mqs

import (
	"context"

	"github.com/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/recorder/internal/svc"
)

type MatchStartLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchStartLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.Match] {
	return &MatchStartLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *MatchStartLogic) Consume(key string, m types.Match) error {
	l.Infof("match start: %s", key)

	if err := l.svcCtx.Recorder.StopBatch(&m); err != nil {
		return errors.Wrap(err, "failed to stop batch")
	}

	return l.svcCtx.Recorder.StartBatch(l.ctx, &m)
}
