package stttext

import (
	"strings"
	"testing"
)

func TestBuildPromptIncludesMatchContext(t *testing.T) {
	prompt := BuildPrompt(PromptData{
		Event:      "RMUC 2026超级对抗赛",
		Zone:       "东部赛区",
		MatchID:    "31002",
		MatchType:  "GROUP",
		Order:      15,
		RoundNo:    2,
		Role:       "主视角",
		RedSchool:  "东南大学",
		RedName:    "3SE",
		BlueSchool: "成都大学",
		BlueName:   "Ultra",
	})
	for _, want := range []string{"简体中文", "31002", "GROUP", "场次：15", "Round 2", "主视角", "红方：东南大学-3SE", "蓝方：成都大学-Ultra"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSimplifier(t *testing.T) {
	c, err := NewSimplifier()
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Simplify("比賽開始，對戰藍方")
	if err != nil {
		t.Fatal(err)
	}
	if got != "比赛开始，对战蓝方" {
		t.Fatalf("Simplify() = %q", got)
	}
}
