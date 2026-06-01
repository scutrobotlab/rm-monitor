package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
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
	raw, err := utils.CardReferenceMessageContent("card_123")
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
		Content:   "<link icon='video_outlined' url='https://example.com/record' pc_url='' ios_url='' android_url=''>主视角</link>",
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	config := got["config"].(map[string]any)
	if config["enable_forward"] != true {
		t.Fatalf("enable_forward = %v, want true", config["enable_forward"])
	}
	if config["update_multi"] != true {
		t.Fatalf("update_multi = %v, want true", config["update_multi"])
	}
	if config["width_mode"] != "compact" {
		t.Fatalf("width_mode = %v, want compact", config["width_mode"])
	}
	if config["enable_forward_interaction"] != false {
		t.Fatalf("enable_forward_interaction = %v, want false", config["enable_forward_interaction"])
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
			if m["expanded"] != true || m["background_color"] != "grey-200" {
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
			{PanelID: "elem_round_2", ContentID: "elem_round_2_content", Title: "<font color=red>**1**</font> : <font color=blue>**0** </font>", Content: "<link icon='video_outlined' url='https://example.com' pc_url='' ios_url='' android_url=''>主视角</link>"},
		},
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(raw, "collapsible_panel") != 2 {
		t.Fatalf("rendered card should contain two collapsible panels: %s", raw)
	}
}

func TestCardEntityDataRendersHighlightImages(t *testing.T) {
	content := &utils.MatchCardContent{Data: utils.MatchCardData{
		RedTeam:           "红队",
		BlueTeam:          "蓝队",
		Color:             "orange",
		RedSchool:         "红校",
		BlueSchool:        "蓝校",
		Report:            "战报正文",
		HighlightMarkdown: "**精选高光**\n- **Round 1 高光1**：关键交锋\n  发布文案",
		HighlightMode:     "double",
		HighlightImages: []utils.HighlightImage{
			{ImageKey: "img_1", Title: "高光1"},
			{ImageKey: "img_2", Title: "高光2"},
		},
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"tag":"img_combination"`) {
		t.Fatalf("rendered card missing img_combination: %s", raw)
	}
	if !strings.Contains(raw, `"combination_mode":"double"`) || !strings.Contains(raw, `"img_key":"img_1"`) || !strings.Contains(raw, `"img_key":"img_2"`) {
		t.Fatalf("rendered card missing highlight image data: %s", raw)
	}
	if strings.Index(raw, "img_combination") < strings.Index(raw, "战报正文") {
		t.Fatalf("highlight images should render after report markdown: %s", raw)
	}
	if strings.Index(raw, "img_combination") < strings.Index(raw, "精选高光") {
		t.Fatalf("highlight images should render after highlight bullets: %s", raw)
	}
	if !strings.Contains(raw, "关键交锋") || !strings.Contains(raw, "发布文案") {
		t.Fatalf("highlight bullets missing: %s", raw)
	}
}

func TestCardEntityDataRendersNoHighlightImagesWithoutImageSection(t *testing.T) {
	content := &utils.MatchCardContent{Data: utils.MatchCardData{
		RedTeam:    "红队",
		BlueTeam:   "蓝队",
		Color:      "orange",
		RedSchool:  "红校",
		BlueSchool: "蓝校",
		Report:     "战报正文",
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"tag":"img_combination"`) || strings.Contains(raw, `"element_id":"highlight_image_1"`) || strings.Contains(raw, `"featured_highlights"`) {
		t.Fatalf("rendered card should not contain highlight section: %s", raw)
	}
}

func TestCardEntityDataRendersOneHighlightImageAsPlainImage(t *testing.T) {
	content := &utils.MatchCardContent{Data: utils.MatchCardData{
		RedTeam:           "红队",
		BlueTeam:          "蓝队",
		Color:             "orange",
		RedSchool:         "红校",
		BlueSchool:        "蓝校",
		Report:            "战报正文",
		HighlightMarkdown: "**精选高光**\n- **Round 1 高光1**：关键交锋",
		HighlightImages: []utils.HighlightImage{
			{ImageKey: "img_1", Title: "高光1"},
		},
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"tag":"img_combination"`) {
		t.Fatalf("single highlight should not render img_combination: %s", raw)
	}
	if !strings.Contains(raw, `"tag":"img"`) || !strings.Contains(raw, `"element_id":"highlight_image_1"`) || !strings.Contains(raw, `"img_key":"img_1"`) {
		t.Fatalf("single highlight should render plain image: %s", raw)
	}
	if strings.Index(raw, "highlight_image_1") < strings.Index(raw, "战报正文") {
		t.Fatalf("highlight image should render after report markdown: %s", raw)
	}
	if strings.Index(raw, "highlight_image_1") < strings.Index(raw, "精选高光") {
		t.Fatalf("highlight image should render after highlight bullets: %s", raw)
	}
}

func TestCardEntityDataRendersTwoHighlightImagesAsDouble(t *testing.T) {
	assertRenderedHighlightCombination(t, 2, "double")
}

func TestCardEntityDataRendersThreeHighlightImagesAsTriple(t *testing.T) {
	assertRenderedHighlightCombination(t, 3, "triple")
}

func TestCardEntityDataRendersFourHighlightImagesAsBisect(t *testing.T) {
	assertRenderedHighlightCombination(t, 4, "bisect")
}

func TestCardEntityDataRendersFiveHighlightImagesAsBisect(t *testing.T) {
	assertRenderedHighlightCombination(t, 5, "bisect")
}

func assertRenderedHighlightCombination(t *testing.T, n int, mode string) {
	t.Helper()
	images := make([]utils.HighlightImage, 0, n)
	for i := 1; i <= n; i++ {
		images = append(images, utils.HighlightImage{ImageKey: "img_" + string(rune('0'+i)), Title: "高光"})
	}
	content := &utils.MatchCardContent{Data: utils.MatchCardData{
		RedTeam:           "红队",
		BlueTeam:          "蓝队",
		Color:             "orange",
		RedSchool:         "红校",
		BlueSchool:        "蓝校",
		Report:            "战报正文",
		HighlightMarkdown: "**精选高光**\n- **Round 1 高光**：关键交锋",
		HighlightMode:     mode,
		HighlightImages:   images,
	}}
	raw, _, err := utils.CardEntityData(content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"combination_mode":"`+mode+`"`) {
		t.Fatalf("combination_mode not rendered as %q: %s", mode, raw)
	}
	if got := strings.Count(raw, `"img_key":"img_`); got != n {
		t.Fatalf("rendered image count = %d, want %d: %s", got, n, raw)
	}
	if strings.Index(raw, "img_combination") < strings.Index(raw, "战报正文") {
		t.Fatalf("highlight images should render after report markdown: %s", raw)
	}
	if strings.Index(raw, "img_combination") < strings.Index(raw, "精选高光") {
		t.Fatalf("highlight images should render after highlight bullets: %s", raw)
	}
}

func TestHighlightCombinationMode(t *testing.T) {
	cases := map[int]string{1: "double", 2: "double", 3: "triple", 4: "bisect", 6: "bisect", 7: "trisect", 9: "trisect"}
	for n, want := range cases {
		if got := highlightCombinationMode(n); got != want {
			t.Fatalf("highlightCombinationMode(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSelectedHighlightClipsLimitsPerRoundAndTotal(t *testing.T) {
	title := "高光"
	m := &ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{RoundNo: 1, Edges: ent.MatchRoundEdges{HighlightClips: []*ent.HighlightClip{
			{ID: 1, HighlightIndex: 1, Status: highlightclip.StatusAVAILABLE, Score: 1, Title: &title},
			{ID: 2, HighlightIndex: 2, Status: highlightclip.StatusAVAILABLE, Score: 5, Title: &title},
			{ID: 3, HighlightIndex: 3, Status: highlightclip.StatusAVAILABLE, Score: 4, Title: &title},
			{ID: 6, HighlightIndex: 4, Status: highlightclip.StatusAVAILABLE, Score: 10, Title: &title, AlgorithmVersion: "old"},
		}}},
		{RoundNo: 2, Edges: ent.MatchRoundEdges{HighlightClips: []*ent.HighlightClip{
			{ID: 4, HighlightIndex: 1, Status: highlightclip.StatusFAILED, Score: 99, Title: &title},
			{ID: 5, HighlightIndex: 2, Status: highlightclip.StatusAVAILABLE, Score: 3, Title: &title},
		}}},
	}}}
	for _, r := range m.Edges.Rounds {
		for _, clip := range r.Edges.HighlightClips {
			if clip.Role == "" {
				clip.Role = "主视角"
			}
			if clip.AlgorithmVersion == "" {
				clip.AlgorithmVersion = "danmu-zscore-dify-v1"
			}
			if clip.OutputDir == "" {
				clip.OutputDir = fmt.Sprintf("Round-%d/highlights/Highlight-%02d", r.RoundNo, clip.HighlightIndex)
			}
		}
	}
	got := selectedHighlightClips(m, 2, 3, "主视角", "danmu-zscore-dify-v1", "")
	if len(got) != 3 {
		t.Fatalf("selected len = %d", len(got))
	}
	ids := []int{got[0].clip.ID, got[1].clip.ID, got[2].clip.ID}
	want := []int{2, 3, 5}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("selected ids = %#v, want %#v", ids, want)
		}
	}
}

func TestSelectedHighlightClipsFiltersCurrentAlgorithmAndDedupesOutput(t *testing.T) {
	title := "高光"
	m := &ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{RoundNo: 1, Edges: ent.MatchRoundEdges{HighlightClips: []*ent.HighlightClip{
			{
				ID:               1,
				HighlightIndex:   1,
				Role:             "主视角",
				AlgorithmVersion: "danmu-zscore-v1",
				Status:           highlightclip.StatusAVAILABLE,
				Score:            9,
				OutputDir:        "Round-1/highlights/Highlight-01",
				Title:            &title,
			},
			{
				ID:               2,
				HighlightIndex:   1,
				Role:             "主视角",
				AlgorithmVersion: "danmu-zscore-dify-v1",
				Status:           highlightclip.StatusAVAILABLE,
				Score:            8,
				OutputDir:        "Round-1/highlights/Highlight-01",
				Title:            &title,
			},
			{
				ID:               3,
				HighlightIndex:   2,
				Role:             "蓝方英雄第一视角",
				AlgorithmVersion: "danmu-zscore-dify-v1",
				Status:           highlightclip.StatusAVAILABLE,
				Score:            7,
				OutputDir:        "Round-1/highlights/Highlight-02",
				Title:            &title,
			},
			{
				ID:               4,
				HighlightIndex:   3,
				Role:             "主视角",
				AlgorithmVersion: "danmu-zscore-dify-v1",
				Status:           highlightclip.StatusAVAILABLE,
				Score:            6,
				OutputDir:        "Round-1/highlights/Highlight-03",
				Title:            &title,
			},
		}}},
	}}}
	got := selectedHighlightClips(m, 2, 9, "主视角", "danmu-zscore-dify-v1", "")
	if len(got) != 2 {
		t.Fatalf("selected len = %d, want 2", len(got))
	}
	ids := []int{got[0].clip.ID, got[1].clip.ID}
	want := []int{2, 4}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("selected ids = %#v, want %#v", ids, want)
		}
	}
}

func TestHighlightMarkdownUsesCaptionAndPublishCaption(t *testing.T) {
	got := highlightMarkdown([]utils.HighlightBullet{{
		RoundNo:        2,
		Title:          "关键反超",
		Caption:        "蓝方抓住窗口完成反超",
		PublishCaption: "关键窗口，节奏拉满。",
	}})
	if !strings.Contains(got, "**精选高光**") || !strings.Contains(got, "Round 2 关键反超") ||
		!strings.Contains(got, "蓝方抓住窗口完成反超") || !strings.Contains(got, "关键窗口，节奏拉满。") {
		t.Fatalf("highlight markdown = %q", got)
	}
}

func TestHighlightPublishCaptionReadsModelPayload(t *testing.T) {
	payload := `{"review":{"publish_caption":"关键窗口，节奏拉满。"}}`
	got := highlightPublishCaption(&ent.HighlightClip{ModelPayload: &payload})
	if got != "关键窗口，节奏拉满。" {
		t.Fatalf("publish caption = %q", got)
	}
}

func TestCardIDReady(t *testing.T) {
	cardID := "card_1"
	if !cardIDReady(&ent.LarkMessage{MessageID: "om_1", CardID: &cardID}) {
		t.Fatal("real message with card_id should be ready")
	}
	if cardIDReady(&ent.LarkMessage{MessageID: "legacy:old", CardID: &cardID}) {
		t.Fatal("legacy message should not be ready")
	}
	if cardIDReady(&ent.LarkMessage{MessageID: "om_1"}) {
		t.Fatal("message without card_id cannot be updated by card entity")
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
			Edges: ent.MatchRoundEdges{LarkBitableRecords: []*ent.LarkBitableRecord{
				{
					Role:      "主视角",
					RecordURL: &url,
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
	if got[0].Content != "<link icon='video_outlined' url='https://example.com/record' pc_url='' ios_url='' android_url=''>主视角</link>" {
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
