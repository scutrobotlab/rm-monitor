package svc

import (
	"context"
	"os"
	"time"

	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/config"
)

type ServiceContext struct {
	Config      config.Config
	DB          *ent.Client
	Redis       *redisx.Client
	RestyClient *resty.Client
	K8s         *kubejob.Client
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
	return &ServiceContext{
		Config:      c,
		DB:          client,
		Redis:       redisx.MustNew(c.RedisConf.WithDefaults()),
		RestyClient: resty.New().SetRetryCount(3).SetRetryWaitTime(time.Second).SetTimeout(10 * time.Second),
		K8s:         k8s,
	}
}
