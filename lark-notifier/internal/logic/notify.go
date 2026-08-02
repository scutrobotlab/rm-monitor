package logic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/larkbitablerecord"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/team"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"scutbot.cn/web/rm-monitor/match-controller/types"
	"scutbot.cn/web/rm-monitor/pkg/highlight"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

type NotifyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

type CardPayload struct {
	Content *utils.MatchCardContent
	Bytes   []byte
	Map     map[string]any
}

type UpdateResult struct {
	MatchID string             `json:"match_id"`
	Chats   []ChatUpdateResult `json:"chats"`
}

type ChatUpdateResult struct {
	ChatID   string `json:"chat_id"`
	Action   string `json:"action"`
	Sequence int64  `json:"sequence"`
	Error    string `json:"error,omitempty"`
}

func (r UpdateResult) HasFailures() bool {
	return lo.SomeBy(r.Chats, func(item ChatUpdateResult) bool { return item.Action == "failed" })
}

func NewNotifyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NotifyLogic {
	return &NotifyLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *NotifyLogic) SyncWindow(since time.Time) error {
	const pageSize = 100
	chatIDs, err := utils.JoinedChatIDs(l.ctx, l.svcCtx)
	if err != nil {
		return err
	}
	if len(chatIDs) == 0 {
		l.Infof("lark scan skipped since=%s reason=no_joined_chats", since.Format(time.RFC3339Nano))
		return nil
	}
	total := 0
	updated := 0
	for offset := 0; ; offset += pageSize {
		matchIDs, err := l.matchesForWindow(since, pageSize, offset)
		if err != nil {
			return err
		}
		for _, matchID := range matchIDs {
			if l.ctx.Err() != nil {
				return nil
			}
			total++
			result, err := ApplyMatchUpdate(l.ctx, l.svcCtx, matchID)
			if err != nil {
				if isContextDone(err) {
					return nil
				}
				l.Errorf("lark match update failed match=%s result=%+v err=%v", matchID, result, err)
				continue
			}
			if lo.SomeBy(result.Chats, func(item ChatUpdateResult) bool { return item.Action == "created" || item.Action == "updated" }) {
				updated++
			}
		}
		if len(matchIDs) < pageSize {
			break
		}
	}
	l.Infof("lark scan finished since=%s chats=%d matches=%d changed=%d", since.Format(time.RFC3339Nano), len(chatIDs), total, updated)
	return nil
}

func (l *NotifyLogic) matchesForWindow(since time.Time, limit, offset int) ([]string, error) {
	ids, err := l.svcCtx.DB.Match.Query().
		Where(match.Or(
			match.UpdatedAtGTE(since),
			match.HasRedTeamWith(team.UpdatedAtGTE(since)),
			match.HasBlueTeamWith(team.UpdatedAtGTE(since)),
			match.HasRoundsWith(matchround.UpdatedAtGTE(since)),
			match.HasRoundsWith(matchround.HasLarkBitableRecordsWith(larkbitablerecord.UpdatedAtGTE(since))),
			match.HasRoundsWith(matchround.HasHighlightClipsWith(highlightclip.UpdatedAtGTE(since))),
		)).
		Order(match.ByUpdatedAt(), match.ByID()).
		Limit(limit).
		Offset(offset).
		IDs(l.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "query lark scan matches")
	}
	return ids, nil
}

func CreateCardPayload(ctx context.Context, svcCtx *svc.ServiceContext, matchID string) ([]byte, error) {
	payload, err := BuildCardPayload(ctx, svcCtx, matchID)
	if err != nil {
		return nil, err
	}
	return payload.Bytes, nil
}

func BuildCardPayload(ctx context.Context, svcCtx *svc.ServiceContext, matchID string) (*CardPayload, error) {
	m, err := queryMatchForCard(ctx, svcCtx, matchID)
	if err != nil {
		return nil, err
	}
	return buildCardPayloadForMatch(ctx, svcCtx, m)
}

func ApplyMatchUpdate(ctx context.Context, svcCtx *svc.ServiceContext, matchID string) (UpdateResult, error) {
	result := UpdateResult{MatchID: matchID}
	chatIDs, err := utils.JoinedChatIDs(ctx, svcCtx)
	if err != nil {
		return result, err
	}
	m, err := queryMatchForCard(ctx, svcCtx, matchID)
	if err != nil {
		return result, err
	}
	if !matchShouldHaveCard(m) {
		return result, nil
	}
	payload, err := buildCardPayloadForMatch(ctx, svcCtx, m)
	if err != nil {
		return result, err
	}
	logic := NewNotifyLogic(ctx, svcCtx)
	for _, chatID := range chatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		item := logic.applyChatUpdate(m.ID, chatID, payload)
		result.Chats = append(result.Chats, item)
	}
	if result.HasFailures() {
		return result, errors.Errorf("lark match update failed for one or more chats")
	}
	return result, nil
}

func buildCardPayloadForMatch(ctx context.Context, svcCtx *svc.ServiceContext, m *ent.Match) (*CardPayload, error) {
	content, err := NewNotifyLogic(ctx, svcCtx).cardContent(m)
	if err != nil {
		return nil, err
	}
	raw, payloadMap, err := utils.CardEntityData(content)
	if err != nil {
		return nil, err
	}
	return &CardPayload{
		Content: content,
		Bytes:   []byte(raw),
		Map:     payloadMap,
	}, nil
}

func queryMatchForCard(ctx context.Context, svcCtx *svc.ServiceContext, matchID string) (*ent.Match, error) {
	m, err := svcCtx.DB.Match.Query().
		Where(match.ID(matchID)).
		WithRedTeam().
		WithBlueTeam().
		WithLarkMessages().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo()).
				WithLarkBitableRecords().
				WithHighlightClips(func(q *ent.HighlightClipQuery) {
					q.Where(highlightclip.StatusEQ(highlightclip.StatusAVAILABLE), highlightclip.ArtifactReadyAtNotNil()).
						Order(highlightclip.ByHighlightIndex())
				})
		}).
		Only(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "query match for lark card")
	}
	return m, nil
}

func matchShouldHaveCard(m *ent.Match) bool {
	if m == nil {
		return false
	}
	if m.LatestStatus == types.MatchStatusSTARTED || m.LatestStatus == "DONE" {
		return true
	}
	return lo.SomeBy(m.Edges.Rounds, func(r *ent.MatchRound) bool {
		return r.Status == matchround.StatusSTARTED || r.Status == matchround.StatusENDED
	})
}

func (l *NotifyLogic) applyChatUpdate(matchID, chatID string, payload *CardPayload) ChatUpdateResult {
	result := ChatUpdateResult{ChatID: chatID}
	err := l.withCardLock(matchID, chatID, func() error {
		message, err := l.svcCtx.DB.LarkMessage.Query().
			Where(larkmessage.ChatIDEQ(chatID), larkmessage.HasMatchWith(match.ID(matchID))).
			Only(l.ctx)
		if err != nil && !ent.IsNotFound(err) {
			return errors.Wrap(err, "query lark message for chat")
		}
		if ent.IsNotFound(err) {
			return l.createChatMessage(matchID, chatID, payload, &result)
		}
		return l.updateChatMessage(matchID, message, payload, &result)
	})
	if err != nil {
		result.Action = "failed"
		result.Error = err.Error()
		l.Error(errors.Wrapf(err, "apply lark chat update match=%s chat=%s", matchID, chatID))
	}
	return result
}

func (l *NotifyLogic) withCardLock(matchID, chatID string, fn func() error) error {
	tx, err := l.svcCtx.SQLDB.BeginTx(l.ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin lark advisory lock transaction")
	}
	defer func() { _ = tx.Rollback() }()
	sum := sha256.Sum256([]byte(matchID + "\x00" + chatID))
	key := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := tx.ExecContext(l.ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
		return errors.Wrap(err, "acquire lark advisory lock")
	}
	if err := fn(); err != nil {
		return err
	}
	return errors.Wrap(tx.Commit(), "release lark advisory lock")
}

func (l *NotifyLogic) createChatMessage(matchID, chatID string, payload *CardPayload, result *ChatUpdateResult) error {
	createdCardID, storedPayload, err := utils.CreateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, payload.Content)
	if err != nil {
		return err
	}
	messageID, err := utils.SendCardReferenceMessage(l.ctx, l.svcCtx.LarkClient, l.retryLark, chatID, createdCardID, utils.MatchCardUUID(matchID, chatID))
	if err != nil {
		return err
	}
	cardID, err := utils.ResolveCardEntityID(l.ctx, l.svcCtx.LarkClient, l.retryLark, messageID)
	if err != nil {
		return err
	}
	sequence := int64(0)
	if cardID != createdCardID {
		sequence = time.Now().Unix()
		hash := cardPayloadHash(payload.Bytes)
		uuid := utils.MatchCardUpdateUUID(matchID, cardID, sequence, hash)
		storedPayload, err = utils.UpdateCardEntityData(l.ctx, l.svcCtx.LarkClient, l.retryLark, cardID, uuid, sequence, string(payload.Bytes), payload.Map)
		if err != nil && !utils.IsCardUpdateAlreadyApplied(err) {
			return errors.Wrap(err, "converge recovered cardkit card")
		}
		storedPayload = storedPayloadOrDesired(storedPayload, payload.Map)
	}
	now := time.Now()
	created, err := l.svcCtx.DB.LarkMessage.Create().
		SetMatchID(matchID).
		SetMessageID(messageID).
		SetChatID(chatID).
		SetCardID(cardID).
		SetCardSequence(sequence).
		SetCardPayload(storedPayload).
		SetLastDeliveryAttemptAt(now).
		Save(l.ctx)
	if err != nil {
		if !ent.IsConstraintError(err) {
			return errors.Wrap(err, "save lark message")
		}
		existing, queryErr := l.svcCtx.DB.LarkMessage.Query().
			Where(larkmessage.ChatIDEQ(chatID), larkmessage.HasMatchWith(match.ID(matchID))).
			Only(l.ctx)
		if queryErr != nil {
			return errors.Wrap(queryErr, "query concurrently created lark message")
		}
		return l.updateChatMessage(matchID, existing, payload, result)
	}
	result.Action = "created"
	result.Sequence = created.CardSequence
	return nil
}

func (l *NotifyLogic) updateChatMessage(matchID string, message *ent.LarkMessage, payload *CardPayload, result *ChatUpdateResult) error {
	if message == nil {
		return errors.New("lark message is nil")
	}
	if message.CardID == nil || strings.TrimSpace(*message.CardID) == "" || strings.HasPrefix(message.MessageID, "legacy:") {
		if strings.HasPrefix(message.MessageID, "legacy:") {
			return l.recordDeliveryFailure(message.ID, errors.New("legacy lark message cannot resolve card_id without a real message_id"))
		}
		cardID, err := utils.ResolveCardEntityID(l.ctx, l.svcCtx.LarkClient, l.retryLark, message.MessageID)
		if err != nil {
			return l.recordDeliveryFailure(message.ID, err)
		}
		sequence := time.Now().Unix()
		hash := cardPayloadHash(payload.Bytes)
		uuid := utils.MatchCardUpdateUUID(matchID, cardID, sequence, hash)
		storedPayload, err := utils.UpdateCardEntityData(l.ctx, l.svcCtx.LarkClient, l.retryLark, cardID, uuid, sequence, string(payload.Bytes), payload.Map)
		if err != nil && !utils.IsCardUpdateAlreadyApplied(err) {
			return l.recordDeliveryFailure(message.ID, err)
		}
		updated, err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).
			SetCardID(cardID).
			SetCardSequence(sequence).
			SetCardPayload(storedPayloadOrDesired(storedPayload, payload.Map)).
			SetLastDeliveryAttemptAt(time.Now()).
			ClearLastDeliveryError().
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "save recovered lark card")
		}
		result.Action = "updated"
		result.Sequence = updated.CardSequence
		return nil
	}
	if equalCardPayload(message.CardPayload, payload.Map) {
		result.Action = "unchanged"
		result.Sequence = message.CardSequence
		return nil
	}
	reserved, err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).
		AddCardSequence(1).
		SetLastDeliveryAttemptAt(time.Now()).
		ClearLastDeliveryError().
		Save(l.ctx)
	if err != nil {
		return errors.Wrap(err, "reserve lark card sequence")
	}
	result.Sequence = reserved.CardSequence
	hash := cardPayloadHash(payload.Bytes)
	uuid := utils.MatchCardUpdateUUID(matchID, *message.CardID, reserved.CardSequence, hash)
	storedPayload, updateErr := utils.UpdateCardEntityData(l.ctx, l.svcCtx.LarkClient, l.retryLark, *message.CardID, uuid, reserved.CardSequence, string(payload.Bytes), payload.Map)
	if updateErr != nil && !utils.IsCardUpdateAlreadyApplied(updateErr) {
		return l.recordDeliveryFailure(message.ID, updateErr)
	}
	if _, err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).
		SetCardPayload(storedPayloadOrDesired(storedPayload, payload.Map)).
		SetLastDeliveryAttemptAt(time.Now()).
		ClearLastDeliveryError().
		Save(l.ctx); err != nil {
		return errors.Wrap(err, "save lark card payload")
	}
	result.Action = "updated"
	return nil
}

func (l *NotifyLogic) recordDeliveryFailure(messageID int, cause error) error {
	if messageID != 0 {
		_, err := l.svcCtx.DB.LarkMessage.UpdateOneID(messageID).
			SetLastDeliveryError(cause.Error()).
			SetLastDeliveryAttemptAt(time.Now()).
			Save(l.ctx)
		if err != nil {
			return errors.Wrapf(cause, "also failed to save delivery error: %v", err)
		}
	}
	return cause
}

func equalCardPayload(a, b map[string]any) bool {
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	return string(aRaw) == string(bRaw)
}

func cardPayloadHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:12])
}

func storedPayloadOrDesired(stored, desired map[string]any) map[string]any {
	if stored != nil {
		return stored
	}
	return desired
}

func cardPayloadFingerprint(messages []*ent.LarkMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		raw, _ := json.Marshal(message.CardPayload)
		cardID := ""
		if message.CardID != nil {
			cardID = *message.CardID
		}
		parts = append(parts, fmt.Sprintf("%s:%s:%s", message.MessageID, cardID, string(raw)))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func (l *NotifyLogic) ensureMatchMessages(m *ent.Match, chatIDs []string) error {
	if len(chatIDs) == 0 {
		return nil
	}
	legacyReady := 0
	readyByChatID := make(map[string]struct{}, len(chatIDs))
	for _, message := range m.Edges.LarkMessages {
		if !cardIDReady(message) {
			continue
		}
		if message.ChatID != nil && strings.TrimSpace(*message.ChatID) != "" {
			readyByChatID[strings.TrimSpace(*message.ChatID)] = struct{}{}
		} else {
			legacyReady++
		}
	}
	if len(readyByChatID) >= len(chatIDs) || legacyReady >= len(chatIDs) {
		return nil
	}
	content, err := l.cardContent(m)
	if err != nil {
		return err
	}
	if err := l.ensureStoredCardIDs(m, content); err != nil {
		return err
	}
	legacyReady = 0
	readyByChatID = make(map[string]struct{}, len(chatIDs))
	for _, message := range m.Edges.LarkMessages {
		if !cardIDReady(message) {
			continue
		}
		if message.ChatID != nil && strings.TrimSpace(*message.ChatID) != "" {
			readyByChatID[strings.TrimSpace(*message.ChatID)] = struct{}{}
		} else {
			legacyReady++
		}
	}
	if len(readyByChatID) >= len(chatIDs) || legacyReady >= len(chatIDs) {
		return nil
	}
	successes := 0
	failures := 0
	for _, chatID := range chatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		if _, ok := readyByChatID[chatID]; ok {
			continue
		}
		cardID, payload, err := utils.CreateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, content)
		if err != nil {
			failures++
			l.Error(errors.Wrapf(err, "create lark card entity match=%s chat=%s", m.ID, chatID))
			continue
		}
		messageID, err := utils.SendCardReferenceMessage(l.ctx, l.svcCtx.LarkClient, l.retryLark, chatID, cardID, utils.MatchCardUUID(m.ID, chatID))
		if err != nil {
			failures++
			l.Error(errors.Wrapf(err, "create lark message match=%s chat=%s", m.ID, chatID))
			continue
		}
		created, err := l.svcCtx.DB.LarkMessage.Create().
			SetMatchID(m.ID).
			SetMessageID(messageID).
			SetChatID(chatID).
			SetCardID(cardID).
			SetCardPayload(payload).
			Save(l.ctx)
		if err != nil && !ent.IsConstraintError(err) {
			failures++
			l.Error(errors.Wrapf(err, "save lark message match=%s chat=%s message_id=%s card_id=%s", m.ID, chatID, messageID, cardID))
			continue
		}
		if err == nil {
			m.Edges.LarkMessages = append(m.Edges.LarkMessages, created)
		}
		successes++
		readyByChatID[chatID] = struct{}{}
	}
	l.Infof("ensured lark match messages match=%s chats=%d existing_by_chat=%d legacy_ready=%d success=%d failure=%d", m.ID, len(chatIDs), len(readyByChatID), legacyReady, successes, failures)
	return nil
}

func (l *NotifyLogic) ensureStoredCardIDs(m *ent.Match, content *utils.MatchCardContent) error {
	if m == nil {
		return nil
	}
	for _, message := range m.Edges.LarkMessages {
		if message == nil || cardIDReady(message) || strings.HasPrefix(message.MessageID, "legacy:") {
			continue
		}
		cardID, err := utils.ResolveCardEntityID(l.ctx, l.svcCtx.LarkClient, l.retryLark, message.MessageID)
		if err != nil {
			l.Error(errors.Wrapf(err, "resolve card entity for existing lark message match=%s message_id=%s", m.ID, message.MessageID))
			continue
		}
		contentData, payload, err := utils.CardEntityData(content)
		if err != nil {
			return err
		}
		sequence := time.Now().Unix()
		hash := cardPayloadHash([]byte(contentData))
		uuid := utils.MatchCardUpdateUUID(m.ID, cardID, sequence, hash)
		storedPayload, err := utils.UpdateCardEntityData(l.ctx, l.svcCtx.LarkClient, l.retryLark, cardID, uuid, sequence, contentData, payload)
		if err != nil && !utils.IsCardUpdateAlreadyApplied(err) {
			l.Error(errors.Wrapf(err, "update resolved card entity match=%s message_id=%s card_id=%s", m.ID, message.MessageID, cardID))
			continue
		}
		if err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).SetCardID(cardID).SetCardSequence(sequence).SetCardPayload(storedPayloadOrDesired(storedPayload, payload)).Exec(l.ctx); err != nil {
			l.Error(errors.Wrapf(err, "save existing lark message card_id match=%s message_id=%s card_id=%s", m.ID, message.MessageID, cardID))
			continue
		}
		message.CardID = &cardID
		message.CardPayload = payload
	}
	return nil
}

func isContextDone(err error) bool {
	return errors.Cause(err) == context.Canceled || errors.Cause(err) == context.DeadlineExceeded
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
	content.Data.HighlightBullets, content.Data.HighlightImages = l.highlightPresentation(m)
	content.Data.HighlightMarkdown = highlightMarkdown(content.Data.HighlightBullets)
	content.Data.HighlightMode = highlightCombinationMode(len(content.Data.HighlightImages))
	if matchCardCompleted(m) {
		content.Data.MatchProgress = "已结束"
		content.Data.Color = completedCardColor(m.Result)
	}
	return content, nil
}

func cardIDReady(message *ent.LarkMessage) bool {
	return message != nil &&
		!strings.HasPrefix(message.MessageID, "legacy:") &&
		message.CardID != nil &&
		*message.CardID != ""
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
			PanelID:            fmt.Sprintf("elem_round_%d", r.RoundNo),
			ContentID:          fmt.Sprintf("elem_round_%d_content", r.RoundNo),
			Title:              roundScoreTitle(redWins, blueWins),
			Content:            roundRecordLinks(r),
			SettlementImageKey: l.roundSettlementImageKey(r),
		})
	}
	return cards
}

func (l *NotifyLogic) roundSettlementImageKey(r *ent.MatchRound) string {
	if r == nil || r.SettlementStatus != "CONFIRMED" || r.SettlementReadyAt == nil || r.SettlementImagePath == nil {
		return ""
	}
	baseDir := strings.TrimSpace(l.svcCtx.Config.RecordConf.BaseDir)
	if baseDir == "" {
		baseDir = "/records"
	}
	imagePath := filepath.Join(baseDir, filepath.FromSlash(*r.SettlementImagePath))
	if !fileExists(imagePath) {
		return ""
	}
	imageKey, err := utils.GetLocalImageKey(l.ctx, l.svcCtx, imagePath)
	if err != nil {
		l.Error(errors.Wrapf(err, "upload settlement image round=%d path=%s", r.ID, imagePath))
		return ""
	}
	return imageKey
}

func roundScoreTitle(redWins, blueWins int) string {
	return fmt.Sprintf("<font color=red>**%d**</font> : <font color=blue>**%d** </font>", redWins, blueWins)
}

func roundRecordLinks(r *ent.MatchRound) string {
	if r == nil {
		return "暂无录制"
	}
	records := append([]*ent.LarkBitableRecord(nil), r.Edges.LarkBitableRecords...)
	sort.Slice(records, func(i, j int) bool {
		return records[i].Role < records[j].Role
	})
	links := make([]string, 0)
	for _, record := range records {
		if record.RecordURL == nil || strings.TrimSpace(*record.RecordURL) == "" {
			continue
		}
		links = append(links, fmt.Sprintf(
			"<link icon='video_outlined' url='%s' pc_url='' ios_url='' android_url=''>%s</link>",
			html.EscapeString(strings.TrimSpace(*record.RecordURL)),
			html.EscapeString(record.Role),
		))
	}
	if len(links) == 0 {
		return "暂无录制"
	}
	return strings.Join(links, "\n")
}

func (l *NotifyLogic) roundDirFromRecords(r *ent.MatchRound) string {
	if r == nil {
		return ""
	}
	baseDir := "/records"
	if l.svcCtx == nil {
		baseDir = "/records"
	} else {
		baseDir = strings.TrimSpace(l.svcCtx.Config.RecordConf.BaseDir)
	}
	if baseDir == "" {
		baseDir = "/records"
	}
	for _, record := range r.Edges.LarkBitableRecords {
		if strings.TrimSpace(record.SourcePath) == "" {
			continue
		}
		return filepath.Dir(filepath.Join(baseDir, filepath.FromSlash(record.SourcePath)))
	}
	return ""
}

func roundSettlementConfirmed(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Settlement struct {
			Status string `json:"status"`
		} `json:"settlement"`
	}
	return json.Unmarshal(raw, &doc) == nil && doc.Settlement.Status == "CONFIRMED"
}

func (l *NotifyLogic) highlightPresentation(m *ent.Match) ([]utils.HighlightBullet, []utils.HighlightImage) {
	if m == nil {
		return nil, nil
	}
	baseDir := strings.TrimSpace(l.svcCtx.Config.RecordConf.BaseDir)
	if baseDir == "" {
		baseDir = "/records"
	}
	highlightConf := l.svcCtx.Config.HighlightConf.WithDefaults()
	if !highlightConf.Enabled {
		return nil, nil
	}
	clips := selectedHighlightClips(m, 2, 9, highlightConf.Role, highlightConf.AlgorithmVersion, baseDir)
	bullets := make([]utils.HighlightBullet, 0, len(clips))
	images := make([]utils.HighlightImage, 0, len(clips))
	seenImageKeys := make(map[string]struct{}, len(clips))
	for _, selected := range clips {
		clip := selected.clip
		path := filepath.Join(baseDir, filepath.FromSlash(clip.OutputDir), "preview.gif")
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				l.Infof("skip missing highlight preview match=%s clip=%d path=%s", m.ID, clip.ID, path)
				continue
			}
			l.Error(errors.Wrapf(err, "stat highlight preview match=%s clip=%d path=%s", m.ID, clip.ID, path))
			continue
		}
		imageKey, err := utils.GetLocalImageKey(l.ctx, l.svcCtx, path)
		if err != nil {
			l.Error(errors.Wrapf(err, "upload highlight preview match=%s clip=%d path=%s", m.ID, clip.ID, path))
			continue
		}
		if _, ok := seenImageKeys[imageKey]; ok {
			l.Infof("skip duplicate highlight preview image_key match=%s clip=%d path=%s", m.ID, clip.ID, path)
			continue
		}
		seenImageKeys[imageKey] = struct{}{}
		title := fmt.Sprintf("Round %d Highlight %02d", selected.roundNo, clip.HighlightIndex)
		if clip.Title != nil && strings.TrimSpace(*clip.Title) != "" {
			title = strings.TrimSpace(*clip.Title)
		}
		caption := ""
		if clip.Description != nil {
			caption = strings.TrimSpace(*clip.Description)
		}
		publishCaption := highlightPublishCaption(clip)
		bullets = append(bullets, utils.HighlightBullet{
			RoundNo:        selected.roundNo,
			Title:          title,
			Caption:        caption,
			PublishCaption: publishCaption,
		})
		images = append(images, utils.HighlightImage{
			ImageKey: imageKey,
			Title:    title,
			Alt:      fmt.Sprintf("Round %d %s", selected.roundNo, title),
		})
	}
	return bullets, images
}

type selectedHighlightClip struct {
	clip    *ent.HighlightClip
	roundNo int
}

func selectedHighlightClips(m *ent.Match, perRoundLimit, totalLimit int, role, algorithmVersion, previewBaseDir string) []selectedHighlightClip {
	if m == nil || perRoundLimit <= 0 || totalLimit <= 0 {
		return nil
	}
	role = strings.TrimSpace(role)
	algorithmVersion = strings.TrimSpace(algorithmVersion)
	selected := make([]selectedHighlightClip, 0)
	candidates := make([]highlight.FeaturedCandidate, 0)
	for _, r := range m.Edges.Rounds {
		for _, clip := range r.Edges.HighlightClips {
			if clip.Status != highlightclip.StatusAVAILABLE {
				continue
			}
			if role != "" && clip.Role != role {
				continue
			}
			if algorithmVersion != "" && clip.AlgorithmVersion != algorithmVersion {
				continue
			}
			outputDir := strings.TrimSpace(clip.OutputDir)
			if outputDir == "" {
				continue
			}
			if previewBaseDir != "" && !fileExists(filepath.Join(previewBaseDir, filepath.FromSlash(outputDir), "preview.gif")) {
				continue
			}
			selected = append(selected, selectedHighlightClip{clip: clip, roundNo: r.RoundNo})
			candidates = append(candidates, highlight.FeaturedCandidate{
				RoundNo:        r.RoundNo,
				HighlightIndex: clip.HighlightIndex,
				Score:          clip.Score,
				Key:            outputDir,
			})
		}
	}
	indexes := highlight.SelectFeatured(candidates, perRoundLimit, totalLimit)
	out := make([]selectedHighlightClip, 0, len(indexes))
	for _, idx := range indexes {
		out = append(out, selected[idx])
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func highlightPublishCaption(clip *ent.HighlightClip) string {
	if clip == nil || clip.ModelPayload == nil || strings.TrimSpace(*clip.ModelPayload) == "" {
		return ""
	}
	var payload struct {
		Review struct {
			PublishCaption string `json:"publish_caption"`
		} `json:"review"`
	}
	if err := json.Unmarshal([]byte(*clip.ModelPayload), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Review.PublishCaption)
}

func highlightMarkdown(bullets []utils.HighlightBullet) string {
	if len(bullets) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**精选高光**")
	for _, item := range bullets {
		b.WriteString(fmt.Sprintf("\n- **Round %d %s**", item.RoundNo, item.Title))
		if strings.TrimSpace(item.Caption) != "" {
			b.WriteString("：" + strings.TrimSpace(item.Caption))
		}
		if strings.TrimSpace(item.PublishCaption) != "" {
			b.WriteString("\n  " + strings.TrimSpace(item.PublishCaption))
		}
	}
	return b.String()
}

func highlightCombinationMode(n int) string {
	switch {
	case n <= 2:
		return "double"
	case n == 3:
		return "triple"
	case n >= 7:
		return "trisect"
	default:
		return "bisect"
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
