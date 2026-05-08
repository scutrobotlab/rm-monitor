package svc

import (
	"context"
	"os"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
)

type ServiceContext struct {
	Config      config.Config
	LarkClient  *lark.Client
	RedisClient *redis.Redis
	RestyClient *resty.Client
	DB          *ent.Client
	UploadSlots chan struct{}
}

func NewServiceContext(c config.Config) *ServiceContext {
	restyClient := resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(10 * time.Second)
	redisClient := redis.MustNewRedis(c.RedisConf)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	uploadConcurrency := c.UploadConf.Concurrency
	if uploadConcurrency <= 0 {
		uploadConcurrency = 1
	}
	return &ServiceContext{
		Config: c,
		LarkClient: lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
			lark.WithHttpClient(restyClient.Client()),
			lark.WithEnableTokenCache(true),
			lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
			lark.WithLogger(larklog.NewLarkLog())),
		RedisClient: redisClient,
		RestyClient: restyClient,
		DB:          client,
		UploadSlots: make(chan struct{}, uploadConcurrency),
	}
}
