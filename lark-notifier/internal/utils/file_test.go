package utils

import (
	"context"
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

func TestGetFolderToken(t *testing.T) {
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

func TestUploadFile(t *testing.T) {
}
