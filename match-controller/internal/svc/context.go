package svc

import (
	"context"
	"os"
	"time"

	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/match-controller/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/argowf"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

type ServiceContext struct {
	Config      config.Config
	RestyClient *resty.Client
	DB          *ent.Client
	RedisClient *redisx.Client
	ArgoClient  *argowf.Client
}

func NewServiceContext(c config.Config) *ServiceContext {
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	var argoClient *argowf.Client
	if c.ArgoConf.WithDefaults().Enabled {
		argoClient, err = argowf.NewInClusterOrKubeconfig(c.ArgoConf.Kubeconfig)
		if err != nil {
			logx.Error(err)
			os.Exit(1)
		}
	}

	return &ServiceContext{
		Config:      c,
		RestyClient: resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(5 * time.Second),
		DB:          client,
		RedisClient: redisx.MustNew(c.RedisConf.WithDefaults()),
		ArgoClient:  argoClient,
	}
}
