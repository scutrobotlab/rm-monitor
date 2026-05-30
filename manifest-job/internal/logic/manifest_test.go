package logic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
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
		Result:       match.ResultRED,
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
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, readme, "# 55. 红方大学-Alpha|One VS 蓝方大学-Beta")
	assertContains(t, readme, "| 赛事 | RMUC \\| 2026 |")
	assertContains(t, readme, "| 红方 | 红方大学-Alpha\\|One |")
	assertContains(t, readme, "| 状态 | 已结束 |")
	assertContains(t, readme, "| 比分 | 红 1 - 0 蓝 |")
	assertContains(t, readme, "| 胜方 | 红方（红方大学-Alpha\\|One） |")
	assertContains(t, readme, "| 1 | 已结束 | 红方（红方大学-Alpha\\|One） | 2026-05-13 10:01:02 | 2026-05-13 10:13:02 |")
	assertNotContains(t, readme, "## 录像文件")
}

func TestRenderReadmeIncludesReport(t *testing.T) {
	report := "### 关键战况\n\n红方把握了关键机会。"
	readme, err := renderReadme(&ent.Match{
		ID:     "match-1",
		Event:  "RMUC",
		Zone:   "南部赛区",
		Order:  55,
		Report: &report,
	}, &ent.Team{Name: "Alpha"}, &ent.Team{Name: "Beta"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, readme, "## 战报")
	assertContains(t, readme, "红方把握了关键机会。")
}

func TestRenderReadmeIncludesOnlyExistingDanmuCharts(t *testing.T) {
	matchDir := t.TempDir()
	statsDir := matchDir + "/Round-1/stats"
	if err := os.MkdirAll(statsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statsDir+"/danmu-count.png", []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	readme, err := renderReadme(&ent.Match{
		ID:    "match-1",
		Event: "RMUC",
		Zone:  "南部赛区",
		Order: 55,
		Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
			{RoundNo: 1, Status: matchround.StatusENDED},
			{RoundNo: 2, Status: matchround.StatusENDED},
		}},
	}, &ent.Team{Name: "Alpha"}, &ent.Team{Name: "Beta"}, matchDir)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, readme, "## 弹幕统计")
	assertContains(t, readme, "![Round 1 弹幕数量](Round-1/stats/danmu-count.png)")
	assertNotContains(t, readme, "online-count.png")
	assertNotContains(t, readme, "Round 2")
	assertNotContains(t, readme, ".json")
}

func TestBuildReportInputIncludesAuthorityFields(t *testing.T) {
	winnerPlace := "晋级八强"
	loserPlace := "进入败者组"
	input, err := buildReportInput(&ent.Match{
		ID:                    "match-1",
		Event:                 "RMUC",
		Zone:                  "南部赛区",
		Order:                 55,
		MatchType:             "GROUP",
		Result:                match.ResultBLUE,
		WinnerPlaceholderName: &winnerPlace,
		LoserPlaceholderName:  &loserPlace,
	}, &ent.Team{SchoolName: "红方大学", Name: "Alpha"}, &ent.Team{SchoolName: "蓝方大学", Name: "Beta"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, input, "- 胜方：蓝方（蓝方大学-Beta）")
	assertContains(t, input, "- 胜者去向：晋级八强")
	assertContains(t, input, "- 败者去向：进入败者组")
	assertContains(t, input, "只有当它明确表达后续赛程、轮次或对阵安排时才纳入战报")
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

	got, err := callReportLLM(context.Background(), common.LLMConf{
		BaseURL: server.URL + "/v1",
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

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("rendered README does not contain %q\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("rendered README unexpectedly contains %q\n%s", want, got)
	}
}
