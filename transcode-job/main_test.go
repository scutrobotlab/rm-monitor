package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
)

func TestArtifactPath(t *testing.T) {
	tests := []struct {
		name string
		base string
		in   string
		want string
		ok   bool
	}{
		{name: "relative chinese path", base: "/records", in: "赛事/赛区/55. A-B VS C-D/Round-1/红方.flv", want: "赛事/赛区/55. A-B VS C-D/Round-1/红方.flv", ok: true},
		{name: "absolute under base", base: "/records", in: "/records/Event/Zone/file.flv", want: "Event/Zone/file.flv", ok: true},
		{name: "clean relative", base: "/records", in: "Event/Zone/../file.flv", want: "Event/file.flv", ok: true},
		{name: "outside absolute", base: "/records", in: "/tmp/file.flv", ok: false},
		{name: "escape relative", base: "/records", in: "../file.flv", ok: false},
		{name: "empty", base: "/records", in: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, full, err := artifactPath(tt.base, tt.in)
			if tt.ok && err != nil {
				t.Fatalf("artifactPath() error = %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("artifactPath() expected error, got %q", got)
			}
			if got != tt.want {
				t.Fatalf("artifactPath() = %q, want %q", got, tt.want)
			}
			if tt.ok && full == "" {
				t.Fatalf("artifactPath() full path is empty")
			}
		})
	}
}

func TestCropRoundDanmuMovesFinalToRawAndPublishesTrimmedFinal(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "主视角.danmuku.xml")
	rawPath := filepath.Join(dir, "主视角.raw.danmuku.xml")
	if err := os.WriteFile(finalPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<i>
  <d p="1.000,1,25,16777215,0,0,0,0">before</d>
  <d p="12.500,1,25,16777215,0,0,0,0">inside</d>
  <d p="20.000,1,25,16777215,0,0,0,0">after</d>
</i>`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := 10.0
	end := 15.0
	ctx := jobcontract.TranscodeContext{RoundDir: dir, Role: "主视角", TrimStartSeconds: &start, TrimEndSeconds: &end}
	if err := cropRoundDanmu(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("raw danmu not preserved: %v", err)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "before") || !strings.Contains(string(raw), "after") {
		t.Fatalf("raw danmu was not original content:\n%s", raw)
	}
	final, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(final)
	if strings.Contains(got, "before") || strings.Contains(got, "after") {
		t.Fatalf("final danmu included outside entries:\n%s", got)
	}
	if !strings.Contains(got, `p="2.500,1,25,16777215,0,0,0,0"`) || !strings.Contains(got, "inside") {
		t.Fatalf("final danmu did not retime inside entry:\n%s", got)
	}
}

func TestApplyRoundBoundaryReadsRoundJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "round.json"), []byte(`{"boundary":{"start_seconds":12.5,"end_seconds":420.25}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := jobcontract.TranscodeContext{RoundDir: dir}
	if err := applyRoundBoundary(&ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.TrimStartSeconds == nil || *ctx.TrimStartSeconds != 12.5 {
		t.Fatalf("TrimStartSeconds = %v, want 12.5", ctx.TrimStartSeconds)
	}
	if ctx.TrimEndSeconds == nil || *ctx.TrimEndSeconds != 420.25 {
		t.Fatalf("TrimEndSeconds = %v, want 420.25", ctx.TrimEndSeconds)
	}
}

func TestApplyRoundBoundaryKeepsExplicitTrim(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "round.json"), []byte(`{"boundary":{"start_seconds":12.5,"end_seconds":420.25}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := 1.0
	end := 2.0
	ctx := jobcontract.TranscodeContext{RoundDir: dir, TrimStartSeconds: &start, TrimEndSeconds: &end}
	if err := applyRoundBoundary(&ctx); err != nil {
		t.Fatal(err)
	}
	if *ctx.TrimStartSeconds != 1.0 || *ctx.TrimEndSeconds != 2.0 {
		t.Fatalf("explicit trim overwritten: start=%v end=%v", *ctx.TrimStartSeconds, *ctx.TrimEndSeconds)
	}
}
