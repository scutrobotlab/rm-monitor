package utils

import "testing"

func TestMatchCardUUID(t *testing.T) {
	got := MatchCardUUID("match-1", "chat-1")
	want := "rm-monitor:match-card:match-1:chat-1"
	if got != want {
		t.Fatalf("MatchCardUUID() = %q, want %q", got, want)
	}
}
