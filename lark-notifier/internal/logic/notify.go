package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"scutbot.cn/web/rm-monitor/ent"
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
		if isContextDone(err) {
			return nil
		}
		return err
	}
	if err := l.replyCompletedUploads(); err != nil {
		if isContextDone(err) {
			return nil
		}
		return err
	}
	return nil
}

func (l *NotifyLogic) SyncEvent(channel, payload string) error {
	switch channel {
	case "match_round_changed":
		id, err := strconv.Atoi(payload)
		if err != nil {
			return errors.Wrapf(err, "parse notify payload %q", payload)
		}
		return l.syncMatchRound(id)
	case "match_changed":
		return l.patchMatchCardsByID(payload)
	case "upload_task_changed":
		id, err := strconv.Atoi(payload)
		if err != nil {
			return errors.Wrapf(err, "parse notify payload %q", payload)
		}
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
				WithLarkMessages(func(q *ent.LarkMessageQuery) {
					q.WithCardMessages()
				}).
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
	if r.Status == matchround.StatusSTARTED && matchNeedsCardSend(m) {
		if err := l.createMatchMessages(m); err != nil {
			return err
		}
	}
	return l.patchMatchCardsByID(m.ID)
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
			q.WithRedTeam().WithBlueTeam().WithLarkMessages(func(q *ent.LarkMessageQuery) {
				q.WithCardMessages()
			})
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query started rounds")
	}

	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil || !matchNeedsCardSend(m) {
			continue
		}
		if err := l.createMatchMessages(m); err != nil {
			return err
		}
	}
	return nil
}

func (l *NotifyLogic) createMatchMessages(m *ent.Match) error {
	chatIDs, err := utils.JoinedChatIDs(l.ctx, l.svcCtx)
	if err != nil {
		return err
	}
	if len(chatIDs) == 0 {
		return nil
	}
	if len(m.Edges.LarkMessages) > 0 {
		card := m.Edges.LarkMessages[0]
		if len(card.Edges.CardMessages) > 0 {
			return nil
		}
		return l.sendCardReferencesToChats(m, card, chatIDs)
	}
	card, err := l.ensureMatchCard(m)
	if err != nil {
		return err
	}
	return l.sendCardReferencesToChats(m, card, chatIDs)
}

func (l *NotifyLogic) ensureMatchCard(m *ent.Match) (*ent.LarkMessage, error) {
	content, err := l.cardContent(m)
	if err != nil {
		return nil, err
	}
	contentData, err := cardEntityData(content)
	if err != nil {
		return nil, err
	}
	var resp *larkcardkit.CreateCardResp
	err = l.withLarkRetry("", func() error {
		var callErr error
		resp, callErr = l.svcCtx.LarkClient.Cardkit.V1.Card.Create(l.ctx, larkcardkit.NewCreateCardReqBuilder().
			Body(larkcardkit.NewCreateCardReqBodyBuilder().
				Type("card_json").
				Data(contentData).
				Build()).
			Build())
		if callErr != nil {
			return callErr
		}
		if !resp.Success() {
			return resp
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "create cardkit card")
	}
	if resp.Data == nil || resp.Data.CardId == nil || *resp.Data.CardId == "" {
		return nil, errors.New("create cardkit card returned empty card_id")
	}
	card, err := l.svcCtx.DB.LarkMessage.Create().
		SetMatchID(m.ID).
		SetCardID(*resp.Data.CardId).
		SetCardPayload(toMap(content)).
		Save(l.ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return nil, errors.Wrap(err, "save lark card")
	}
	if ent.IsConstraintError(err) {
		current, queryErr := l.svcCtx.DB.Match.Query().
			Where(match.ID(m.ID)).
			WithLarkMessages(func(q *ent.LarkMessageQuery) {
				q.WithCardMessages()
			}).
			Only(l.ctx)
		if queryErr != nil {
			return nil, errors.Wrap(queryErr, "query existing lark card after conflict")
		}
		if len(current.Edges.LarkMessages) == 0 {
			return nil, errors.Wrap(err, "save lark card conflict without existing card")
		}
		card = current.Edges.LarkMessages[0]
	}
	return card, nil
}

func (l *NotifyLogic) sendCardReferencesToChats(m *ent.Match, card *ent.LarkMessage, chatIDs []string) error {
	if strings.HasPrefix(card.CardID, "legacy:") {
		return nil
	}
	contentData, err := cardMessageContent(card.CardID)
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
		if resp.Data == nil || resp.Data.MessageId == nil || *resp.Data.MessageId == "" {
			l.Error(errors.New("create lark message returned empty message_id"))
			continue
		}
		if err := l.saveCardMessage(card.ID, *resp.Data.MessageId); err != nil {
			l.Error(errors.Wrap(err, "save lark card message"))
		}
	}
	return nil
}

func (l *NotifyLogic) saveCardMessage(cardID int, messageID string) error {
	_, err := l.svcCtx.DB.LarkCardMessage.Create().
		SetCardID(cardID).
		SetMessageID(messageID).
		Save(l.ctx)
	if ent.IsConstraintError(err) {
		return nil
	}
	return err
}

func (l *NotifyLogic) patchChangedCardsSince(since time.Time) error {
	seen := map[string]struct{}{}
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.UpdatedAtGTE(since)).
		WithMatch().
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query recently changed rounds")
	}
	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil {
			continue
		}
		seen[m.ID] = struct{}{}
	}
	matches, err := l.svcCtx.DB.Match.Query().
		Where(match.UpdatedAtGTE(since)).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query recently changed matches")
	}
	for _, m := range matches {
		seen[m.ID] = struct{}{}
	}
	for id := range seen {
		if l.ctx.Err() != nil {
			return nil
		}
		if err := l.patchMatchCardsByID(id); err != nil {
			if isContextDone(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func isContextDone(err error) bool {
	return errors.Cause(err) == context.Canceled || errors.Cause(err) == context.DeadlineExceeded
}

func (l *NotifyLogic) patchMatchCardsByID(matchID string) error {
	m, err := l.matchForPatch(matchID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	return l.patchMatchCards(m)
}

func (l *NotifyLogic) matchForPatch(matchID string) (*ent.Match, error) {
	return l.svcCtx.DB.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		WithLarkMessages(func(q *ent.LarkMessageQuery) {
			q.WithCardMessages()
		}).
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
	contentData, err := cardEntityData(content)
	if err != nil {
		return err
	}
	dataUpdatedAt := cardDataUpdatedAt(m)
	for _, card := range m.Edges.LarkMessages {
		if strings.HasPrefix(card.CardID, "legacy:") {
			continue
		}
		if !card.UpdatedAt.Before(dataUpdatedAt) {
			continue
		}
		sequence := time.Now().Unix()
		req := larkcardkit.NewUpdateCardReqBuilder().
			CardId(card.CardID).
			Body(larkcardkit.NewUpdateCardReqBodyBuilder().
				Card(larkcardkit.NewCardBuilder().
					Type("card_json").
					Data(contentData).
					Build()).
				Uuid(utils.MatchCardUpdateUUID(m.ID, card.CardID, sequence)).
				Sequence(int(sequence)).
				Build()).
			Build()
		var resp *larkcardkit.UpdateCardResp
		err := l.withLarkRetry("", func() error {
			var callErr error
			resp, callErr = l.svcCtx.LarkClient.Cardkit.V1.Card.Update(l.ctx, req)
			if callErr != nil {
				return callErr
			}
			if !resp.Success() {
				return resp
			}
			return nil
		})
		if err != nil {
			l.Error(errors.Wrap(err, "update cardkit card"))
			continue
		}
		if err := l.svcCtx.DB.LarkMessage.UpdateOneID(card.ID).SetCardPayload(contentMap).Exec(l.ctx); err != nil {
			l.Error(errors.Wrap(err, "update lark card payload"))
			continue
		}
	}
	return nil
}

func cardDataUpdatedAt(m *ent.Match) time.Time {
	if m == nil {
		return time.Time{}
	}
	updatedAt := m.UpdatedAt
	for _, r := range m.Edges.Rounds {
		if r.UpdatedAt.After(updatedAt) {
			updatedAt = r.UpdatedAt
		}
	}
	return updatedAt
}

func (l *NotifyLogic) replyCompletedUploads() error {
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.LarkRepliedAtIsNil(), uploadtask.BitableRecordURLNotNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithLarkMessages(func(q *ent.LarkMessageQuery) {
						q.WithCardMessages()
					})
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
					q.WithLarkMessages(func(q *ent.LarkMessageQuery) {
						q.WithCardMessages()
					})
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
	messageIDs := larkMessageIDs(match.Edges.LarkMessages)
	if len(messageIDs) == 0 {
		return nil
	}
	replyContent, err := uploadReplyContent(task)
	if err != nil {
		return err
	}
	done := 0
	for _, messageID := range messageIDs {
		req := larkim.NewReplyMessageReqBuilder().
			Body(larkim.NewReplyMessageReqBodyBuilder().
				Content(replyContent).
				MsgType(larkim.MsgTypePost).
				ReplyInThread(true).
				Uuid(utils.UploadReplyUUID(task.ID, messageID)).
				Build()).
			MessageId(messageID).
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
			if isTerminalReplyError(err) {
				l.Infof("skip upload reply for unreachable message: task=%d message_id=%s", task.ID, messageID)
				done++
				continue
			}
			l.Error(errors.Wrap(err, "reply upload url"))
			continue
		}
		done++
	}
	if done != len(messageIDs) {
		return nil
	}
	if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetLarkRepliedAt(time.Now()).Exec(l.ctx); err != nil {
		return errors.Wrap(err, "mark upload replied")
	}
	return nil
}

func isTerminalReplyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "code:230002") || strings.Contains(msg, "Bot/User can NOT be out of the chat")
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
		Result:      string(m.Result),
		WinnerText:  cardWinnerText(m, red, blue),
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
	if m.Report != nil {
		msg.Report = *m.Report
	}
	if m.WinnerPlaceholderName != nil {
		msg.WinnerPlacehold = *m.WinnerPlaceholderName
	}
	if m.LoserPlaceholderName != nil {
		msg.LoserPlacehold = *m.LoserPlaceholderName
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
		content.Data.Scores = append(content.Data.Scores, utils.MatchScore{
			RedScore:  fmt.Sprintf("%d", msg.RedWinGameCount),
			BlueScore: fmt.Sprintf("%d", msg.BlueWinGameCount),
		})
	}
	if matchCardCompleted(m) {
		content.Data.MatchProgress = ""
		content.Data.Color = completedCardColor(m.Result)
	}
	content.Data.Scores = lo.Uniq(content.Data.Scores)
	return content, nil
}

func matchNeedsCardSend(m *ent.Match) bool {
	if m == nil || len(m.Edges.LarkMessages) == 0 {
		return true
	}
	for _, card := range m.Edges.LarkMessages {
		if len(card.Edges.CardMessages) > 0 {
			return false
		}
	}
	return true
}

func cardEntityData(content *utils.MatchCardContent) (string, error) {
	return content.RenderJSON()
}

func cardMessageContent(cardID string) (string, error) {
	b, err := json.Marshal(map[string]any{
		"type": "card",
		"data": map[string]any{
			"card_id": cardID,
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal cardkit message content")
	}
	return string(b), nil
}

func larkMessageIDs(cards []*ent.LarkMessage) []string {
	ids := make([]string, 0)
	for _, card := range cards {
		for _, msg := range card.Edges.CardMessages {
			if msg.MessageID != "" {
				ids = append(ids, msg.MessageID)
			}
		}
	}
	return ids
}

func completedCardColor(result match.Result) string {
	switch result {
	case match.ResultRED:
		return "red"
	case match.ResultBLUE:
		return "wathet"
	case match.ResultDRAW:
		return "yellow"
	default:
		return "yellow"
	}
}

func cardWinnerText(m *ent.Match, red, blue *ent.Team) string {
	if m == nil {
		return ""
	}
	switch m.Result {
	case match.ResultRED:
		return "红方（" + displayTeamName(red) + "）"
	case match.ResultBLUE:
		return "蓝方（" + displayTeamName(blue) + "）"
	case match.ResultDRAW:
		return "平局"
	default:
		return ""
	}
}

func displayTeamName(t *ent.Team) string {
	if t == nil {
		return ""
	}
	switch {
	case t.SchoolName != "" && t.Name != "":
		return t.SchoolName + "-" + t.Name
	case t.SchoolName != "":
		return t.SchoolName
	default:
		return t.Name
	}
}

func matchCardCompleted(m *ent.Match) bool {
	if m == nil || m.LatestStatus != "DONE" || len(m.Edges.Rounds) == 0 {
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
