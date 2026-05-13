package bitableupload

import (
	"fmt"
	"strings"

	"scutbot.cn/web/rm-monitor/ent"
)

const (
	FieldRole       = "视角"
	FieldMatch      = "场次"
	FieldRound      = "对局"
	FieldType       = "类型"
	FieldRedTeam    = "红方"
	FieldBlueTeam   = "蓝方"
	FieldAttachment = "录像"
)

func TableName(event, zone string) string {
	return strings.TrimSpace(fmt.Sprintf("%s-%s", event, zone))
}

func MatchName(m *ent.Match) string {
	if m == nil {
		return ""
	}
	red := teamName(m.Edges.RedTeam)
	blue := teamName(m.Edges.BlueTeam)
	return fmt.Sprintf("%d. %s VS %s", m.Order, red, blue)
}

func TeamName(t *ent.Team) string {
	return teamName(t)
}

func teamName(t *ent.Team) string {
	if t == nil {
		return ""
	}
	school := strings.TrimSpace(t.SchoolName)
	name := strings.TrimSpace(t.Name)
	switch {
	case school == "":
		return name
	case name == "":
		return school
	default:
		return school + "-" + name
	}
}

func RecordFields(m *ent.Match, roundNo int, role string) map[string]interface{} {
	return map[string]interface{}{
		FieldRole:     role,
		FieldMatch:    m.Order,
		FieldRound:    roundNo,
		FieldType:     m.MatchType,
		FieldRedTeam:  TeamName(m.Edges.RedTeam),
		FieldBlueTeam: TeamName(m.Edges.BlueTeam),
	}
}

func AttachmentValue(fileToken, name string) []map[string]interface{} {
	return []map[string]interface{}{{
		"file_token": fileToken,
		"name":       name,
	}}
}
