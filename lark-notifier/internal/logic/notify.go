package logic

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

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
	if err := l.patchCardsForUploadsSince(since); err != nil {
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
		return l.syncUploadTaskCard(id)
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
	if r.Status == matchround.StatusSTARTED && matchNeedsCardSend(m) {
		if err := l.createMatchMessages(m); err != nil {
			return err
		}
	}
	return l.patchMatchCardsByID(m.ID)
}

func (l *NotifyLogic) syncUploadTaskCard(id int) error {
	task, err := l.uploadTaskForCard(id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	if task.Edges.RecordTask == nil || task.Edges.RecordTask.Edges.MatchRound == nil || task.Edges.RecordTask.Edges.MatchRound.Edges.Match == nil {
		return nil
	}
	return l.patchMatchCardsByID(task.Edges.RecordTask.Edges.MatchRound.Edges.Match.ID)
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
		return nil
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
	cardID, payload, err := utils.CreateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, content)
	if err != nil {
		return nil, err
	}
	card, err := l.svcCtx.DB.LarkMessage.Create().
		SetMatchID(m.ID).
		SetCardID(cardID).
		SetCardPayload(payload).
		Save(l.ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return nil, errors.Wrap(err, "save lark card")
	}
	if ent.IsConstraintError(err) {
		current, queryErr := l.svcCtx.DB.Match.Query().
			Where(match.ID(m.ID)).
			WithLarkMessages().
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
	for _, chatID := range chatIDs {
		if err := utils.SendCardReferenceMessage(l.ctx, l.svcCtx.LarkClient, l.retryLark, chatID, card.CardID, utils.MatchCardUUID(m.ID, chatID)); err != nil {
			l.Error(errors.Wrap(err, "create lark message"))
			continue
		}
	}
	return nil
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
		WithLarkMessages().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo()).
				WithRecordTasks(func(q *ent.RecordTaskQuery) {
					q.WithUploadTask()
				})
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
	contentMap := utils.ToMap(content)
	for _, card := range m.Edges.LarkMessages {
		if strings.HasPrefix(card.CardID, "legacy:") {
			continue
		}
		if reflect.DeepEqual(card.CardPayload, contentMap) {
			continue
		}
		sequence := time.Now().Unix()
		payload, err := utils.UpdateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, card.CardID, utils.MatchCardUpdateUUID(m.ID, card.CardID, sequence), sequence, content)
		if err != nil {
			l.Error(errors.Wrap(err, "update cardkit card"))
			continue
		}
		if err := l.svcCtx.DB.LarkMessage.UpdateOneID(card.ID).SetCardPayload(payload).Exec(l.ctx); err != nil {
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

func (l *NotifyLogic) patchCardsForUploadsSince(since time.Time) error {
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.UpdatedAtGTE(since), uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.BitableRecordURLNotNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch()
			})
		}).Limit(200).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query completed uploads")
	}
	seen := map[string]struct{}{}
	for _, task := range tasks {
		if task.Edges.RecordTask == nil || task.Edges.RecordTask.Edges.MatchRound == nil || task.Edges.RecordTask.Edges.MatchRound.Edges.Match == nil {
			continue
		}
		seen[task.Edges.RecordTask.Edges.MatchRound.Edges.Match.ID] = struct{}{}
	}
	for id := range seen {
		if err := l.patchMatchCardsByID(id); err != nil {
			if isContextDone(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (l *NotifyLogic) uploadTaskForCard(id int) (*ent.UploadTask, error) {
	return l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.ID(id), uploadtask.StatusEQ(uploadtask.StatusSUCCEEDED), uploadtask.BitableRecordURLNotNil()).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch()
			})
		}).
		Only(l.ctx)
}

func (l *NotifyLogic) retryLark(chatID string, f func() error) error {
	return l.svcCtx.RetryLark(l.ctx, chatID, f)
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
	content.Data.Rounds = l.roundCards(m)
	if matchCardCompleted(m) {
		content.Data.MatchProgress = ""
		content.Data.Color = completedCardColor(m.Result)
	}
	return content, nil
}

func matchNeedsCardSend(m *ent.Match) bool {
	return m == nil || len(m.Edges.LarkMessages) == 0
}

func cardEntityData(content *utils.MatchCardContent) (string, error) {
	data, _, err := utils.CardEntityData(content)
	return data, err
}

func cardMessageContent(cardID string) (string, error) {
	return utils.CardReferenceMessageContent(cardID)
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

func (l *NotifyLogic) roundCards(m *ent.Match) []utils.MatchRoundCard {
	if m == nil {
		return nil
	}
	redWins := 0
	blueWins := 0
	cards := make([]utils.MatchRoundCard, 0, len(m.Edges.Rounds))
	for _, r := range m.Edges.Rounds {
		if r.Status == matchround.StatusENDED && r.Winner != nil {
			switch *r.Winner {
			case matchround.WinnerRed:
				redWins++
			case matchround.WinnerBlue:
				blueWins++
			case matchround.WinnerDraw:
			}
		}
		cards = append(cards, utils.MatchRoundCard{
			PanelID:   fmt.Sprintf("elem_round_%d", r.RoundNo),
			ContentID: fmt.Sprintf("elem_round_%d_content", r.RoundNo),
			Title:     roundScoreTitle(redWins, blueWins),
			Content:   roundRecordLinks(r),
		})
	}
	return cards
}

func roundScoreTitle(redWins, blueWins int) string {
	return fmt.Sprintf("<font color=red>**%d**</font> : <font color=blue>**%d** </font>", redWins, blueWins)
}

func roundRecordLinks(r *ent.MatchRound) string {
	if r == nil {
		return "暂无录制"
	}
	tasks := append([]*ent.RecordTask(nil), r.Edges.RecordTasks...)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Role < tasks[j].Role
	})
	links := make([]string, 0)
	for _, task := range tasks {
		if task.Edges.UploadTask == nil || task.Edges.UploadTask.BitableRecordURL == nil || *task.Edges.UploadTask.BitableRecordURL == "" {
			continue
		}
		links = append(links, fmt.Sprintf("[%s](%s)", task.Role, *task.Edges.UploadTask.BitableRecordURL))
	}
	if len(links) == 0 {
		return "暂无录制"
	}
	return strings.Join(links, "\n")
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
