package mqs

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/recorder/internal/svc"
)

type MatchDoneLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchDoneLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.Match] {
	return &MatchDoneLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *MatchDoneLogic) Consume(key string, m types.Match) error {
	l.Infof("match done: %s", key)

	return l.svcCtx.Recorder.StopBatch(&m)
}
