package logic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
)

func TestMatchCardCompleted(t *testing.T) {
	if matchCardCompleted(&ent.Match{}) {
		t.Fatal("empty rounds should not be completed")
	}
	m := &ent.Match{LatestStatus: "STARTED", Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{Status: matchround.StatusENDED},
		{Status: matchround.StatusENDED},
	}}}
	if matchCardCompleted(m) {
		t.Fatal("all ended rounds should not be completed while match is still started")
	}
	m.LatestStatus = "DONE"
	if !matchCardCompleted(m) {
		t.Fatal("done match with all ended rounds should be completed")
	}
	m.Edges.Rounds = append(m.Edges.Rounds, &ent.MatchRound{Status: matchround.StatusSTARTED})
	if matchCardCompleted(m) {
		t.Fatal("started round should not be completed")
	}
}

func TestMatchCardCompletedRequiresDoneStatus(t *testing.T) {
	m := &ent.Match{LatestStatus: "WAITING", Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{Status: matchround.StatusENDED},
		{Status: matchround.StatusENDED},
	}}}
	if matchCardCompleted(m) {
		t.Fatal("non-DONE match should not be completed")
	}
}

func TestCardMessageContentReferencesCardID(t *testing.T) {
	raw, err := cardMessageContent("card_123")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "card" {
		t.Fatalf("type = %v, want card", got["type"])
	}
	data := got["data"].(map[string]any)
	if data["card_id"] != "card_123" {
		t.Fatalf("card_id = %v, want card_123", data["card_id"])
	}
}

func TestCardEntityDataRendersCardJSON(t *testing.T) {
	content := &utils.MatchCardContent{}
	content.Data.RedAvatar = "img_red"
	content.Data.BlueAvatar = "img_blue"
	content.Data.RedSchool = `红"校`
	content.Data.BlueSchool = "蓝校"
	content.Data.RedTeam = "红队"
	content.Data.BlueTeam = "蓝队"
	content.Data.Rounds = []utils.MatchRoundCard{{
		PanelID:   "elem_round_1",
		ContentID: "elem_round_1_content",
		Title:     "<font color=red>**1**</font> : <font color=blue>**0** </font>",
		Content:   "[主视角](https://example.com/record)",
	}}
	raw, err := cardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["schema"] != "2.0" {
		t.Fatalf("schema = %v, want 2.0", got["schema"])
	}
	if _, ok := got["card_link"]; ok {
		t.Fatal("card_link should be removed")
	}
	body := got["body"].(map[string]any)
	elements := body["elements"].([]any)
	if len(elements) == 0 {
		t.Fatal("rendered card has no body elements")
	}
	foundPanel := false
	for _, element := range elements {
		m := element.(map[string]any)
		if m["tag"] == "collapsible_panel" {
			foundPanel = true
			if m["element_id"] != "elem_round_1" {
				t.Fatalf("panel element_id = %v, want elem_round_1", m["element_id"])
			}
			if m["direction"] != "vertical" || m["horizontal_align"] != "center" || m["vertical_align"] != "center" {
				t.Fatalf("unexpected panel alignment: %#v", m)
			}
			if m["expanded"] != false || m["background_color"] != "grey-200" {
				t.Fatalf("unexpected panel display settings: %#v", m)
			}
			header := m["header"].(map[string]any)
			if _, ok := header["background_color"]; ok {
				t.Fatalf("header background_color should not be set: %#v", header)
			}
			if header["width"] != "fill" || header["vertical_align"] != "center" {
				t.Fatalf("unexpected panel header: %#v", header)
			}
			icon := header["icon"].(map[string]any)
			if icon["tag"] != "standard_icon" || icon["token"] != "down-small-ccm_outlined" || icon["color"] != "" || icon["size"] != "16px 16px" {
				t.Fatalf("unexpected panel icon: %#v", icon)
			}
			title := header["title"].(map[string]any)
			if title["tag"] != "markdown" || title["content"] != "<font color=red>**1**</font> : <font color=blue>**0** </font>" {
				t.Fatalf("unexpected panel title: %#v", title)
			}
			border := m["border"].(map[string]any)
			if _, ok := border["color"]; ok {
				t.Fatalf("border color should not be set: %#v", border)
			}
			if border["corner_radius"] != "5px" {
				t.Fatalf("unexpected panel border: %#v", border)
			}
			children := m["elements"].([]any)
			content := children[0].(map[string]any)
			if content["element_id"] != "elem_round_1_content" {
				t.Fatalf("content element_id = %v, want elem_round_1_content", content["element_id"])
			}
		}
		if m["tag"] == "column_set" && len(m["columns"].([]any)) != 3 {
			t.Fatalf("unexpected extra column_set after team header: %#v", m)
		}
	}
	if !foundPanel {
		t.Fatal("round collapsible panel not found")
	}
	if !strings.Contains(raw, "img_red") || !strings.Contains(raw, `红\"校`) {
		t.Fatalf("rendered card did not include escaped fields: %s", raw)
	}
	if strings.Contains(raw, "点击查看录制") || strings.Contains(raw, "card_link") {
		t.Fatalf("rendered card still includes removed recording jump: %s", raw)
	}
}

func TestCardEntityDataRendersMultipleRoundPanels(t *testing.T) {
	content := &utils.MatchCardContent{Data: utils.MatchCardData{
		RedTeam:    "红队",
		BlueTeam:   "蓝队",
		Color:      "orange",
		RedSchool:  "红校",
		BlueSchool: "蓝校",
		Rounds: []utils.MatchRoundCard{
			{PanelID: "elem_round_1", ContentID: "elem_round_1_content", Title: "<font color=red>**0**</font> : <font color=blue>**0** </font>", Content: "暂无录制"},
			{PanelID: "elem_round_2", ContentID: "elem_round_2_content", Title: "<font color=red>**1**</font> : <font color=blue>**0** </font>", Content: "[主视角](https://example.com)"},
		},
	}}
	raw, err := cardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(raw, "collapsible_panel") != 2 {
		t.Fatalf("rendered card should contain two collapsible panels: %s", raw)
	}
}

func TestMatchNeedsCardSend(t *testing.T) {
	if !matchNeedsCardSend(&ent.Match{}) {
		t.Fatal("match without card should need send")
	}
	m := &ent.Match{Edges: ent.MatchEdges{LarkMessages: []*ent.LarkMessage{
		{CardID: "card_1"},
	}}}
	if matchNeedsCardSend(m) {
		t.Fatal("card presence should stop persistent send compensation")
	}
}

func TestRoundCardsIncludeUploadLinks(t *testing.T) {
	url := "https://example.com/record"
	winner := matchround.WinnerRed
	logic := &NotifyLogic{}
	got := logic.roundCards(&ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{
			RoundNo: 1,
			Status:  matchround.StatusENDED,
			Winner:  &winner,
			Edges: ent.MatchRoundEdges{RecordTasks: []*ent.RecordTask{
				{
					Role: "主视角",
					Edges: ent.RecordTaskEdges{UploadTask: &ent.UploadTask{
						BitableRecordURL: &url,
					}},
				},
			}},
		},
	}}})
	if len(got) != 1 {
		t.Fatalf("roundCards len = %d, want 1", len(got))
	}
	if got[0].PanelID != "elem_round_1" || got[0].ContentID != "elem_round_1_content" {
		t.Fatalf("unexpected element ids: %#v", got[0])
	}
	if got[0].Title != "<font color=red>**1**</font> : <font color=blue>**0** </font>" {
		t.Fatalf("unexpected title: %s", got[0].Title)
	}
	if got[0].Content != "[主视角](https://example.com/record)" {
		t.Fatalf("content = %q", got[0].Content)
	}
}

func TestRoundCardsWithoutUploadsShowEmptyText(t *testing.T) {
	logic := &NotifyLogic{}
	got := logic.roundCards(&ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{RoundNo: 1, Status: matchround.StatusSTARTED},
	}}})
	if len(got) != 1 || got[0].Content != "暂无录制" {
		t.Fatalf("roundCards = %#v", got)
	}
}

func TestIsContextDone(t *testing.T) {
	if !isContextDone(context.DeadlineExceeded) {
		t.Fatal("deadline exceeded should be context done")
	}
	if isContextDone(nil) {
		t.Fatal("nil should not be context done")
	}
}

func TestCardDataUpdatedAt(t *testing.T) {
	matchUpdatedAt := time.Date(2026, 5, 20, 1, 0, 0, 0, time.UTC)
	roundUpdatedAt := matchUpdatedAt.Add(time.Minute)
	m := &ent.Match{UpdatedAt: matchUpdatedAt, Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{UpdatedAt: matchUpdatedAt.Add(-time.Minute)},
		{UpdatedAt: roundUpdatedAt},
	}}}
	if got := cardDataUpdatedAt(m); !got.Equal(roundUpdatedAt) {
		t.Fatalf("cardDataUpdatedAt() = %s, want %s", got, roundUpdatedAt)
	}
}

func TestCompletedCardColor(t *testing.T) {
	cases := map[match.Result]string{
		match.ResultRED:  "red",
		match.ResultBLUE: "wathet",
		match.ResultDRAW: "yellow",
	}
	for result, want := range cases {
		if got := completedCardColor(result); got != want {
			t.Fatalf("completedCardColor(%s) = %s, want %s", result, got, want)
		}
	}
}
