package main

import (
	"slices"
	"testing"
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
