package utils

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/jsonx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

const matchCardId = "AAqkpd7LuaV0s"

type MatchScore struct {
	RedScore  string `json:"red_score"`
	BlueScore string `json:"blue_score"`
}

type MatchCardContent struct {
	Type string `json:"type"`
	Data struct {
		TemplateId       string `json:"template_id"`
		TemplateVariable struct {
			RedTeam       string       `json:"red_team"`
			BlueTeam      string       `json:"blue_team"`
			MatchProgress string       `json:"match_progress"`
			MatchIndex    string       `json:"match_index"`
			TotalRound    string       `json:"total_round"`
			MatchId       string       `json:"match_id"`
			EventTitle    string       `json:"event_title"`
			RedSchool     string       `json:"red_school"`
			BlueSchool    string       `json:"blue_school"`
			RedAvatar     string       `json:"red_avatar"`
			BlueAvatar    string       `json:"blue_avatar"`
			Scores        []MatchScore `json:"scores"`
			Color         string       `json:"color"`
			MatchType     string       `json:"match_type"`
			ZoneTitle     string       `json:"zone_title"`
		} `json:"template_variable"`
	} `json:"data"`
}

func NewMatchCardContent(ctx context.Context, svcCtx *svc.ServiceContext, m *types.Match) (*MatchCardContent, error) {
	var content MatchCardContent
	var err error
	content.Type = "template"
	content.Data.TemplateId = matchCardId
	content.Data.TemplateVariable.RedTeam = m.RedTeam.Name
	content.Data.TemplateVariable.BlueTeam = m.BlueTeam.Name
	content.Data.TemplateVariable.MatchProgress = "进行中"
	content.Data.TemplateVariable.MatchIndex = fmt.Sprintf("%d", m.Order)
	content.Data.TemplateVariable.TotalRound = fmt.Sprintf("%d", m.TotalRounds)
	content.Data.TemplateVariable.MatchId = m.Id
	content.Data.TemplateVariable.EventTitle = m.EventName
	content.Data.TemplateVariable.RedSchool = m.RedTeam.SchoolName
	content.Data.TemplateVariable.BlueSchool = m.BlueTeam.SchoolName
	content.Data.TemplateVariable.RedAvatar, err = GetImageKey(ctx, svcCtx, m.RedTeam.SchoolLogo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get red avatar")
	}
	content.Data.TemplateVariable.BlueAvatar, err = GetImageKey(ctx, svcCtx, m.BlueTeam.SchoolLogo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get blue avatar")
	}
	content.Data.TemplateVariable.Scores = []MatchScore{
		{"0", "0"},
	}
	content.Data.TemplateVariable.Color = "blue"
	content.Data.TemplateVariable.MatchType = m.MatchType
	content.Data.TemplateVariable.ZoneTitle = m.ZoneName
	if m.MatchSlug != "" {
		content.Data.TemplateVariable.MatchType = m.MatchSlug
	}

	return &content, nil
}

func SaveMatchMessageCard(ctx context.Context, svcCtx *svc.ServiceContext, matchId string, content *MatchCardContent) error {
	contentData, err := jsonx.MarshalToString(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal content")
	}

	messagePayloadKey := fmt.Sprintf("rm_monitor:message_payload:%s", matchId)
	if err := svcCtx.RedisClient.SetexCtx(ctx, messagePayloadKey, contentData, 6*60*60); err != nil {
		return errors.Wrapf(err, "failed to set message payload key %s", messagePayloadKey)
	}

	return nil
}

func GetMatchMessageCard(ctx context.Context, svcCtx *svc.ServiceContext, matchId string) (*MatchCardContent, error) {
	messagePayloadKey := fmt.Sprintf("rm_monitor:message_payload:%s", matchId)
	contentData, err := svcCtx.RedisClient.GetCtx(ctx, messagePayloadKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get message payload key %s", messagePayloadKey)
	}

	if contentData == "" {
		return nil, errors.New("message payload not found")
	}

	var content MatchCardContent
	if err := jsonx.UnmarshalFromString(contentData, &content); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal content")
	}

	return &content, nil
}

func SaveMatchMessageId(ctx context.Context, svcCtx *svc.ServiceContext, chatId string, matchId string, messageId string) error {
	messageKey := fmt.Sprintf("rm_monitor:message_id:%s:%s", chatId, matchId)
	if err := svcCtx.RedisClient.SetexCtx(ctx, messageKey, messageId, 6*60*60); err != nil {
		return errors.Wrapf(err, "failed to set message key %s", messageKey)
	}

	return nil
}

func GetMatchMessageId(ctx context.Context, svcCtx *svc.ServiceContext, chatId string, matchId string) (string, error) {
	messageKey := fmt.Sprintf("rm_monitor:message_id:%s:%s", chatId, matchId)
	messageId, err := svcCtx.RedisClient.GetCtx(ctx, messageKey)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get message key %s", messageKey)
	}

	if messageId == "" {
		return "", errors.New("message id not found")
	}

	return messageId, nil
}
