package utils

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

//go:embed card.json.tpl
var matchCardTemplateSource string

var matchCardTemplate = template.Must(template.New("match-card").
	Funcs(template.FuncMap{
		"json":     jsonLiteral,
		"jsonText": jsonStringContent,
	}).
	Parse(matchCardTemplateSource))

type MatchScore struct {
	RedScore  string `json:"red_score"`
	BlueScore string `json:"blue_score"`
}

type MatchCardData struct {
	RedTeam       string
	BlueTeam      string
	MatchProgress string
	MatchIndex    string
	TotalRound    string
	MatchID       string
	EventTitle    string
	RedSchool     string
	BlueSchool    string
	RedAvatar     string
	BlueAvatar    string
	Scores        []MatchScore
	Color         string
	MatchType     string
	ZoneTitle     string
	Report        string
	Result        string
	Winner        string
	WinnerPlace   string
	LoserPlace    string
}

type MatchCardContent struct {
	Data MatchCardData `json:"-"`
}

func NewMatchCardContent(ctx context.Context, svcCtx *svc.ServiceContext, m *types.Match) (*MatchCardContent, error) {
	var content MatchCardContent
	var err error
	content.Data.RedTeam = m.RedTeam.Name
	content.Data.BlueTeam = m.BlueTeam.Name
	content.Data.MatchProgress = "进行中"
	content.Data.MatchIndex = fmt.Sprintf("%d", m.Order)
	content.Data.TotalRound = fmt.Sprintf("%d", m.TotalRounds)
	content.Data.MatchID = m.Id
	content.Data.EventTitle = m.EventName
	content.Data.RedSchool = m.RedTeam.SchoolName
	content.Data.BlueSchool = m.BlueTeam.SchoolName
	content.Data.RedAvatar, err = GetImageKey(ctx, svcCtx, m.RedTeam.SchoolLogo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get red avatar")
	}
	content.Data.BlueAvatar, err = GetImageKey(ctx, svcCtx, m.BlueTeam.SchoolLogo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get blue avatar")
	}
	content.Data.Scores = []MatchScore{
		{"0", "0"},
	}
	content.Data.Color = "orange"
	content.Data.MatchType = m.MatchType
	content.Data.ZoneTitle = m.ZoneName
	content.Data.Report = m.Report
	content.Data.Result = m.Result
	content.Data.Winner = m.WinnerText
	content.Data.WinnerPlace = m.WinnerPlacehold
	content.Data.LoserPlace = m.LoserPlacehold
	if m.MatchSlug != "" {
		content.Data.MatchType = m.MatchSlug
	}

	return &content, nil
}

func (c *MatchCardContent) MarshalJSON() ([]byte, error) {
	raw, err := c.RenderJSON()
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func (c *MatchCardContent) RenderJSON() (string, error) {
	var buf bytes.Buffer
	if err := matchCardTemplate.Execute(&buf, c.Data); err != nil {
		return "", errors.Wrap(err, "render match card template")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, buf.Bytes()); err != nil {
		return "", errors.Wrap(err, "compact rendered match card json")
	}
	return compact.String(), nil
}

func jsonLiteral(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func jsonStringContent(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		s = string(b)
	}
	quoted := strconv.Quote(s)
	return strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`), nil
}
