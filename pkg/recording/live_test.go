package recording

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"resty.dev/v3"
)

func TestLiveContextForZoneReturnsChatRoomAndURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"eventData": [
				{
					"zoneName": "南部赛区",
					"chatRoomId": "room-south",
					"zoneLiveString": [
						{"res": "1080p", "src": "https://example.test/main.m3u8"}
					],
					"fpvData": [
						{
							"role": "红方英雄第一视角",
							"sources": [
								{"res": "720p", "src": "https://example.test/red-720.m3u8"},
								{"res": "1080p", "src": "https://example.test/red-1080.m3u8"}
							]
						}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	got, err := LiveContextForZone(context.Background(), resty.New(), server.URL, "南部赛区", "1080p")
	if err != nil {
		t.Fatalf("LiveContextForZone returned error: %v", err)
	}
	if got.ChatRoomID != "room-south" {
		t.Fatalf("chatRoomID = %q, want room-south", got.ChatRoomID)
	}
	if got.URLs["主视角"] != "https://example.test/main.m3u8" {
		t.Fatalf("main url = %q", got.URLs["主视角"])
	}
	if got.URLs["红方英雄第一视角"] != "https://example.test/red-1080.m3u8" {
		t.Fatalf("fpv url = %q", got.URLs["红方英雄第一视角"])
	}
}

func TestLiveContextForZoneDoesNotFallbackToAnotherZone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"eventData": [
				{"zoneName": "东部赛区", "chatRoomId": "room-east", "zoneLiveString": [], "fpvData": []}
			]
		}`))
	}))
	defer server.Close()

	if _, err := LiveContextForZone(context.Background(), resty.New(), server.URL, "南部赛区", "1080p"); err == nil {
		t.Fatal("LiveContextForZone should fail when the requested zone is absent")
	}
}
