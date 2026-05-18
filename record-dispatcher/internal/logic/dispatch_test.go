package logic

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
)

func TestFilterBlacklistedRoles(t *testing.T) {
	urls := map[string]string{
		"主视角":         "main",
		"主视角（无解说版）":   "main-no-commentary",
		"蓝方机器人第一视角合集": "blue-all",
		"红方英雄第一视角":    "red-hero",
	}
	got := filterBlacklistedRoles(urls, []string{"主视角（无解说版）", "蓝方机器人第一视角合集"})

	if _, ok := got["主视角（无解说版）"]; ok {
		t.Fatal("blacklisted main no-commentary role was kept")
	}
	if _, ok := got["蓝方机器人第一视角合集"]; ok {
		t.Fatal("blacklisted blue all role was kept")
	}
	if got["主视角"] != "main" || got["红方英雄第一视角"] != "red-hero" {
		t.Fatalf("non-blacklisted roles changed: %#v", got)
	}
}

func TestManifestJobNameStablePerMatch(t *testing.T) {
	first := manifestJobName("match-1")
	if first != manifestJobName("match-1") {
		t.Fatal("manifest job name must be stable for the same match")
	}
	if first == manifestJobName("match-2") {
		t.Fatal("manifest job name should include match id")
	}
}

func TestCompletedMatchRequiresAllRoundsEnded(t *testing.T) {
	if completedMatch(&ent.Match{}) {
		t.Fatal("match without rounds should not be complete")
	}
	m := &ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{Status: matchround.StatusENDED},
		{Status: matchround.StatusSTARTED},
	}}}
	if completedMatch(m) {
		t.Fatal("match with started round should not be complete")
	}
	m.Edges.Rounds[1].Status = matchround.StatusENDED
	if !completedMatch(m) {
		t.Fatal("match with all rounds ended should be complete")
	}
}
