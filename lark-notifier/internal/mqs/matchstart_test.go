package mqs

import (
	"context"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/zeromicro/go-zero/core/conf"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"testing"
)

func TestGroups(t *testing.T) {
	var c config.Config
	conf.MustLoad("../../etc/config.yml", &c)

	svcCtx := svc.NewServiceContext(c)

	err := utils.ForeachChat(context.Background(), svcCtx, func(chat *larkim.ListChat) {
		t.Logf("Chat ID: %s, Name: %s", *chat.ChatId, *chat.Name)
	})
	if err != nil {
		t.Errorf("ForeachChat failed: %v", err)
	} else {
		t.Log("ForeachChat succeeded")
	}
}
