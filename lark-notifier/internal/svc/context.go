package svc

import (
	"resty.dev/v3"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/zeromicro/go-zero/core/stores/redis"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
)

type ServiceContext struct {
	Config      config.Config
	LarkClient  *lark.Client
	RedisClient *redis.Redis
	RestyClient *resty.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	restyClient := resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second)

	return &ServiceContext{
		Config:      c,
		LarkClient:  lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret, lark.WithHttpClient(restyClient.Client())),
		RedisClient: redis.MustNewRedis(c.RedisConf),
		RestyClient: restyClient,
	}
}
