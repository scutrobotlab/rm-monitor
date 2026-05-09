package utils

import (
	"context"
	"os"
	"testing"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
)

func TestGetFolderToken(t *testing.T) {
	if os.Getenv("RUN_LARK_INTEGRATION") != "1" {
		t.Skip("set RUN_LARK_INTEGRATION=1 to run Feishu integration test")
	}

	var c config.Config
	app.MustLoadConfig("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	token, err := GetFolderToken(context.Background(), svcCtx, c.RecordConf.RootNode, "dnjgf/dhs/fh")
	if err != nil {
		t.Errorf("GetFolderToken failed: %v", err)
	} else {
		t.Logf("GetFolderToken succeeded: %s", token)
	}
}
