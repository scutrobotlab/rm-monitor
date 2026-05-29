package main

import (
	"context"
	"flag"
	"time"

	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/config"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/logic"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/svc"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "record-dispatcher", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)
	defer svcCtx.DB.Close()

	wake := make(chan struct{}, 1)
	go listen(c.PostgresConf.DSN, wake)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	source := "startup"
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		stats, err := logic.NewDispatchLogic(ctx, svcCtx).Tick(source)
		if err != nil {
			logx.Errorf("record dispatch tick failed: %v", err)
		} else {
			logx.Infof("record dispatch tick source=%s cancelled=%d created=%d recovered=%d dispatched=%d result_applied=%d manifest_created=%d",
				stats.Source, stats.Cancelled, stats.Created, stats.Recovered, stats.Dispatched, stats.ResultApplied, stats.ManifestCreated)
		}
		cancel()
		select {
		case <-wake:
			source = "notify"
		case <-ticker.C:
			source = "ticker"
		}
	}
}

func listen(dsn string, wake chan<- struct{}) {
	for {
		ctx := context.Background()
		l, err := db.NewListener(ctx, dsn, db.MatchRoundChangedChannel)
		if err != nil {
			logx.Errorf("record dispatcher listener failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for {
			if _, _, err := l.Wait(ctx); err != nil {
				_ = l.Close(ctx)
				break
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}
