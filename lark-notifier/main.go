package main

import (
	"flag"

	"github.com/zeromicro/go-zero/core/service"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/mqs"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")

func init() {
	logx.SetUp(logx.LogConf{
		ServiceName: "monitor",
		Mode:        "console",
		Encoding:    "plain",
	})
}

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)
	s := mqs.NewConsumerRouter(svcCtx)
	g := service.NewServiceGroup()
	for _, sv := range s {
		g.Add(sv)
	}
	defer g.Stop()

	logx.Info("starting lark notifier")
	g.Start()
}
