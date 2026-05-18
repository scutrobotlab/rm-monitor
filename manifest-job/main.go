package main

import (
	"context"
	"flag"
	"os"

	"scutbot.cn/web/rm-monitor/manifest-job/internal/config"
	"scutbot.cn/web/rm-monitor/manifest-job/internal/logic"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	matchID    = flag.String("match", "", "match id")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "manifest-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	if *matchID == "" {
		logx.Error("match id is required")
		os.Exit(1)
	}
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	if err := logic.WriteMatchReadme(context.Background(), client, c.RecordConf, c.ReportConf, c.PostgresConf.DSN, *matchID); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}
