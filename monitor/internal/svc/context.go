package svc

import (
	"os"
	"time"

	"github.com/zeromicro/go-queue/natsq"
	"github.com/zeromicro/go-zero/core/logx"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/monitor/internal/config"
)

type ServiceContext struct {
	Config      config.Config
	RedisClient *redis.Redis
	RestyClient *resty.Client
	NatsPusher  *natsq.DefaultProducer
}

func NewServiceContext(c config.Config) *ServiceContext {
	p, err := natsq.NewDefaultProducer(c.NatsConf.Conf())
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	return &ServiceContext{
		Config:      c,
		RedisClient: redis.MustNewRedis(c.RedisConf),
		RestyClient: resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
		NatsPusher:  p,
	}
}
