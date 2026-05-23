package utils

import (
	"testing"

	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	"github.com/pkg/errors"
)

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

func TestCardReferenceContentCanBeReusedAcrossChats(t *testing.T) {
	contentA, err := CardReferenceMessageContent("card-1")
	if err != nil {
		t.Fatal(err)
	}
	contentB, err := CardReferenceMessageContent("card-1")
	if err != nil {
		t.Fatal(err)
	}
	if contentA != `{"data":{"card_id":"card-1"},"type":"card"}` {
		t.Fatalf("content = %s, want CardKit card reference", contentA)
	}
	if contentA != contentB {
		t.Fatalf("same card_id should render identical content: %s vs %s", contentA, contentB)
	}
	if MatchCardUUID("match-1", "chat-a") == MatchCardUUID("match-1", "chat-b") {
		t.Fatal("different chats must use different message UUIDs while sharing the same card_id content")
	}
}

func TestIsCardUpdateAlreadyApplied(t *testing.T) {
	for _, code := range []int{200770, 300317} {
		err := &larkcardkit.UpdateCardResp{}
		err.Code = code
		if !IsCardUpdateAlreadyApplied(errors.Wrap(err, "wrapped")) {
			t.Fatalf("code %d should be treated as already applied", code)
		}
	}
	err := &larkcardkit.UpdateCardResp{}
	err.Code = 9499
	if IsCardUpdateAlreadyApplied(err) {
		t.Fatal("9499 should remain a hard card update error")
	}
}
