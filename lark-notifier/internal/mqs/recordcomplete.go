package mqs

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/recorder/types"
)

type RecordCompletedLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRecordCompletedLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.RecordCompletedEvent] {
	return &RecordCompletedLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RecordCompletedLogic) Consume(key string, m types.RecordCompletedEvent) error {
	l.Infof("record completed: %+v %s", m.Match, m.Role)

	return nil
}
