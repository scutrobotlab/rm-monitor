package utils

import (
	"context"
	"os"
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

func TestGetFolderToken(t *testing.T) {
	if os.Getenv("RUN_LARK_INTEGRATION") != "1" {
		t.Skip("set RUN_LARK_INTEGRATION=1 to run Feishu integration test")
	}

	var c config.Config
	conf.MustLoad("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	token, err := GetFolderToken(context.Background(), svcCtx, c.RecordConf.RootNode, "dnjgf/dhs/fh")
	if err != nil {
		t.Errorf("GetFolderToken failed: %v", err)
	} else {
		t.Logf("GetFolderToken succeeded: %s", token)
	}
}
