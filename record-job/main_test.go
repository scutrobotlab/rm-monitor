package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRecordFFmpegArgsKeepsAudioForConfiguredRole(t *testing.T) {
	args := recordFFmpegArgs("https://example.test/live.m3u8", "/records/a.flv.part", true)
	if slices.Contains(args, "-an") {
		t.Fatalf("audio role args must not contain -an: %v", args)
	}
	assertHasSequence(t, args, "-map", "0:a:0?")
	assertHasSequence(t, args, "-c:a", "copy")
	assertHasSequence(t, args, "-map", "0:v:0")
	assertHasSequence(t, args, "-c:v", "copy")
}

func TestRecordFFmpegArgsDropsAudioForOtherRoles(t *testing.T) {
	args := recordFFmpegArgs("https://example.test/live.m3u8", "/records/a.flv.part", false)
	if !slices.Contains(args, "-an") {
		t.Fatalf("non-audio role args must contain -an: %v", args)
	}
	if slices.Contains(args, "0:a:0?") {
		t.Fatalf("non-audio role args must not map audio: %v", args)
	}
}

func TestRoleKeepsAudio(t *testing.T) {
	if !roleKeepsAudio([]string{"主视角"}, "主视角") {
		t.Fatal("expected configured role to keep audio")
	}
	if roleKeepsAudio([]string{"主视角"}, "蓝方机器人") {
		t.Fatal("unexpected audio for unconfigured role")
	}
}

func TestWriteRecordMeta(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	done := start.Add(5 * time.Minute)
	err := writeRecordMeta(dir, recordMeta{
		Schema:                "rm-monitor/record-meta/v1",
		RecordTaskID:          42,
		Role:                  "主视角",
		SourceURL:             "https://example.test/live.m3u8",
		OutputPath:            "Event/Zone/Match/Round-1/主视角.flv",
		RecordWallStartedAt:   start,
		RecordWallCompletedAt: &done,
		MediaTimeZeroWallAt:   start,
		FileSize:              123,
		Checksum:              "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, recordMetaFile))
	if err != nil {
		t.Fatal(err)
	}
	var got recordMeta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "rm-monitor/record-meta/v1" || got.RecordTaskID != 42 || got.MediaTimeZeroWallAt != start {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, recordMetaFile+".part")); !os.IsNotExist(err) {
		t.Fatalf("temporary metadata file should not remain, stat err = %v", err)
	}
}

func assertHasSequence(t *testing.T, args []string, want ...string) {
	t.Helper()
	for i := 0; i+len(want) <= len(args); i++ {
		ok := true
		for j := range want {
			if args[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return
		}
	}
	t.Fatalf("args missing sequence %v: %v", want, args)
}
