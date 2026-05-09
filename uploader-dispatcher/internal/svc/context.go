package svc

import (
	"context"
	"os"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/uploader-dispatcher/internal/config"
)

type ServiceContext struct {
	Config config.Config
	DB     *ent.Client
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
	return &ServiceContext{Config: c, DB: client, K8s: k8s}
}
