package utils

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestCardEntityFanoutSmoke(t *testing.T) {
	if os.Getenv("LARK_CARDKIT_SMOKE") != "1" {
		t.Skip("set LARK_CARDKIT_SMOKE=1 to run")
	}
	appID := os.Getenv("LARK_APP_ID")
	appSecret := os.Getenv("LARK_APP_SECRET")
	if appID == "" || appSecret == "" {
		t.Fatal("LARK_APP_ID/LARK_APP_SECRET required")
	}

	content := &MatchCardContent{Data: MatchCardData{
		RedTeam:       "红队",
		BlueTeam:      "蓝队",
		MatchProgress: "进行中",
		MatchIndex:    "999",
		TotalRound:    "3",
		EventTitle:    "CardKit Smoke",
		RedSchool:     "红校",
		BlueSchool:    "蓝校",
		RedAvatar:     "img_v3_0211t_6fe68794-1131-4b50-a0e6-4cd78d1c5fag",
		BlueAvatar:    "img_v3_0211t_6fe68794-1131-4b50-a0e6-4cd78d1c5fag",
		Rounds: []MatchRoundCard{{
			PanelID:   "elem_round_1",
			ContentID: "elem_round_1_content",
			Title:     "<font color=red>**0**</font> : <font color=blue>**0** </font>",
			Content:   "暂无录制",
		}},
		Color:     "orange",
		MatchType: "测试",
		ZoneTitle: "测试赛区",
	}}
	client := lark.NewClient(appID, appSecret, lark.WithEnableTokenCache(true))
	ctx := context.Background()
	retry := func(_ string, f func() error) error { return f() }
	cardIDs := make([]string, 0, 2)
	if os.Getenv("LARK_CARDKIT_SMOKE_SEND") == "1" {
		chats, err := client.Im.Chat.List(ctx, larkim.NewListChatReqBuilder().PageSize(2).Build())
		if err != nil {
			t.Fatal(err)
		}
		if !chats.Success() {
			t.Fatalf("chat list failed: %d %s", chats.Code, chats.Msg)
		}
		if len(chats.Data.Items) == 0 {
			t.Fatal("no joined chat available for smoke send")
		}
		sendCount := min(2, len(chats.Data.Items))
		if sendCount < 2 {
			t.Logf("only %d joined chat available for live smoke; multi-chat behavior is covered by unit tests", sendCount)
		}
		for i, chat := range chats.Data.Items[:sendCount] {
			chatID := *chat.ChatId
			cardID, _, err := CreateCardEntity(ctx, client, retry, content)
			if err != nil {
				t.Fatalf("create card entity for chat %d failed: %v", i, err)
			}
			if _, err := SendCardReferenceMessage(ctx, client, retry, chatID, cardID, fmt.Sprintf("card-smoke:%d:%d", i, time.Now().UnixNano()%1_000_000_000)); err != nil {
				t.Fatalf("send card reference to chat %d failed: %v", i, err)
			}
			cardIDs = append(cardIDs, cardID)
		}
	}

	content.Data.MatchProgress = "Round 1 已结束"
	content.Data.Rounds = []MatchRoundCard{{
		PanelID:   "elem_round_1",
		ContentID: "elem_round_1_content",
		Title:     "<font color=red>**1**</font> : <font color=blue>**0** </font>",
		Content:   "[主视角](https://example.com/round1-main)",
	}}
	for _, cardID := range cardIDs {
		if _, err := UpdateCardEntity(ctx, client, retry, cardID, SmokeUpdateUUID(), time.Now().Unix(), content); err != nil {
			t.Fatalf("update card %s failed: %v", cardID, err)
		}
	}

	content.Data.MatchProgress = "Round 2 已结束"
	content.Data.Report = "Smoke report：Round 1 和 Round 2 录制链接均已写入卡片。"
	content.Data.Rounds = append(content.Data.Rounds, MatchRoundCard{
		PanelID:   "elem_round_2",
		ContentID: "elem_round_2_content",
		Title:     "<font color=red>**1**</font> : <font color=blue>**1** </font>",
		Content:   "[主视角](https://example.com/round2-main)\n[蓝方视角](https://example.com/round2-blue)",
	})
	for _, cardID := range cardIDs {
		if _, err := UpdateCardEntity(ctx, client, retry, cardID, SmokeUpdateUUID(), time.Now().Unix(), content); err != nil {
			t.Fatalf("update card %s second time failed: %v", cardID, err)
		}
	}
}
