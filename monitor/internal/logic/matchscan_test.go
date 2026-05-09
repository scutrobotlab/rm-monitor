package logic

import (
	"context"
	"os"
	"testing"

	"scutbot.cn/web/rm-monitor/monitor/internal/config"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
)

func TestNewMatchScanLogic(t *testing.T) {
	if os.Getenv("RM_MONITOR_INTEGRATION") == "" {
		t.Skip("set RM_MONITOR_INTEGRATION=1 to run live monitor scan")
	}
	var c config.Config
	app.MustLoadConfig("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	err := NewMatchScanLogic(context.TODO(), svcCtx).MatchScan()
	if err != nil {
		t.Errorf("MatchScan failed: %v", err)
	} else {
		t.Log("MatchScan succeeded")
	}
}

func TestWinnersFromDelta(t *testing.T) {
	prev := processedSnapshot{Status: "STARTED", RedWinGameCount: 0, BlueWinGameCount: 0}
	cur := scannedMatch{RedWinGameCount: 2, BlueWinGameCount: 1}
	got := winnersFromDelta(prev, cur, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 winners, got %d", len(got))
	}
	if got[0] != "red" || got[1] != "red" || got[2] != "blue" {
		t.Fatalf("unexpected winners: %#v", got)
	}
}
