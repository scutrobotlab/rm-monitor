package utils

import (
	"context"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

func ForeachChat(ctx context.Context, svcCtx *svc.ServiceContext, f func(*larkim.ListChat)) error {
	nextPageToken := ""

	for {
		resp, err := svcCtx.LarkClient.Im.Chat.List(ctx, larkim.NewListChatReqBuilder().PageSize(20).PageToken(nextPageToken).Build())
		if err != nil {
			return errors.Wrap(err, "failed to list chats")
		}
		if !resp.Success() {
			return errors.Wrapf(resp, "failed to list chats")
		}

		for _, chat := range resp.Data.Items {
			f(chat)
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore {
			break
		}

		nextPageToken = *resp.Data.PageToken
	}

	return nil
}
