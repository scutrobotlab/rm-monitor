package svc

import (
	"time"

	"github.com/zeromicro/go-queue/kq"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/recorder/internal/config"
	"scutbot.cn/web/rm-monitor/recorder/internal/record"
)

type ServiceContext struct {
	Config         config.Config
	KqPusherClient *kq.Pusher
	RestyClient    *resty.Client
	Recorder       *record.Daemon
}

func NewServiceContext(c config.Config) *ServiceContext {
	ctx := &ServiceContext{
		Config:         c,
		KqPusherClient: kq.NewPusher(c.KqPusherConf.Brokers, c.KqPusherConf.Topic),
		RestyClient:    resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
	}

	ctx.Recorder = record.NewDaemon(c.RecordConf.Res, c.RecordConf.BaseDir, *ctx.RestyClient, ctx.KqPusherClient)

	return ctx
}
