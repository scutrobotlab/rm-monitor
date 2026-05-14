package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	"scutbot.cn/web/rm-monitor/ent/match"
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

func (l *NotifyLogic) Sync(since time.Time) error {
	if err := l.ensureStartedMessages(); err != nil {
		return err
	}
	if err := l.patchChangedCardsSince(since); err != nil {
		return err
	}
	return l.replyCompletedUploads()
}

func (l *NotifyLogic) SyncEvent(channel, payload string) error {
	id, err := strconv.Atoi(payload)
	if err != nil {
		return errors.Wrapf(err, "parse notify payload %q", payload)
	}
	switch channel {
	case "match_round_changed":
		return l.syncMatchRound(id)
	case "upload_task_changed":
		return l.syncUploadTask(id)
	default:
		return nil
	}
}

func (l *NotifyLogic) syncMatchRound(id int) error {
	r, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.ID(id)).
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().
				WithBlueTeam().
				WithLarkMessages().
				WithRounds(func(q *ent.MatchRoundQuery) {
					q.Order(matchround.ByRoundNo())
				})
		}).
		Only(l.ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "query notified match round")
	}
	m := r.Edges.Match
	if m == nil {
		return nil
	}
	if r.Status == matchround.StatusSTARTED && len(m.Edges.LarkMessages) == 0 {
		if err := l.createMatchMessages(m); err != nil {
			return err
		}
		m, err = l.matchForPatch(m.ID)
		if err != nil {
			return err
		}
	}
	return l.patchMatchCards(m)
}

func (l *NotifyLogic) syncUploadTask(id int) error {
	task, err := l.uploadTaskForReply(id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	return l.replyUploadTask(task)
}

func (l *NotifyLogic) ensureStartedMessages() error {
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.StatusEQ(matchround.StatusSTARTED)).
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().WithBlueTeam().WithLarkMessages()
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query started rounds")
	}

	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil || len(m.Edges.LarkMessages) > 0 {
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
	for _, chatID := range chatIDs {
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
	return nil
}

func (l *NotifyLogic) patchChangedCardsSince(since time.Time) error {
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.UpdatedAtGTE(since)).
		WithMatch(func(q *ent.MatchQuery) {
			q.WithRedTeam().
				WithBlueTeam().
				WithLarkMessages().
				WithRounds(func(q *ent.MatchRoundQuery) {
					q.Order(matchround.ByRoundNo())
				})
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query recently changed rounds")
	}
	seen := make(map[string]struct{}, len(rounds))
	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil {
			continue
		}
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		if err := l.patchMatchCards(m); err != nil {
			return err
		}
	}
	return nil
}

func (l *NotifyLogic) matchForPatch(matchID string) (*ent.Match, error) {
	return l.svcCtx.DB.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		WithLarkMessages().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo())
		}).
		Only(l.ctx)
}

func (l *NotifyLogic) patchMatchCards(m *ent.Match) error {
	if m == nil {
		return nil
	}
	content, err := l.cardContent(m)
	if err != nil {
		return err
	}
	contentMap := toMap(content)
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "marshal card content")
	}
	contentData := string(contentBytes)
	for _, message := range m.Edges.LarkMessages {
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
	if matchCardCompleted(m) {
		l.pushResultWebhooks(m.ID, content)
	}
	return nil
}

func (l *NotifyLogic) replyCompletedUploads() error {
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.LarkRepliedAtIsNil(), uploadtask.BitableRecordURLNotNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithLarkMessages()
				})
			})
		}).Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query completed uploads")
	}
	for _, task := range tasks {
		if err := l.replyUploadTask(task); err != nil {
			return err
		}
	}
	return nil
}

func (l *NotifyLogic) uploadTaskForReply(id int) (*ent.UploadTask, error) {
	return l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.ID(id), uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.LarkRepliedAtIsNil(), uploadtask.BitableRecordURLNotNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithLarkMessages()
				})
			})
		}).
		Only(l.ctx)
}

func (l *NotifyLogic) replyUploadTask(task *ent.UploadTask) error {
	if task == nil || task.BitableRecordURL == nil || task.Edges.RecordTask == nil || task.Edges.RecordTask.Edges.MatchRound == nil || task.Edges.RecordTask.Edges.MatchRound.Edges.Match == nil {
		return nil
	}
	match := task.Edges.RecordTask.Edges.MatchRound.Edges.Match
	if len(match.Edges.LarkMessages) == 0 {
		return nil
	}
	replyContent, err := uploadReplyContent(task)
	if err != nil {
		return err
	}
	replied := 0
	for _, message := range match.Edges.LarkMessages {
		req := larkim.NewReplyMessageReqBuilder().
			Body(larkim.NewReplyMessageReqBodyBuilder().
				Content(replyContent).
				MsgType(larkim.MsgTypePost).
				ReplyInThread(true).
				Uuid(utils.UploadReplyUUID(task.ID, message.MessageID)).
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
		replied++
	}
	if replied != len(match.Edges.LarkMessages) {
		return nil
	}
	if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetLarkRepliedAt(time.Now()).Exec(l.ctx); err != nil {
		return errors.Wrap(err, "mark upload replied")
	}
	return nil
}

func uploadReplyContent(task *ent.UploadTask) (string, error) {
	round := task.Edges.RecordTask.Edges.MatchRound
	title := fmt.Sprintf("Round%d-%s", round.RoundNo, task.Edges.RecordTask.Role)
	content := map[string]any{
		"zh_cn": map[string]any{
			"title": title,
			"content": [][]map[string]string{
				{
					{
						"tag":  "text",
						"text": *task.BitableRecordURL,
					},
				},
			},
		},
	}
	b, err := json.Marshal(content)
	if err != nil {
		return "", errors.Wrap(err, "marshal upload reply content")
	}
	return string(b), nil
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
				wait := retryDelay(attempt)
				select {
				case <-l.ctx.Done():
					return l.ctx.Err()
				case <-time.After(wait):
				}
			}
			continue
		}
		return nil
	}
	return last
}

func retryDelay(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
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
	if matchCardCompleted(m) {
		content.Data.TemplateVariable.MatchProgress = "结束"
		content.Data.TemplateVariable.Color = "green"
	}
	content.Data.TemplateVariable.Scores = lo.Uniq(content.Data.TemplateVariable.Scores)
	return content, nil
}

func matchCardCompleted(m *ent.Match) bool {
	if m == nil || len(m.Edges.Rounds) == 0 {
		return false
	}
	return lo.EveryBy(m.Edges.Rounds, func(item *ent.MatchRound) bool {
		return item.Status == matchround.StatusENDED
	})
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
