package main

import (
	"context"
	"flag"
	"os"
	"time"

	"scutbot.cn/web/rm-monitor/health-checker/internal/config"
	"scutbot.cn/web/rm-monitor/health-checker/internal/logic"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "health-checker", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := db.Open(ctx, c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()

	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	defer redisClient.Close()

	if err := logic.Run(ctx, client, redisClient, logic.CheckConfig{ArgoConf: c.ArgoConf, K8sJobConf: c.K8sJobConf}); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}
