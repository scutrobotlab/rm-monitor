package utils

import (
	"context"
	"encoding/json"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

const chatCacheKey = "rm-monitor:lark:joined-chat-ids"

var chatGroup singleflight.Group

func JoinedChatIDs(ctx context.Context, svcCtx *svc.ServiceContext) ([]string, error) {
	ids, err := cachedChatIDs(ctx, svcCtx)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	return fetchAndCacheChatIDs(ctx, svcCtx)
}

func cachedChatIDs(ctx context.Context, svcCtx *svc.ServiceContext) ([]string, error) {
	raw, err := svcCtx.RedisClient.GetCtx(ctx, chatCacheKey)
	if err != nil {
		return nil, errors.Wrap(err, "get joined chat cache")
	}
	if raw == "" {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, errors.Wrap(err, "decode joined chat cache")
	}
	return ids, nil
}

func fetchAndCacheChatIDs(ctx context.Context, svcCtx *svc.ServiceContext) ([]string, error) {
	v, err, _ := chatGroup.Do(chatCacheKey, func() (any, error) {
		return listChatIDs(ctx, svcCtx)
	})
	if err != nil {
		return nil, err
	}
	ids := v.([]string)
	b, err := json.Marshal(ids)
	if err != nil {
		return nil, errors.Wrap(err, "encode joined chat cache")
	}
	if err := svcCtx.RedisClient.SetexCtx(ctx, chatCacheKey, string(b), 5*60); err != nil {
		return nil, errors.Wrap(err, "set joined chat cache")
	}
	return ids, nil
}

func listChatIDs(ctx context.Context, svcCtx *svc.ServiceContext) ([]string, error) {
	nextPageToken := ""
	ids := make([]string, 0)

	for {
		if err := svcCtx.RateLimiter.Wait(ctx, ""); err != nil {
			return nil, err
		}
		resp, err := svcCtx.LarkClient.Im.Chat.List(ctx, larkim.NewListChatReqBuilder().PageSize(20).PageToken(nextPageToken).Build())
		if err != nil {
			return nil, errors.Wrap(err, "failed to list chats")
		}
		if !resp.Success() {
			return nil, errors.Wrapf(resp, "failed to list chats")
		}

		for _, chat := range resp.Data.Items {
			if chat.ChatId != nil && *chat.ChatId != "" {
				ids = append(ids, *chat.ChatId)
			}
		}

		if resp.Data.HasMore == nil || !*resp.Data.HasMore {
			break
		}

		nextPageToken = *resp.Data.PageToken
	}

	return ids, nil
}

func MatchCardUUID(matchID, chatID string) string {
	return fmt.Sprintf("rm-monitor:match-card:%s:%s", matchID, chatID)
}
