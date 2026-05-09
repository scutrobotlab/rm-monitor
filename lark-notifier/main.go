package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/logic"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
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
	events := make(chan notifyEvent, 32)
	go listen(c.PostgresConf.DSN, events)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case event := <-events:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := logic.NewNotifyLogic(ctx, svcCtx).SyncEvent(event.Channel, event.Payload); err != nil {
				logx.Errorf("lark notifier event sync failed: %v", err)
			}
			cancel()
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := logic.NewNotifyLogic(ctx, svcCtx).Sync(); err != nil {
				logx.Errorf("lark notifier sync failed: %v", err)
			}
			cancel()
		}
	}
}

type notifyEvent struct {
	Channel string
	Payload string
}

func listen(dsn string, events chan<- notifyEvent) {
	for {
		l, err := db.NewListener(context.Background(), dsn, db.MatchRoundChangedChannel, db.UploadTaskChangedChannel)
		if err != nil {
			logx.Errorf("lark notifier listener failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for {
			channel, payload, err := l.Wait(context.Background())
			if err != nil {
				_ = l.Close(context.Background())
				break
			}
			select {
			case events <- notifyEvent{Channel: channel, Payload: payload}:
			default:
				logx.Error(fmt.Errorf("drop lark notifier event: channel=%s payload=%s", channel, payload))
			}
		}
	}
}
