package logic

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
)

func TestResultWebhookKeyUsesStableHash(t *testing.T) {
	hash := resultWebhookHash("https://open.feishu.cn/open-apis/bot/v2/hook/secret")
	if hash != resultWebhookHash("https://open.feishu.cn/open-apis/bot/v2/hook/secret") {
		t.Fatal("hash should be stable")
	}
	if hash == resultWebhookHash("https://open.feishu.cn/open-apis/bot/v2/hook/other") {
		t.Fatal("hash should include webhook url")
	}
	if got := resultWebhookKey("match-1", hash); got != "rm-monitor:lark-result-webhook:match-1:"+hash {
		t.Fatalf("resultWebhookKey() = %q", got)
	}
}

func TestResultWebhookPayloadReusesMatchCardContent(t *testing.T) {
	card := &utils.MatchCardContent{Type: "template"}
	card.Data.TemplateId = "AAqtgv8IIbOr4"
	payload := resultWebhookPayload{MsgType: "interactive", Card: card}
	if payload.MsgType != "interactive" {
		t.Fatalf("MsgType = %q", payload.MsgType)
	}
	if payload.Card != card {
		t.Fatal("payload should reuse the rendered match card content")
	}
}

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
