package utils

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
)

func TestCardKitRenderedCardJSONSmoke(t *testing.T) {
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
		Scores:        []MatchScore{{RedScore: "0", BlueScore: "0"}},
		Color:         "orange",
		MatchType:     "测试",
		ZoneTitle:     "测试赛区",
	}}
	data, err := content.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}

	client := lark.NewClient(appID, appSecret, lark.WithEnableTokenCache(true))
	ctx := context.Background()
	createResp, err := client.Cardkit.V1.Card.Create(ctx, larkcardkit.NewCreateCardReqBuilder().
		Body(larkcardkit.NewCreateCardReqBodyBuilder().Type("card_json").Data(data).Build()).
		Build())
	if err != nil {
		t.Fatal(err)
	}
	if !createResp.Success() {
		t.Fatalf("create failed: %d %s", createResp.Code, createResp.Msg)
	}
	cardID := *createResp.Data.CardId

	content.Data.MatchProgress = "更新成功"
	updated, err := content.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	updateResp, err := client.Cardkit.V1.Card.Update(ctx, larkcardkit.NewUpdateCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewUpdateCardReqBodyBuilder().
			Card(larkcardkit.NewCardBuilder().Type("card_json").Data(updated).Build()).
			Uuid(fmt.Sprintf("rm-monitor-card-json-smoke:%d", time.Now().UnixNano())).
			Sequence(int(time.Now().Unix())).
			Build()).
		Build())
	if err != nil {
		t.Fatal(err)
	}
	if !updateResp.Success() {
		t.Fatalf("update failed: %d %s", updateResp.Code, updateResp.Msg)
	}
}
