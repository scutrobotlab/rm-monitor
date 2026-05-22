package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scutbot.cn/web/rm-monitor/pkg/stttext"
)

func TestNormalizeJSONLineConvertsTextAndSegments(t *testing.T) {
	converter, err := stttext.NewSimplifier()
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := normalizeJSONLine(`{"text":"比賽開始","segments":[{"text":"對戰藍方"}]}`, converter)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("line should change")
	}
	if !strings.Contains(got, "比赛开始") || !strings.Contains(got, "对战蓝方") {
		t.Fatalf("normalized line = %s", got)
	}
}

func TestNormalizeFileDryRunDoesNotWrite(t *testing.T) {
	converter, err := stttext.NewSimplifier()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	orig := `{"text":"比賽開始"}` + "\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeFile(path, converter, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("dry run should report change")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != orig {
		t.Fatalf("dry run changed file: %s", raw)
	}
}

func TestNormalizeFileWritesAtomically(t *testing.T) {
	converter, err := stttext.NewSimplifier()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	if err := os.WriteFile(path, []byte(`{"text":"對戰藍方"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := normalizeFile(path, converter, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("file should change")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "对战蓝方") {
		t.Fatalf("normalized file = %s", raw)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file exists or stat failed: %v", err)
	}
}
