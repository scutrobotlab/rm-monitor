package main

import (
	"context"
	"flag"
	"time"

	"scutbot.cn/web/rm-monitor/highlight-dispatcher/internal/config"
	"scutbot.cn/web/rm-monitor/highlight-dispatcher/internal/logic"
	"scutbot.cn/web/rm-monitor/highlight-dispatcher/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "highlight-dispatcher", Mode: "console", Encoding: "plain"})
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
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := logic.NewDispatchLogic(ctx, svcCtx).Tick(); err != nil {
			logx.Errorf("highlight dispatch tick failed: %v", err)
		}
		cancel()
		select {
		case <-wake:
		case <-ticker.C:
		}
	}
}

func listen(dsn string, wake chan<- struct{}) {
	for {
		l, err := db.NewListener(context.Background(), dsn, db.MatchRoundChangedChannel, db.RecordTaskChangedChannel)
		if err != nil {
			logx.Errorf("highlight dispatcher listener failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for {
			if _, _, err := l.Wait(context.Background()); err != nil {
				_ = l.Close(context.Background())
				break
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
}
