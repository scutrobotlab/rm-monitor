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
	content.Data.Scores = []utils.MatchScore{{RedScore: "1", BlueScore: "0"}}
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
	body := got["body"].(map[string]any)
	if len(body["elements"].([]any)) == 0 {
		t.Fatal("rendered card has no body elements")
	}
	if !strings.Contains(raw, "img_red") || !strings.Contains(raw, `红\"校`) {
		t.Fatalf("rendered card did not include escaped fields: %s", raw)
	}
}

func TestMatchNeedsCardSend(t *testing.T) {
	if !matchNeedsCardSend(&ent.Match{}) {
		t.Fatal("match without card should need send")
	}
	m := &ent.Match{Edges: ent.MatchEdges{LarkMessages: []*ent.LarkMessage{
		{Edges: ent.LarkMessageEdges{}},
	}}}
	if !matchNeedsCardSend(m) {
		t.Fatal("card without sent messages should need send")
	}
	m.Edges.LarkMessages[0].Edges.CardMessages = []*ent.LarkCardMessage{{MessageID: "om_1"}}
	if matchNeedsCardSend(m) {
		t.Fatal("card with sent messages should not need send")
	}
}

func TestLarkMessageIDs(t *testing.T) {
	got := larkMessageIDs([]*ent.LarkMessage{
		{Edges: ent.LarkMessageEdges{CardMessages: []*ent.LarkCardMessage{{MessageID: "om_1"}, {MessageID: ""}}}},
		{Edges: ent.LarkMessageEdges{CardMessages: []*ent.LarkCardMessage{{MessageID: "om_2"}}}},
	})
	if len(got) != 2 || got[0] != "om_1" || got[1] != "om_2" {
		t.Fatalf("larkMessageIDs() = %#v", got)
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
