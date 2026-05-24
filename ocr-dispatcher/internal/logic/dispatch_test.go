package logic

import (
	"path/filepath"
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ocr-dispatcher/internal/config"
	"scutbot.cn/web/rm-monitor/ocr-dispatcher/internal/svc"
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestOCRContextUsesSourceArtifactAndRoundDir(t *testing.T) {
	l := NewDispatchLogic(t.Context(), &svc.ServiceContext{Config: config.Config{
		OCRConf: common.OCRConf{Role: "主视角", FrameInterval: 2, SimilarityThreshold: 0.7},
		K8sJobConf: common.K8sJobConf{
			RecordsMountPath: "/records",
		},
	}})
	task := &ent.OCRTask{
		ID:   9,
		Role: "主视角",
		Edges: ent.OCRTaskEdges{
			SourceArtifact: &ent.MediaArtifact{
				ID:   12,
				Path: "RMUC/南部赛区/1. A-B VS C-D/Round-1/主视角.flv",
				Edges: ent.MediaArtifactEdges{
					RecordTask: &ent.RecordTask{
						Edges: ent.RecordTaskEdges{
							MatchRound: &ent.MatchRound{
								ID:      34,
								RoundNo: 1,
								Edges: ent.MatchRoundEdges{
									Match: &ent.Match{ID: "match-1"},
								},
							},
						},
					},
				},
			},
		},
	}
	ctx, err := l.ocrContext(task)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.TaskID != 9 || ctx.MatchRoundID != 34 || ctx.SourceArtifactID != 12 || ctx.MatchID != "match-1" {
		t.Fatalf("unexpected ids: %#v", ctx)
	}
	if ctx.SourcePath != "RMUC/南部赛区/1. A-B VS C-D/Round-1/主视角.flv" {
		t.Fatalf("source path = %q", ctx.SourcePath)
	}
	wantRoundDir := filepath.FromSlash("/records/RMUC/南部赛区/1. A-B VS C-D/Round-1")
	if ctx.RoundDir != wantRoundDir {
		t.Fatalf("round dir = %q, want %q", ctx.RoundDir, wantRoundDir)
	}
	if ctx.FrameInterval != 2 || ctx.SimilarityThreshold != 0.7 {
		t.Fatalf("ocr options = %#v", ctx)
	}
}

func TestArtifactRelRejectsEscapes(t *testing.T) {
	if _, err := artifactRel("/records", "/tmp/a.flv"); err == nil {
		t.Fatal("expected outside base dir to fail")
	}
	if _, err := artifactRel("/records", "../a.flv"); err == nil {
		t.Fatal("expected relative escape to fail")
	}
	got, err := artifactRel("/records", "/records/a/b.flv")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a/b.flv" {
		t.Fatalf("rel = %q", got)
	}
}
