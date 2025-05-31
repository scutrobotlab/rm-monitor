package mqs

import (
	"context"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/jsonx"
	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

type MatchStartLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchStartLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.Match] {
	return &MatchStartLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *MatchStartLogic) Consume(key string, m types.Match) error {
	l.Infof("match start: %s", key)

	content, err := utils.NewMatchCardContent(l.ctx, l.svcCtx, &m)
	if err != nil {
		return errors.Wrap(err, "failed to create match card content")
	}

	contentData, err := jsonx.MarshalToString(content)
	if err != nil {
		l.Errorf("failed to marshal content: %v", err)
		return errors.Wrap(err, "failed to marshal content")
	}

	messageIds := make(map[string]string)
	err = utils.ForeachChat(l.ctx, l.svcCtx, func(chat *larkim.ListChat) {
		l.Debugf("Sending match %s start message to chat %s(%s)", m.Id, *chat.Name, *chat.ChatId)
		req := larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(*chat.ChatId).
				MsgType(larkim.MsgTypeInteractive).
				Content(contentData).
				// Uuid(key + ":" + *chat.ChatId).
				Build()).
			Build()

		resp, err := l.svcCtx.LarkClient.Im.V1.Message.Create(l.ctx, req)
		if err != nil {
			l.Error(errors.Wrapf(err, "failed to create message %+v", req))
			return
		}

		if !resp.Success() {
			l.Error(errors.Wrapf(resp, "failed to create message %+v", req))
			return
		}

		messageIds[*chat.ChatId] = *resp.Data.MessageId
	})
	if err != nil {
		l.Errorf("failed to iterate chats: %v", err)
		return errors.Wrap(err, "failed to iterate chats")
	}

	if err = utils.SaveMatchMessageCard(l.ctx, l.svcCtx, m.Id, content); err != nil {
		return errors.Wrapf(err, "failed to save message card %s", contentData)
	}

	if err = utils.SaveMatchMessageIds(l.ctx, l.svcCtx, m.Id, messageIds); err != nil {
		return errors.Wrapf(err, "failed to save message ids %s", messageIds)
	}

	return nil
}
