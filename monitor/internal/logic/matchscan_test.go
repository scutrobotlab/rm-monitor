package logic

import (
	"context"
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
	"scutbot.cn/web/rm-monitor/monitor/internal/config"
	"scutbot.cn/web/rm-monitor/monitor/internal/svc"
)

func TestNewMatchScanLogic(t *testing.T) {
	var c config.Config
	conf.MustLoad("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	err := NewMatchScanLogic(context.TODO(), svcCtx).MatchScan()
	if err != nil {
		t.Errorf("MatchScan failed: %v", err)
	} else {
		t.Log("MatchScan succeeded")
	}
}
