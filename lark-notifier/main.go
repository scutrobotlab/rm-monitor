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

const (
	startupLookback = 30 * time.Minute
	scanOverlap     = 5 * time.Second
	scanInterval    = 10 * time.Second
	syncTimeout     = 120 * time.Second
)

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
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	lastScan := time.Now().Add(-startupLookback)
	for {
		scanSince := lastScan.Add(-scanOverlap)
		scanStartedAt := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		if err := logic.NewNotifyLogic(ctx, svcCtx).SyncWindow(scanSince); err != nil {
			logx.Errorf("lark notifier scan failed: %v", err)
		} else {
			lastScan = scanStartedAt
		}
		cancel()
		select {
		case <-ticker.C:
		}
	}
}
