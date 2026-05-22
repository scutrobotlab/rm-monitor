package main

import (
	"context"
	"flag"
	"os"
	"time"

	"scutbot.cn/web/rm-monitor/artifact-cleaner/internal/config"
	"scutbot.cn/web/rm-monitor/artifact-cleaner/internal/logic"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "artifact-cleaner", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()
	transcodeConf := c.TranscodeConf.WithDefaults()
	result, err := logic.CleanExpiredSources(context.Background(), client, transcodeConf.BaseDir, time.Now(), 500)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	logx.Infof("artifact cleanup completed: scanned=%d deleted=%d skipped=%d", result.Scanned, result.Deleted, result.Skipped)
}
