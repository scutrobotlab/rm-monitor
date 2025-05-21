package main

import (
	"flag"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/recorder/internal/config"
	"scutbot.cn/web/rm-monitor/recorder/internal/mqs"
	"scutbot.cn/web/rm-monitor/recorder/internal/svc"
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
	defer s.Stop()
	defer svcCtx.Recorder.Close()
	defer func() {
		if r := recover(); r != nil {
			logx.Errorf("recovered from panic: %v", r)
		}
	}()

	logx.Info("starting lark notifier")
	s.Start()
}
