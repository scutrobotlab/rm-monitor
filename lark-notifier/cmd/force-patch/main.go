package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	"scutbot.cn/web/rm-monitor/ent/match"
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

type result struct {
	MatchID string `json:"match_id"`
	Changed bool   `json:"changed"`
}

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "lark-notifier-force-patch", Mode: "console", Encoding: "plain"})
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

	ctx := context.Background()

	cleared, err := svcCtx.DB.LarkMessage.Update().
		Where(larkmessage.HasMatchWith(
			match.ID(*matchID),
		)).
		SetCardPayload(map[string]any{}).
		Save(ctx)
	if err != nil {
		logx.Error(errors.Wrap(err, "clear lark card payloads"))
		os.Exit(1)
	}
	logx.Infof("cleared card payload for %d lark messages", cleared)

	changed, err := logic.ApplyMatchUpdate(ctx, svcCtx, *matchID)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result{MatchID: *matchID, Changed: changed}); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}
