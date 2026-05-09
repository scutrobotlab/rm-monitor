package svc

import (
	"context"
	"os"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"

	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/monitor/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
)

type ServiceContext struct {
	Config      config.Config
	RestyClient *resty.Client
	DB          *ent.Client
	RedisClient *redis.Redis
}

func NewServiceContext(c config.Config) *ServiceContext {
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	return &ServiceContext{
		Config:      c,
		RestyClient: resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
		DB:          client,
		RedisClient: redis.MustNewRedis(c.RedisConf),
	}
}
