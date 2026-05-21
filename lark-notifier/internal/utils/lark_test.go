package utils

import "testing"

func TestMatchCardUUID(t *testing.T) {
	got := MatchCardUUID("match-1", "chat-1")
	if got != MatchCardUUID("match-1", "chat-1") {
		t.Fatalf("MatchCardUUID() is not stable")
	}
	if got == MatchCardUUID("match-1", "chat-2") {
		t.Fatalf("MatchCardUUID() should include chat id")
	}
	if len(got) > 50 {
		t.Fatalf("MatchCardUUID() length = %d, want <= 50", len(got))
	}
}

func TestUploadReplyUUID(t *testing.T) {
	got := UploadReplyUUID(123, "om_long_message_id")
	if got != UploadReplyUUID(123, "om_long_message_id") {
		t.Fatalf("UploadReplyUUID() is not stable")
	}
	if got == UploadReplyUUID(124, "om_long_message_id") {
		t.Fatalf("UploadReplyUUID() should include upload task id")
	}
	if len(got) > 50 {
		t.Fatalf("UploadReplyUUID() length = %d, want <= 50", len(got))
	}
}

func TestMatchCardUpdateUUID(t *testing.T) {
	got := MatchCardUpdateUUID("match-1", "card-1", 100)
	if got != MatchCardUpdateUUID("match-1", "card-1", 100) {
		t.Fatalf("MatchCardUpdateUUID() is not stable")
	}
	if got == MatchCardUpdateUUID("match-1", "card-1", 101) {
		t.Fatalf("MatchCardUpdateUUID() should include sequence")
	}
	if len(got) > 50 {
		t.Fatalf("MatchCardUpdateUUID() length = %d, want <= 50", len(got))
	}
}
