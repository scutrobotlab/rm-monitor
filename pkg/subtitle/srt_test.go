package subtitle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSRTFromJSONL(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "stt.jsonl")
	output := filepath.Join(dir, "主视角.srt")
	raw := strings.Join([]string{
		`{"start":1.2,"end":3.45,"status":"SUCCEEDED","text":"第一句"}`,
		`{"start":4,"end":5,"status":"FAILED","text":"不要出现"}`,
		`{"start":6,"end":7.01,"status":"SUCCEEDED","text":"第二句  带 空格"}`,
		`{"start":8,"end":9,"status":"SUCCEEDED","text":""}`,
	}, "\n")
	if err := os.WriteFile(input, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSRTFromJSONL(input, output, Options{}); err != nil {
		t.Fatal(err)
	}
	gotRaw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	got := string(gotRaw)
	if !strings.Contains(got, "1\n00:00:01,200 --> 00:00:03,450\n第一句") {
		t.Fatalf("missing first cue:\n%s", got)
	}
	if !strings.Contains(got, "2\n00:00:06,000 --> 00:00:07,010\n第二句 带 空格") {
		t.Fatalf("missing normalized second cue:\n%s", got)
	}
	if strings.Contains(got, "不要出现") {
		t.Fatalf("failed cue should be skipped:\n%s", got)
	}
}

func TestWriteSRTFromJSONLCropResetsTime(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "stt.jsonl")
	output := filepath.Join(dir, "video.srt")
	raw := strings.Join([]string{
		`{"start":10,"end":15,"status":"SUCCEEDED","text":"before"}`,
		`{"start":20,"end":24,"status":"SUCCEEDED","text":"inside"}`,
		`{"start":28,"end":36,"status":"SUCCEEDED","text":"overlap"}`,
		`{"start":40,"end":45,"status":"SUCCEEDED","text":"after"}`,
	}, "\n")
	if err := os.WriteFile(input, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	start, end := 18.0, 30.0
	if err := WriteSRTFromJSONL(input, output, Options{Start: &start, End: &end}); err != nil {
		t.Fatal(err)
	}
	rawOut, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	got := string(rawOut)
	if strings.Contains(got, "before") || strings.Contains(got, "after") {
		t.Fatalf("crop included outside cues:\n%s", got)
	}
	if !strings.Contains(got, "00:00:02,000 --> 00:00:06,000\ninside") {
		t.Fatalf("inside cue not reset:\n%s", got)
	}
	if !strings.Contains(got, "00:00:10,000 --> 00:00:12,000\noverlap") {
		t.Fatalf("overlap cue not clipped:\n%s", got)
	}
}

func TestWriteSRTFromJSONLNoCues(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "stt.jsonl")
	output := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(input, []byte(`{"start":0,"end":1,"status":"FAILED","text":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteSRTFromJSONL(input, output, Options{})
	if !errors.Is(err, ErrNoCues) {
		t.Fatalf("err = %v, want ErrNoCues", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output should not exist, stat err = %v", statErr)
	}
}
