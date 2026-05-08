package main

import (
	"context"
	"flag"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/transcode-dispatcher/internal/config"
	"scutbot.cn/web/rm-monitor/transcode-dispatcher/internal/logic"
	"scutbot.cn/web/rm-monitor/transcode-dispatcher/internal/svc"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "transcode-dispatcher", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	conf.MustLoad(*configFile, &c)
	svcCtx := svc.NewServiceContext(c)
	defer svcCtx.DB.Close()

	wake := make(chan struct{}, 1)
	go listen(c.PostgresConf.DSN, wake)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := logic.NewDispatchLogic(ctx, svcCtx).Tick(); err != nil {
			logx.Errorf("transcode dispatch tick failed: %v", err)
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
		l, err := db.NewListener(context.Background(), dsn, db.RecordTaskChangedChannel)
		if err != nil {
			logx.Errorf("transcode dispatcher listener failed: %v", err)
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
