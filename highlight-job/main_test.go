package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scutbot.cn/web/rm-monitor/pkg/highlight"
)

func TestBuildReviewEvidenceIncludesRoundSignals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stt.jsonl"), []byte(
		`{"start":90,"end":95,"status":"SUCCEEDED","text":"场外闲聊"}`+"\n"+
			`{"start":104,"end":109,"status":"SUCCEEDED","text":"蓝方英雄机器人打出关键伤害"}`+"\n"+
			`{"start":128,"end":132,"status":"FAILED","text":"忽略失败段"}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "主视角.danmuku.xml"), []byte(`<?xml version="1.0" encoding="UTF-8"?><i>
<d p="98.000,1,25,16777215,0,0,x,y">太早了</d>
<d p="112.500,1,25,16777215,0,0,x,y">这波漂亮</d>
<d p="116.000,1,25,16777215,0,0,x,y">蓝方起势</d>
</i>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "stats"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stats", "danmu-count.json"), []byte(`{"bucket_seconds":10,"points":[{"t":90,"count":1,"total":1},{"t":110,"count":8,"total":9},{"t":120,"count":5,"total":14}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "round.json"), []byte(`{"analysis":{"status":"CONFIRMED"},"boundary":{"start_seconds":10,"end_seconds":200},"settlement":{"status":"CONFIRMED","ocr":{"data":{"victory_condition":"基地血量","red":{"score_reference":1},"blue":{"score_reference":2}}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	evidence := buildReviewEvidence(dir, "主视角", highlight.Candidate{Start: 105, End: 120, Peak: 112})
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, want := range []string{"蓝方英雄机器人打出关键伤害", "这波漂亮", "蓝方起势", "settlement_ocr", "基地血量"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("evidence missing %q: %s", want, payload)
		}
	}
	if strings.Contains(payload, "忽略失败段") {
		t.Fatalf("failed STT segment should be excluded: %s", payload)
	}
}

func TestReadDanmuEvidenceFallsBackToRawDanmuFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "主视角.raw.danmuku.xml"), []byte(`<?xml version="1.0" encoding="UTF-8"?><i>
<d p="42.000,1,25,16777215,0,0,x,y">raw 弹幕可用</d>
</i>`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readDanmuEvidence(dir, "主视角", 40, 45, 10)
	if len(got) != 1 || got[0].Text != "raw 弹幕可用" {
		t.Fatalf("unexpected danmu evidence: %#v", got)
	}
}

func TestRecordRelativePathStripsRecordBase(t *testing.T) {
	got := recordRelativePath("/records", "/records/RMUC 2026/Round-1")
	if got != "RMUC 2026/Round-1" {
		t.Fatalf("recordRelativePath() = %q", got)
	}
}
