package stttext

import (
	"fmt"
	"strings"
)

const GenericPrompt = "请使用简体中文输出 RoboMaster 赛事解说转写，保留常见英文队名、数字、机器人编号和专有名词。"

type PromptData struct {
	Event      string
	Zone       string
	MatchID    string
	MatchType  string
	Order      int
	RoundNo    int
	Role       string
	RedSchool  string
	RedName    string
	BlueSchool string
	BlueName   string
}

func BuildPrompt(d PromptData) string {
	parts := []string{GenericPrompt}
	if strings.TrimSpace(d.Event) != "" {
		parts = append(parts, "赛事："+strings.TrimSpace(d.Event))
	}
	if strings.TrimSpace(d.Zone) != "" {
		parts = append(parts, "赛区："+strings.TrimSpace(d.Zone))
	}
	if strings.TrimSpace(d.MatchID) != "" {
		parts = append(parts, "Match ID："+strings.TrimSpace(d.MatchID))
	}
	if strings.TrimSpace(d.MatchType) != "" {
		parts = append(parts, "比赛类型："+strings.TrimSpace(d.MatchType))
	}
	if d.Order > 0 {
		parts = append(parts, fmt.Sprintf("场次：%d", d.Order))
	}
	if d.RoundNo > 0 {
		parts = append(parts, fmt.Sprintf("小局：Round %d", d.RoundNo))
	}
	if strings.TrimSpace(d.Role) != "" {
		parts = append(parts, "视角："+strings.TrimSpace(d.Role))
	}
	if red := teamText(d.RedSchool, d.RedName); red != "" {
		parts = append(parts, "红方："+red)
	}
	if blue := teamText(d.BlueSchool, d.BlueName); blue != "" {
		parts = append(parts, "蓝方："+blue)
	}
	return strings.Join(parts, "\n")
}

func teamText(school, name string) string {
	school = strings.TrimSpace(school)
	name = strings.TrimSpace(name)
	switch {
	case school != "" && name != "":
		return school + "-" + name
	case school != "":
		return school
	default:
		return name
	}
}
