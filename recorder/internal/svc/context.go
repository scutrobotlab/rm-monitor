package svc

import (
	"os"
	"time"

	"github.com/zeromicro/go-queue/natsq"
	"github.com/zeromicro/go-zero/core/logx"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/recorder/internal/config"
	"scutbot.cn/web/rm-monitor/recorder/internal/record"
)

type ServiceContext struct {
	Config      config.Config
	NatsPusher  *natsq.DefaultProducer
	RestyClient *resty.Client
	Recorder    *record.Daemon
}

func NewServiceContext(c config.Config) *ServiceContext {
	p, err := natsq.NewDefaultProducer(&c.NatsConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	ctx := &ServiceContext{
		Config:      c,
		NatsPusher:  p,
		RestyClient: resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
	}

	ctx.Recorder = record.NewDaemon(c.RecordConf.Res, c.RecordConf.BaseDir, *ctx.RestyClient, ctx.NatsPusher)

	return ctx
}
