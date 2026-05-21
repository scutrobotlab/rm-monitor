package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestBackfillMissingOnly(t *testing.T) {
	base := t.TempDir()
	roundDir := filepath.Join(base, "Event", "Zone", "Match", "Round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sttPath := filepath.Join(roundDir, "stt.jsonl")
	if err := os.WriteFile(sttPath, []byte(`{"start":1,"end":2,"status":"SUCCEEDED","text":"已有识别"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(roundDir, "主视角.srt")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := Backfill(common.RecordConf{BaseDir: base, STTRole: "主视角"}, BackfillOptions{Rounds: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.RoundGenerated != 0 || summary.RoundSkippedExisting != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	raw, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "keep" {
		t.Fatalf("existing subtitle overwritten: %q", raw)
	}
}

func TestBackfillRoundAndHighlight(t *testing.T) {
	base := t.TempDir()
	roundDir := filepath.Join(base, "Event", "Zone", "Match", "Round-2")
	highlightDir := filepath.Join(roundDir, "highlights", "Highlight-1")
	if err := os.MkdirAll(highlightDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stt := strings.Join([]string{
		`{"start":10,"end":15,"status":"SUCCEEDED","text":"before"}`,
		`{"start":20,"end":24,"status":"SUCCEEDED","text":"inside"}`,
		`{"start":28,"end":36,"status":"SUCCEEDED","text":"overlap"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(roundDir, "stt.jsonl"), []byte(stt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(highlightDir, "highlight.json"), []byte(`{"start_seconds":18,"end_seconds":30}`), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := Backfill(common.RecordConf{BaseDir: base, STTRole: "主视角"}, BackfillOptions{Rounds: true, Highlights: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.RoundGenerated != 1 || summary.HighlightGenerated != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	roundSRT, err := os.ReadFile(filepath.Join(roundDir, "主视角.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(roundSRT), "before") {
		t.Fatalf("round srt missing cue:\n%s", roundSRT)
	}
	highlightSRT, err := os.ReadFile(filepath.Join(highlightDir, "video.srt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(highlightSRT)
	if strings.Contains(got, "before") {
		t.Fatalf("highlight srt included outside cue:\n%s", got)
	}
	if !strings.Contains(got, "00:00:02,000 --> 00:00:06,000\ninside") {
		t.Fatalf("highlight srt did not reset inside cue:\n%s", got)
	}
	if !strings.Contains(got, "00:00:10,000 --> 00:00:12,000\noverlap") {
		t.Fatalf("highlight srt did not clip overlap cue:\n%s", got)
	}
}
