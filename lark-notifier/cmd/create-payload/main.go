package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/logic"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	matchID    = flag.String("match", "", "match id")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "lark-notifier-create-payload", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	if *matchID == "" {
		fmt.Fprintln(os.Stderr, "-match is required")
		os.Exit(2)
	}
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	svcCtx := svc.NewServiceContext(c)
	defer svcCtx.DB.Close()

	payload, err := logic.CreateCardPayload(context.Background(), svcCtx, *matchID)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	os.Stdout.Write(payload)
	os.Stdout.Write([]byte("\n"))
}
