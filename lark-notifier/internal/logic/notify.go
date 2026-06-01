package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/larkbitablerecord"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
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
		matches, err := l.matchesForWindow(since, pageSize, offset)
		if err != nil {
			return err
		}
		for _, m := range matches {
			if l.ctx.Err() != nil {
				return nil
			}
			total++
			changed, err := l.syncMatchCard(m, chatIDs)
			if err != nil {
				if isContextDone(err) {
					return nil
				}
				return err
			}
			if changed {
				updated++
			}
		}
		if len(matches) < pageSize {
			break
		}
	}
	l.Infof("lark scan finished since=%s chats=%d matches=%d changed=%d", since.Format(time.RFC3339Nano), len(chatIDs), total, updated)
	return nil
}

func (l *NotifyLogic) matchesForWindow(since time.Time, limit, offset int) ([]*ent.Match, error) {
	matches, err := l.svcCtx.DB.Match.Query().
		Where(match.Or(
			match.LatestStatusNEQ("DONE"),
			match.UpdatedAtGTE(since),
			match.HasRoundsWith(matchround.UpdatedAtGTE(since)),
			match.HasRoundsWith(matchround.HasLarkBitableRecordsWith(larkbitablerecord.UpdatedAtGTE(since))),
			match.HasRoundsWith(matchround.HasHighlightClipsWith(highlightclip.UpdatedAtGTE(since))),
		)).
		Order(match.ByUpdatedAt(), match.ByID()).
		WithRedTeam().
		WithBlueTeam().
		WithLarkMessages().
		WithRounds(func(q *ent.MatchRoundQuery) {
			q.Order(matchround.ByRoundNo()).
				WithLarkBitableRecords().
				WithHighlightClips(func(q *ent.HighlightClipQuery) {
					q.Where(highlightclip.StatusEQ(highlightclip.StatusAVAILABLE)).
						Order(highlightclip.ByHighlightIndex())
				})
		}).
		Limit(limit).
		Offset(offset).
		All(l.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "query lark scan matches")
	}
	return matches, nil
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

func ApplyMatchUpdate(ctx context.Context, svcCtx *svc.ServiceContext, matchID string) (bool, error) {
	chatIDs, err := utils.JoinedChatIDs(ctx, svcCtx)
	if err != nil {
		return false, err
	}
	m, err := queryMatchForCard(ctx, svcCtx, matchID)
	if err != nil {
		return false, err
	}
	return NewNotifyLogic(ctx, svcCtx).syncMatchCard(m, chatIDs)
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
					q.Where(highlightclip.StatusEQ(highlightclip.StatusAVAILABLE)).
						Order(highlightclip.ByHighlightIndex())
				})
		}).
		Only(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "query match for lark card")
	}
	return m, nil
}

func (l *NotifyLogic) syncMatchCard(m *ent.Match, chatIDs []string) (bool, error) {
	if m == nil || !matchShouldHaveCard(m) {
		return false, nil
	}
	before := cardPayloadFingerprint(m.Edges.LarkMessages)
	if err := l.ensureMatchMessages(m, chatIDs); err != nil {
		return false, err
	}
	if err := l.patchMatchCards(m); err != nil {
		return false, err
	}
	after := cardPayloadFingerprint(m.Edges.LarkMessages)
	return before != after, nil
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
		cardID, payload, err := utils.CreateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, content)
		if err != nil {
			l.Error(errors.Wrapf(err, "create card entity for existing lark message match=%s message_id=%s", m.ID, message.MessageID))
			continue
		}
		if err := utils.PatchCardReferenceMessage(l.ctx, l.svcCtx.LarkClient, l.retryLark, message.MessageID, cardID); err != nil {
			l.Error(errors.Wrapf(err, "bind existing lark message to card entity match=%s message_id=%s card_id=%s", m.ID, message.MessageID, cardID))
			continue
		}
		if err := l.svcCtx.DB.LarkMessage.UpdateOneID(message.ID).SetCardID(cardID).SetCardPayload(payload).Exec(l.ctx); err != nil {
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

func (l *NotifyLogic) patchMatchCards(m *ent.Match) error {
	if m == nil {
		return nil
	}
	payload, err := buildCardPayloadForMatch(l.ctx, l.svcCtx, m)
	if err != nil {
		return err
	}
	content := payload.Content
	contentMap := payload.Map
	dataUpdatedAt := cardDataUpdatedAt(m)
	sequence := dataUpdatedAt.Unix()
	attempted := 0
	updated := 0
	skipped := 0
	failed := 0
	for _, card := range m.Edges.LarkMessages {
		if !cardIDReady(card) {
			skipped++
			l.Infof("skip lark card update match=%s message=%s reason=card_not_ready", m.ID, card.MessageID)
			continue
		}
		if reflect.DeepEqual(card.CardPayload, contentMap) && !dataUpdatedAt.After(card.UpdatedAt) {
			skipped++
			l.Infof("skip lark card update match=%s message=%s card=%s reason=payload_unchanged data_updated_at=%s lark_updated_at=%s", m.ID, card.MessageID, *card.CardID, dataUpdatedAt.Format(time.RFC3339Nano), card.UpdatedAt.Format(time.RFC3339Nano))
			continue
		}
		attempted++
		l.Infof("attempt lark card update match=%s message=%s card=%s sequence=%d data_updated_at=%s lark_updated_at=%s forced=%t", m.ID, card.MessageID, *card.CardID, sequence, dataUpdatedAt.Format(time.RFC3339Nano), card.UpdatedAt.Format(time.RFC3339Nano), dataUpdatedAt.After(card.UpdatedAt))
		payload, err := utils.UpdateCardEntity(l.ctx, l.svcCtx.LarkClient, l.retryLark, *card.CardID, utils.MatchCardUpdateUUID(m.ID, *card.CardID, sequence), sequence, content)
		if err != nil {
			if utils.IsCardUpdateAlreadyApplied(err) {
				if err := l.svcCtx.DB.LarkMessage.UpdateOneID(card.ID).SetCardPayload(contentMap).Exec(l.ctx); err != nil {
					l.Error(errors.Wrap(err, "update lark card payload after idempotent card update"))
				}
				card.CardPayload = contentMap
				updated++
				l.Infof("lark card update already applied match=%s message=%s card=%s", m.ID, card.MessageID, *card.CardID)
				continue
			}
			failed++
			l.Error(errors.Wrap(err, "update lark card entity"))
			continue
		}
		if err := l.svcCtx.DB.LarkMessage.UpdateOneID(card.ID).SetCardPayload(payload).Exec(l.ctx); err != nil {
			failed++
			l.Error(errors.Wrap(err, "update lark card payload"))
			continue
		}
		card.CardPayload = payload
		updated++
		l.Infof("updated lark card match=%s message=%s card=%s", m.ID, card.MessageID, *card.CardID)
	}
	l.Infof("lark card patch finished match=%s attempted=%d updated=%d skipped=%d failed=%d", m.ID, attempted, updated, skipped, failed)
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
		for _, record := range r.Edges.LarkBitableRecords {
			if record.UpdatedAt.After(updatedAt) {
				updatedAt = record.UpdatedAt
			}
		}
		for _, clip := range r.Edges.HighlightClips {
			if clip.UpdatedAt.After(updatedAt) {
				updatedAt = clip.UpdatedAt
			}
		}
	}
	return updatedAt
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
		content.Data.MatchProgress = ""
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
	if r == nil {
		return ""
	}
	roundDir := l.roundDirFromRecords(r)
	if roundDir == "" {
		return ""
	}
	if !roundSettlementConfirmed(filepath.Join(roundDir, "round.json")) {
		return ""
	}
	imagePath := filepath.Join(roundDir, "settlement.jpg")
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
