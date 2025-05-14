package mqs

import (
	"context"

	"github.com/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/recorder/internal/svc"
)

type MatchNewRoundLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchNewRoundLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.Match] {
	return &MatchNewRoundLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *MatchNewRoundLogic) Consume(key string, m types.Match) error {
	l.Infof("match new round: %s", key)

	if err := l.svcCtx.Recorder.StopBatch(&m); err != nil {
		return errors.Wrap(err, "failed to stop batch")
	}

	return l.svcCtx.Recorder.StartBatch(l.ctx, &m)
}
