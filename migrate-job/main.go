package main

import (
	"context"
	"flag"

	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

type Config struct {
	PostgresConf config.PostgresConf
}

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "migrate-job",
		Mode:        "console",
		Encoding:    "plain",
	})
}

func main() {
	flag.Parse()

	var c Config
	app.MustLoadConfig(*configFile, &c)

	logx.Info("starting database migration")
	if err := db.Migrate(context.Background(), c.PostgresConf); err != nil {
		panic(err)
	}
	logx.Info("database migration finished")
}
