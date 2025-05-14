package mqs

import (
	"context"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/zeromicro/go-zero/core/jsonx"
	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

type MatchNewRoundLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMatchNewRoundLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.Match] {
	return &MatchNewRoundLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *MatchNewRoundLogic) Consume(key string, m types.Match) error {
	l.Infof("match new round: %s", key)

	content, err := utils.GetMatchMessageCard(l.ctx, l.svcCtx, m.Id)
	if err != nil {
		return errors.Wrapf(err, "failed to get message card %s", m.Id)
	}

	content.Data.TemplateVariable.Scores = append(content.Data.TemplateVariable.Scores, utils.MatchScore{
		RedScore: fmt.Sprintf("%d", m.RedWinGameCount), BlueScore: fmt.Sprintf("%d", m.BlueWinGameCount),
	})
	content.Data.TemplateVariable.Scores = lo.Uniq(content.Data.TemplateVariable.Scores)

	contentData, err := jsonx.MarshalToString(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal content")
	}

	err = utils.ForeachChat(l.ctx, l.svcCtx, func(chat *larkim.ListChat) {
		l.Debugf("Sending match %s new round message to chat %s(%s)", m.Id, *chat.Name, *chat.ChatId)
		messageId, err := utils.GetMatchMessageId(l.ctx, l.svcCtx, *chat.ChatId, m.Id)
		if err != nil && key != "" {
			l.Errorf("failed to get message id, rerunning: %v", err)
			return
		}

		req := larkim.NewPatchMessageReqBuilder().MessageId(messageId).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(contentData).
				Build()).
			Build()

		resp, err := l.svcCtx.LarkClient.Im.V1.Message.Patch(l.ctx, req)
		if err != nil {
			l.Errorf("failed to update message: %v", err)
			return
		}

		if !resp.Success() {
			l.Error(errors.Wrapf(resp, "failed to update message %+v", req))
			return
		}
	})
	if err != nil {
		l.Errorf("failed to iterate chats: %v", err)
		return errors.Wrap(err, "failed to iterate chats")
	}

	if err = utils.SaveMatchMessageCard(l.ctx, l.svcCtx, m.Id, content); err != nil {
		return errors.Wrapf(err, "failed to save message card %s", contentData)
	}

	return nil
}
