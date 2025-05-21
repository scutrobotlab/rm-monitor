package svc

import (
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
)

type ServiceContext struct {
	Config      config.Config
	LarkClient  *lark.Client
	RedisClient *redis.Redis
	RestyClient *resty.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	restyClient := resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second)
	redisClient := redis.MustNewRedis(c.RedisConf)
	return &ServiceContext{
		Config: c,
		LarkClient: lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
			lark.WithHttpClient(restyClient.Client()),
			lark.WithEnableTokenCache(true),
			lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
			lark.WithLogger(larklog.NewLarkLog())),
		RedisClient: redisClient,
		RestyClient: restyClient,
	}
}
