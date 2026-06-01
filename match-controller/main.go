package main

import (
	"context"
	"flag"
	"os"
	"time"

	"scutbot.cn/web/rm-monitor/pkg/logx"

	"scutbot.cn/web/rm-monitor/match-controller/internal/config"
	"scutbot.cn/web/rm-monitor/match-controller/internal/logic"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/match-controller/internal/ticker"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logc"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")
var roundGate = flag.Bool("round-gate", false, "write Argo round gate outputs and exit")
var roundGateMatchID = flag.String("match", "", "match id for -round-gate")
var roundGateRoundNo = flag.Int("round", 0, "round number for -round-gate")
var roundGatePlanGameCount = flag.Int("plan-game-count", 5, "planned game count for -round-gate")
var roundGateRoleSpecs = flag.String("role-specs", "[]", "role specs JSON for -round-gate")
var roundGateChatRoomID = flag.String("chat-room-id", "", "chat room id for -round-gate")

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "match-controller",
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

	if *roundGate {
		if err := logic.NewRoundGateLogic(context.Background(), svcCtx).Run(*roundGateMatchID, *roundGateRoundNo, *roundGatePlanGameCount, *roundGateRoleSpecs, *roundGateChatRoomID); err != nil {
			logx.Error(err)
			os.Exit(1)
		}
		return
	}

	t := ticker.NewTicker(5 * time.Second)
	defer t.Stop()
	t.AddJob(func(ctx context.Context) error {
		logc.Infof(ctx, "starting match scan")
		defer logc.Infof(ctx, "match scan finished")
		return logic.NewMatchScanLogic(ctx, svcCtx).MatchScan()
	})

	logx.Info("starting match-controller")
	t.Start()
}
