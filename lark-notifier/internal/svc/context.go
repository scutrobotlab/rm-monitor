package svc

import (
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"net/http"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
)

type ServiceContext struct {
	Config      config.Config
	LarkClient  *lark.Client
	RedisClient *redis.Redis
	HttpClient  *http.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{
		Config:      c,
		LarkClient:  lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret),
		RedisClient: redis.MustNewRedis(c.RedisConf),
		HttpClient:  &http.Client{},
	}
}
