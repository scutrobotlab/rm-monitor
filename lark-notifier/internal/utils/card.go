package utils

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
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
			RedAvatar     string       `json:"red_avatar,omitempty"`
			BlueAvatar    string       `json:"blue_avatar,omitempty"`
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
