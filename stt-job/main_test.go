package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
)

func TestSegmentCompleteRequiresNextOrDoneAndStable(t *testing.T) {
	dir := t.TempDir()
	if got := filepath.Base(segmentPath(dir, 0)); got != "part-00000.wav" {
		t.Fatalf("segment path = %q, want wav", got)
	}
	if err := os.WriteFile(segmentPath(dir, 0), []byte("wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	if segmentComplete(dir, 0) {
		t.Fatal("segment without next segment or done marker is complete")
	}
	if err := os.WriteFile(segmentPath(dir, 1), []byte("next"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !segmentComplete(dir, 0) {
		t.Fatal("segment with next segment should be complete")
	}
}

func TestAppendLineWritesJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	if err := appendLine(path, sttLine{Index: 1, Start: 60, End: 120, Status: "SUCCEEDED", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var line sttLine
	if err := json.Unmarshal(raw[:len(raw)-1], &line); err != nil {
		t.Fatal(err)
	}
	if line.Index != 1 || line.Start != 60 || line.End != 120 || line.Status != "SUCCEEDED" || line.Text != "hello" {
		t.Fatalf("line = %#v", line)
	}
}

func TestAppendRecognizedLineWritesOneJSONLRowPerSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	result := whisperResult{
		Duration: 30,
		Text:     "hello",
		Segments: []whisperSegment{
			{ID: 1, Start: 2, End: 4, Text: "hello"},
			{ID: 2, Start: 5, End: 9, Text: "world"},
		},
	}
	if err := appendRecognizedLine(path, 2, 1, 120, result, 1.5); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := splitJSONLLines(raw)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2; raw=%s", len(rows), raw)
	}
	var line sttLine
	if err := json.Unmarshal(rows[0], &line); err != nil {
		t.Fatal(err)
	}
	if line.Start != 122 || line.End != 124 || line.APISeconds != 1.5 || line.Text != "hello" || line.SegmentID != 1 {
		t.Fatalf("line = %#v", line)
	}
	if err := json.Unmarshal(rows[1], &line); err != nil {
		t.Fatal(err)
	}
	if line.Start != 125 || line.End != 129 || line.Text != "world" || line.SegmentID != 2 {
		t.Fatalf("line = %#v", line)
	}
}

func splitJSONLLines(raw []byte) [][]byte {
	raw = raw[:len(raw)-1]
	out := make([][]byte, 0)
	for _, row := range bytes.Split(raw, []byte("\n")) {
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out
}

func TestRecognizeFilePostsWhisperMultipart(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "part-00000.wav")
	if err := os.WriteFile(wav, []byte("RIFFtest"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			t.Fatalf("path = %q, want /inference", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("response_format"); got != "verbose_json" {
			t.Fatalf("response_format = %q", got)
		}
		if got := r.FormValue("temperature"); got != "0.0" {
			t.Fatalf("temperature = %q", got)
		}
		if got := r.FormValue("prompt"); !strings.Contains(got, "简体中文") || !strings.Contains(got, "红方") {
			t.Fatalf("prompt = %q", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"duration":1.2,"text":"ok","segments":[{"id":0,"start":0.1,"end":0.9,"text":"ok","words":[{"word":"ignored"}]}]}`))
	}))
	defer server.Close()

	result, _, err := recognizeFile(context.Background(), server.URL+"/", "请使用简体中文输出。红方：A", wav)
	if err != nil {
		t.Fatal(err)
	}
	if result.Duration != 1.2 || result.Text != "ok" || len(result.Segments) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunSubtitleBackfill(t *testing.T) {
	dir := t.TempDir()
	roundDir := filepath.Join(dir, "Event", "Zone", "Match", "Round-1")
	highlightDir := filepath.Join(roundDir, "highlights", "Highlight-01")
	if err := os.MkdirAll(highlightDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stt := strings.Join([]string{
		`{"start":10,"end":12,"status":"SUCCEEDED","text":"开场"}`,
		`{"start":20,"end":23,"status":"SUCCEEDED","text":"高光"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(roundDir, "stt.jsonl"), []byte(stt), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"start_seconds":18,"end_seconds":25}`
	if err := os.WriteFile(filepath.Join(highlightDir, "highlight.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := subtitle.Backfill(common.RecordConf{BaseDir: dir, STTRole: "主视角"}, subtitle.BackfillOptions{Force: true, Rounds: true, Highlights: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(roundDir, "主视角.srt")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(highlightDir, "video.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "00:00:02,000 --> 00:00:05,000") || !strings.Contains(string(raw), "高光") {
		t.Fatalf("unexpected highlight srt:\n%s", raw)
	}
}
