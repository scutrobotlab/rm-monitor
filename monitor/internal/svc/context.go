package svc

import (
	"time"

	"github.com/zeromicro/go-queue/kq"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/monitor/internal/config"
)

type ServiceContext struct {
	Config         config.Config
	KqPusherClient *kq.Pusher
	RedisClient    *redis.Redis
	RestyClient    *resty.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{
		Config:         c,
		KqPusherClient: kq.NewPusher(c.KqPusherConf.Brokers, c.KqPusherConf.Topic),
		RedisClient:    redis.MustNewRedis(c.RedisConf),
		RestyClient:    resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
	}
}
