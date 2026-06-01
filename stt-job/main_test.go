package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/subtitle"
	jobconfig "scutbot.cn/web/rm-monitor/stt-job/internal/config"
)

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

func TestWriteRecognizedLinesWritesOneJSONLRowPerSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	result := whisperResult{
		Duration: 30,
		Text:     "hello",
		Segments: []whisperSegment{
			{ID: 1, Start: 2, End: 4, Text: "hello"},
			{ID: 2, Start: 5, End: 9, Text: "world"},
		},
	}
	if err := writeRecognizedLines(path, result, 1.5); err != nil {
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
	if line.Start != 2 || line.End != 4 || line.APISeconds != 1.5 || line.Text != "hello" || line.SegmentID != 1 {
		t.Fatalf("line = %#v", line)
	}
	if err := json.Unmarshal(rows[1], &line); err != nil {
		t.Fatal(err)
	}
	if line.Start != 5 || line.End != 9 || line.Text != "world" || line.SegmentID != 2 {
		t.Fatalf("line = %#v", line)
	}
}

func TestWriteRecognizedLinesSimplifiesTraditionalChinese(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stt.jsonl")
	result := whisperResult{
		Duration: 30,
		Text:     "機甲大師開場",
		Segments: []whisperSegment{
			{ID: 1, Start: 2, End: 4, Text: "紅方進攻"},
			{ID: 2, Start: 5, End: 9, Text: "藍方防守"},
		},
	}
	if err := writeRecognizedLines(path, result, 1.5); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "紅方") || strings.Contains(text, "藍方") {
		t.Fatalf("stt text was not simplified:\n%s", text)
	}
	if !strings.Contains(text, "红方进攻") || !strings.Contains(text, "蓝方防守") {
		t.Fatalf("simplified text missing:\n%s", text)
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
	audio := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(audio, []byte("m4atest"), 0o644); err != nil {
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

	result, _, err := recognizeFile(context.Background(), []string{server.URL + "/inference"}, "请使用简体中文输出。红方：A", audio)
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

func TestFinishSTTWritesSubtitleAndResult(t *testing.T) {
	dir := t.TempDir()
	roundDir := filepath.Join(dir, "Round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if err := os.WriteFile(sttPath, []byte(`{"start":1,"end":2,"status":"SUCCEEDED","text":"测试字幕"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sttCtx := jobcontract.STTContext{
		MatchRoundID: 42,
		RoundDir:     roundDir,
		STTPath:      sttPath,
		SubtitleName: "主视角.srt",
	}
	if err := finishSTT(sttCtx, roundInfoFromContext(sttCtx)); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(roundDir, "主视角.srt")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(raw), "测试字幕") {
		t.Fatalf("subtitle missing text:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(jobcontract.TempJobDir, "result.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(jobcontract.ArgoOutDir, "stt_path")); err != nil {
		t.Fatal(err)
	}
}

func TestRunSTTNoAudioWritesNoAudioAndResult(t *testing.T) {
	dir := t.TempDir()
	roundDir := filepath.Join(dir, "Round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatal(err)
	}
	restore := prependFakeFFmpeg(t, dir, `#!/bin/sh
echo "Stream map '0:a:0' matches no streams." >&2
exit 1
`)
	defer restore()
	sttCtx := jobcontract.STTContext{
		MatchRoundID:      7,
		SourcePath:        filepath.Join(dir, "source.flv"),
		RoundDir:          roundDir,
		STTPath:           filepath.Join(roundDir, "stt.jsonl"),
		SubtitleName:      "主视角.srt",
		WhisperServerURLs: []string{"http://127.0.0.1/unused"},
	}
	if err := runSTT(context.Background(), sttCtx, jobconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sttCtx.STTPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"NO_AUDIO"`) {
		t.Fatalf("missing NO_AUDIO row:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(roundDir, "主视角.srt")); !os.IsNotExist(err) {
		t.Fatalf("subtitle should not exist for no audio, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(jobcontract.TempJobDir, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestQualityCleanSTTRenamesRawAndWritesCleaned(t *testing.T) {
	roundDir := t.TempDir()
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if err := appendLines(sttPath, []sttLine{
		{Index: 0, Start: 0, End: 2, Status: "SUCCEEDED", Text: "紅方開局"},
		{Index: 1, Start: 3, End: 5, Status: "SUCCEEDED", Text: "关注官方账号"},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs map[string]any `json:"inputs"`
			User   string         `json:"user"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.User != "rm-monitor:stt:9" {
			t.Fatalf("user = %q", body.User)
		}
		payload, _ := body.Inputs["payload"].(string)
		if !strings.Contains(payload, `"schema":"rm-monitor/dify-stt-quality-input/v1"`) {
			t.Fatalf("payload missing schema: %s", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"succeeded","outputs":{"cleaned_json":{"usable":true,"quality_score":0.8,"segments":[{"index":0,"start":0,"end":2,"status":"SUCCEEDED","text":"红方开局压制","keep":true},{"index":1,"keep":false,"reason":"ad"}]}}}}`))
	}))
	defer server.Close()

	sttCtx := jobcontract.STTContext{MatchRoundID: 9, MatchID: "match-8", RoundNo: 1, Role: "主视角", RoundDir: roundDir, STTPath: sttPath}
	conf := jobconfig.Config{
		DifyConf:       common.DifyConf{BaseURL: server.URL, TimeoutSeconds: 5},
		STTQualityConf: common.STTQualityConf{UseQuality: true, WorkflowAPIKey: "app-test"},
	}
	if err := qualityCleanSTT(context.Background(), sttCtx, roundInfoFromContext(sttCtx), conf); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(roundDir, "stt.raw.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "关注官方账号") {
		t.Fatalf("raw stt not preserved:\n%s", raw)
	}
	cleaned, err := os.ReadFile(sttPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cleaned), "关注官方账号") || !strings.Contains(string(cleaned), "红方开局压制") {
		t.Fatalf("unexpected cleaned stt:\n%s", cleaned)
	}
	if _, err := os.Stat(filepath.Join(roundDir, "stt.quality.json")); err != nil {
		t.Fatal(err)
	}
}

func TestQualityOutputPartialAppliesOnlyChanges(t *testing.T) {
	keep := false
	lines, err := qualityOutputToLines([]sttLine{
		{Index: 0, Start: 0, End: 1, Status: "SUCCEEDED", Text: "紅方開局"},
		{Index: 1, Start: 1, End: 2, Status: "SUCCEEDED", Text: "是的"},
		{Index: 2, Start: 2, End: 3, Status: "SUCCEEDED", Text: "蓝方防守"},
	}, sttQualityOutput{
		Partial: true,
		Segments: []sttQualitySegment{
			{Index: 0, Text: "红方开局压制"},
			{Index: 1, Keep: &keep, Reason: "low_information"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0].Text != "红方开局压制" || lines[1].Text != "蓝方防守" {
		t.Fatalf("unexpected partial output: %#v", lines)
	}
}

func TestQualityFailureKeepsRawSTTAndWritesErrorArtifact(t *testing.T) {
	roundDir := t.TempDir()
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if err := appendLine(sttPath, sttLine{Index: 0, Start: 0, End: 2, Status: "SUCCEEDED", Text: "红方开局"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "dify unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	sttCtx := jobcontract.STTContext{MatchRoundID: 9, MatchID: "match-8", RoundNo: 1, Role: "主视角", RoundDir: roundDir, STTPath: sttPath}
	conf := jobconfig.Config{
		DifyConf:       common.DifyConf{BaseURL: server.URL, TimeoutSeconds: 5},
		STTQualityConf: common.STTQualityConf{UseQuality: true, WorkflowAPIKey: "app-test"},
	}
	if err := applyQualityCleanSTT(context.Background(), sttCtx, roundInfoFromContext(sttCtx), conf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(roundDir, "stt.raw.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("raw rename should not happen on quality failure, err=%v", err)
	}
	raw, err := os.ReadFile(sttPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "红方开局") {
		t.Fatalf("raw stt should stay at stt.jsonl:\n%s", raw)
	}
	errArtifact, err := os.ReadFile(filepath.Join(roundDir, "stt.quality.error.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(errArtifact), "dify") {
		t.Fatalf("quality error artifact missing error detail:\n%s", errArtifact)
	}
}

func prependFakeFFmpeg(t *testing.T, dir, script string) func() {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg := filepath.Join(bin, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		ffmpeg = filepath.Join(bin, "ffmpeg.bat")
		batch := "@echo off\r\necho Stream map '0:a:0' matches no streams. 1>&2\r\nexit /b 1\r\n"
		if err := os.WriteFile(ffmpeg, []byte(batch), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := os.Getenv("PATH")
	if err := os.Setenv("PATH", bin+string(os.PathListSeparator)+old); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Setenv("PATH", old) }
}
