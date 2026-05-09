package svc

import (
	"context"
	"os"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/uploader-dispatcher/internal/config"
)

type ServiceContext struct {
	Config config.Config
	DB     *ent.Client
	Redis  *redisx.Client
	Lark   *lark.Client
	K8s    *kubejob.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	k8s, err := kubejob.NewInClusterClient()
	if err != nil {
		logx.Errorf("k8s client disabled: %v", err)
	}
	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	restyClient := resty.New().SetRetryCount(3).SetRetryWaitTime(time.Second).SetTimeout(30 * time.Second)
	return &ServiceContext{
		Config: c,
		DB:     client,
		Redis:  redisClient,
		Lark: lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
			lark.WithHttpClient(restyClient.Client()),
			lark.WithEnableTokenCache(true),
			lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
			lark.WithLogger(larklog.NewLarkLog())),
		K8s: k8s,
	}
}
