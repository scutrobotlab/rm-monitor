package logic

import "testing"

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
