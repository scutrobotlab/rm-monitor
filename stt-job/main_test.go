package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func TestAppendRecognizedLineUsesAbsoluteSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	result := whisperResult{
		Duration: 30,
		Text:     "hello",
		Segments: []whisperSegment{
			{ID: 1, Start: 2, End: 4, Text: "hello"},
		},
	}
	if err := appendRecognizedLine(path, 2, 1, 120, result, 1.5); err != nil {
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
	if line.Start != 120 || line.End != 150 || line.APISeconds != 1.5 || line.Text != "hello" {
		t.Fatalf("line = %#v", line)
	}
	if len(line.Segments) != 1 || line.Segments[0].Start != 122 || line.Segments[0].End != 124 {
		t.Fatalf("segments = %#v", line.Segments)
	}
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
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"duration":1.2,"text":"ok","segments":[{"id":0,"start":0.1,"end":0.9,"text":"ok","words":[{"word":"ignored"}]}]}`))
	}))
	defer server.Close()

	result, _, err := recognizeFile(context.Background(), server.URL+"/", wav)
	if err != nil {
		t.Fatal(err)
	}
	if result.Duration != 1.2 || result.Text != "ok" || len(result.Segments) != 1 {
		t.Fatalf("result = %#v", result)
	}
}
