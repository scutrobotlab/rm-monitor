package logic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestRenderReadmeUsesStableMarkdown(t *testing.T) {
	started := time.Date(2026, 5, 13, 10, 1, 2, 0, time.FixedZone("CST", 8*3600))
	ended := started.Add(12 * time.Minute)
	winner := matchround.WinnerRed
	readme, err := renderReadme(&ent.Match{
		ID:           "match-1",
		Event:        "RMUC | 2026",
		Zone:         "南部赛区",
		Order:        55,
		MatchType:    "GROUP",
		LatestStatus: "PENDING",
		Edges: ent.MatchEdges{
			Rounds: []*ent.MatchRound{{
				RoundNo:   1,
				Status:    matchround.StatusENDED,
				Winner:    &winner,
				StartedAt: started,
				EndedAt:   &ended,
			}},
		},
	}, &ent.Team{
		SchoolName: "红方大学",
		Name:       "Alpha|One",
	}, &ent.Team{
		SchoolName: "蓝方大学",
		Name:       "Beta",
	})
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, readme, "# 55. 红方大学-Alpha|One VS 蓝方大学-Beta")
	assertContains(t, readme, "| 赛事 | RMUC \\| 2026 |")
	assertContains(t, readme, "| 红方 | 红方大学-Alpha\\|One |")
	assertContains(t, readme, "| 状态 | 已结束 |")
	assertContains(t, readme, "| 比分 | 红 1 - 0 蓝 |")
	assertContains(t, readme, "| 1 | 已结束 | 红方（红方大学-Alpha\\|One） | 2026-05-13 10:01:02 | 2026-05-13 10:13:02 |")
}

func TestRenderReadmeIncludesReport(t *testing.T) {
	report := "### 关键战况\n\n红方把握了关键机会。"
	readme, err := renderReadme(&ent.Match{
		ID:     "match-1",
		Event:  "RMUC",
		Zone:   "南部赛区",
		Order:  55,
		Report: &report,
	}, &ent.Team{Name: "Alpha"}, &ent.Team{Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, readme, "## 战报")
	assertContains(t, readme, "红方把握了关键机会。")
}

func TestCallReportLLMParsesOpenAICompatibleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"## 战报\n\n测试内容"}}]}`))
	}))
	defer server.Close()

	got, err := callReportLLM(context.Background(), common.ReportConf{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	}, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got != "## 战报\n\n测试内容" {
		t.Fatalf("report = %q", got)
	}
}

func TestOpenAIBaseURLNormalizesV1(t *testing.T) {
	tests := map[string]string{
		"https://ai.scutbot.cn/":         "https://ai.scutbot.cn/v1",
		"https://ai.scutbot.cn/v1":       "https://ai.scutbot.cn/v1",
		"https://example.com/openai":     "https://example.com/openai/v1",
		"https://example.com/openai/v1/": "https://example.com/openai/v1",
	}
	for input, want := range tests {
		if got := openAIBaseURL(input); got != want {
			t.Fatalf("openAIBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("rendered README does not contain %q\n%s", want, got)
	}
}
