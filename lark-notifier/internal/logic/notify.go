package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"scutbot.cn/web/rm-monitor/monitor/types"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

type NotifyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewNotifyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NotifyLogic {
	return &NotifyLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *NotifyLogic) Sync() error {
	if err := l.ensureStartedMessages(); err != nil {
		return err
	}
	if err := l.patchChangedCards(); err != nil {
		return err
	}
	return l.replyCompletedUploads()
}

func (l *NotifyLogic) ensureStartedMessages() error {
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.StatusEQ(matchround.StatusSTARTED)).
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().WithBlueTeam().WithLarkMessages()
		}).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query started rounds")
	}

	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil {
			continue
		}
		pending, err := l.pendingMatchSendProgress(m.ID)
		if err != nil {
			return err
		}
		if len(m.Edges.LarkMessages) > 0 && pending == nil {
			continue
		}
		if err := l.createMatchMessages(m); err != nil {
			return err
		}
	}
	return nil
}

func (l *NotifyLogic) createMatchMessages(m *ent.Match) error {
	content, err := l.cardContent(m)
	if err != nil {
		return err
	}
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "marshal card content")
	}
	contentData := string(contentBytes)

	chatIDs, err := utils.JoinedChatIDs(l.ctx, l.svcCtx)
	if err != nil {
		return err
	}
	sent, err := l.pendingMatchSendProgress(m.ID)
	if err != nil {
		return err
	}
	if sent == nil {
		sent = map[string]bool{}
	}
	if err := l.savePendingMatchSendProgress(m.ID, sent); err != nil {
		return err
	}
	for _, chatID := range chatIDs {
		if sent[chatID] {
			continue
		}
		req := larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType(larkim.MsgTypeInteractive).
				Content(contentData).
				Uuid(utils.MatchCardUUID(m.ID, chatID)).
				Build()).
			Build()
		var resp *larkim.CreateMessageResp
		err := l.withLarkRetry(chatID, func() error {
			var callErr error
			resp, callErr = l.svcCtx.LarkClient.Im.V1.Message.Create(l.ctx, req)
			if callErr != nil {
				return callErr
			}
			if !resp.Success() {
				return resp
			}
			return nil
		})
		if err != nil {
			l.Error(errors.Wrap(err, "create lark message"))
			continue
		}
		sent[chatID] = true
		if err := l.savePendingMatchSendProgress(m.ID, sent); err != nil {
			l.Error(errors.Wrap(err, "save pending lark message progress"))
		}
		if _, err := l.svcCtx.DB.LarkMessage.Create().
			SetMatchID(m.ID).
			SetMessageID(*resp.Data.MessageId).
			SetCardPayload(toMap(content)).
			OnConflictColumns(larkmessage.FieldMessageID).
			UpdateNewValues().
			ID(l.ctx); err != nil {
			l.Error(errors.Wrap(err, "save lark message"))
		}
	}
	if len(sent) >= len(chatIDs) {
		if err := l.svcCtx.RedisClient.DelCtx(l.ctx, pendingMatchSendKey(m.ID)); err != nil {
			l.Error(errors.Wrap(err, "clear pending lark message progress"))
		}
	}
	return nil
}

func (l *NotifyLogic) patchChangedCards() error {
	messages, err := l.svcCtx.DB.LarkMessage.Query().
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().WithBlueTeam().WithRounds(func(q *ent.MatchRoundQuery) {
				q.Order(matchround.ByRoundNo())
			})
		}).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query lark messages")
	}
	grouped := lo.GroupBy(messages, func(item *ent.LarkMessage) string {
		if item.Edges.Match == nil {
			return ""
		}
		return item.Edges.Match.ID
	})
	for _, group := range grouped {
		if len(group) == 0 || group[0].Edges.Match == nil {
			continue
		}
		content, err := l.cardContent(group[0].Edges.Match)
		if err != nil {
			return err
		}
		contentMap := toMap(content)
		contentBytes, err := json.Marshal(content)
		if err != nil {
			return errors.Wrap(err, "marshal card content")
		}
		contentData := string(contentBytes)
		for _, message := range group {
			if sameJSON(message.CardPayload, contentMap) {
				continue
			}
			req := larkim.NewPatchMessageReqBuilder().MessageId(message.MessageID).
				Body(larkim.NewPatchMessageReqBodyBuilder().Content(contentData).Build()).
				Build()
			var resp *larkim.PatchMessageResp
			err := l.withLarkRetry("", func() error {
				var callErr error
				resp, callErr = l.svcCtx.LarkClient.Im.V1.Message.Patch(l.ctx, req)
				if callErr != nil {
					return callErr
				}
				if !resp.Success() {
					return resp
				}
				return nil
			})
			if err != nil {
				l.Error(errors.Wrap(err, "patch lark message"))
				continue
			}
			if err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).SetCardPayload(contentMap).Exec(l.ctx); err != nil {
				l.Error(errors.Wrap(err, "update lark card payload"))
			}
		}
	}
	return nil
}

func (l *NotifyLogic) replyCompletedUploads() error {
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.LarkRepliedAtIsNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithLarkMessages()
				})
			})
		}).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query completed uploads")
	}
	for _, task := range tasks {
		match := task.Edges.RecordTask.Edges.MatchRound.Edges.Match
		for _, message := range match.Edges.LarkMessages {
			if task.FileURL == nil {
				continue
			}
			req := larkim.NewReplyMessageReqBuilder().
				Body(larkim.NewReplyMessageReqBodyBuilder().
					Content(larkim.NewMessageTextBuilder().Text(*task.FileURL).Build()).
					MsgType(larkim.MsgTypeText).
					ReplyInThread(true).
					Uuid(fmt.Sprintf("rm-monitor:upload-reply:%d:%s", task.ID, message.MessageID)).
					Build()).
				MessageId(message.MessageID).
				Build()
			var resp *larkim.ReplyMessageResp
			err := l.withLarkRetry("", func() error {
				var callErr error
				resp, callErr = l.svcCtx.LarkClient.Im.V1.Message.Reply(l.ctx, req)
				if callErr != nil {
					return callErr
				}
				if !resp.Success() {
					return resp
				}
				return nil
			})
			if err != nil {
				l.Error(errors.Wrap(err, "reply upload url"))
				continue
			}
		}
		if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetLarkRepliedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark upload replied")
		}
	}
	return nil
}

func (l *NotifyLogic) withLarkRetry(chatID string, f func() error) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := l.svcCtx.RateLimiter.Wait(l.ctx, chatID); err != nil {
			return err
		}
		if err := f(); err != nil {
			last = err
			if attempt < 2 {
				select {
				case <-l.ctx.Done():
					return l.ctx.Err()
				case <-time.After(time.Duration(attempt+1) * time.Second):
				}
			}
			continue
		}
		return nil
	}
	return last
}

func (l *NotifyLogic) pendingMatchSendProgress(matchID string) (map[string]bool, error) {
	raw, err := l.svcCtx.RedisClient.GetCtx(l.ctx, pendingMatchSendKey(matchID))
	if err != nil {
		return nil, errors.Wrap(err, "get pending lark message progress")
	}
	if raw == "" {
		return nil, nil
	}
	var chatIDs []string
	if err := json.Unmarshal([]byte(raw), &chatIDs); err != nil {
		return nil, errors.Wrap(err, "decode pending lark message progress")
	}
	out := make(map[string]bool, len(chatIDs))
	for _, chatID := range chatIDs {
		out[chatID] = true
	}
	return out, nil
}

func (l *NotifyLogic) savePendingMatchSendProgress(matchID string, sent map[string]bool) error {
	chatIDs := make([]string, 0, len(sent))
	for chatID := range sent {
		chatIDs = append(chatIDs, chatID)
	}
	sort.Strings(chatIDs)
	b, err := json.Marshal(chatIDs)
	if err != nil {
		return errors.Wrap(err, "encode pending lark message progress")
	}
	return l.svcCtx.RedisClient.SetexCtx(l.ctx, pendingMatchSendKey(matchID), string(b), 60*60)
}

func pendingMatchSendKey(matchID string) string {
	return fmt.Sprintf("rm-monitor:lark:match-card-pending:%s", matchID)
}

func (l *NotifyLogic) cardContent(m *ent.Match) (*utils.MatchCardContent, error) {
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return nil, err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return nil, err
	}
	msg := &types.Match{
		Id:          m.ID,
		Order:       int64(m.Order),
		Status:      m.LatestStatus,
		TotalRounds: int64(m.TotalRounds),
		MatchType:   m.MatchType,
		ZoneName:    m.Zone,
		EventName:   m.Event,
		RedTeam: types.Team{
			Name:       red.Name,
			SchoolName: red.SchoolName,
			SchoolLogo: red.SchoolLogo,
		},
		BlueTeam: types.Team{
			Name:       blue.Name,
			SchoolName: blue.SchoolName,
			SchoolLogo: blue.SchoolLogo,
		},
	}
	if m.MatchSlug != nil {
		msg.MatchSlug = *m.MatchSlug
	}
	content, err := utils.NewMatchCardContent(l.ctx, l.svcCtx, msg)
	if err != nil {
		return nil, err
	}
	rounds := m.Edges.Rounds
	for _, r := range rounds {
		if r.Status != matchround.StatusENDED || r.Winner == nil {
			continue
		}
		switch *r.Winner {
		case matchround.WinnerRed:
			msg.RedWinGameCount++
		case matchround.WinnerBlue:
			msg.BlueWinGameCount++
		}
		content.Data.TemplateVariable.Scores = append(content.Data.TemplateVariable.Scores, utils.MatchScore{
			RedScore:  fmt.Sprintf("%d", msg.RedWinGameCount),
			BlueScore: fmt.Sprintf("%d", msg.BlueWinGameCount),
		})
	}
	if len(rounds) > 0 && lo.EveryBy(rounds, func(item *ent.MatchRound) bool {
		return item.Status == matchround.StatusENDED
	}) {
		content.Data.TemplateVariable.MatchProgress = "结束"
		content.Data.TemplateVariable.Color = "green"
	}
	content.Data.TemplateVariable.Scores = lo.Uniq(content.Data.TemplateVariable.Scores)
	return content, nil
}

func toMap(v any) map[string]any {
	var out map[string]any
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &out)
	return out
}

func sameJSON(a, b map[string]any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
