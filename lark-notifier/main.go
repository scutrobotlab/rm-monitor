package main

import (
	"context"
	"flag"
	"time"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/logic"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "lark-notifier",
		Mode:        "console",
		Encoding:    "plain",
	})
}

func main() {
	flag.Parse()

	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)
	defer svcCtx.DB.Close()

	logx.Info("starting lark notifier")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := logic.NewNotifyLogic(ctx, svcCtx).Sync(); err != nil {
			logx.Errorf("lark notifier sync failed: %v", err)
		}
		cancel()
		<-ticker.C
	}
}
