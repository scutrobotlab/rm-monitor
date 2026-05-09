package main

import (
	"context"
	"flag"
	"time"

	"scutbot.cn/web/rm-monitor/pkg/logx"

	"scutbot.cn/web/rm-monitor/monitor/internal/config"
	"scutbot.cn/web/rm-monitor/monitor/internal/logic"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/internal/ticker"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logc"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "monitor",
		Mode:        "console",
		Encoding:    "plain",
	})
}

func main() {
	flag.Parse()

	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)

	t := ticker.NewTicker(1 * time.Second)
	defer t.Stop()
	t.AddJob(func(ctx context.Context) error {
		logc.Infof(ctx, "starting match scan")
		defer logc.Infof(ctx, "match scan finished")
		return logic.NewMatchScanLogic(ctx, svcCtx).MatchScan()
	})

	logx.Info("starting monitor")
	t.Start()
}
