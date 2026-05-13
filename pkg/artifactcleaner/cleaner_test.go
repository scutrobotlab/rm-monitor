package artifactcleaner

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
)

func TestCanDeleteSourceSkipsOnlyActiveUploads(t *testing.T) {
	artifact := sourceWith(uploadtask.StatusSUCCEEDED)
	if !canDeleteSource(artifact) {
		t.Fatal("succeeded upload should be deletable")
	}
	artifact = sourceWith(uploadtask.StatusFAILED)
	if !canDeleteSource(artifact) {
		t.Fatal("failed upload should be deletable")
	}
	artifact = sourceWith(uploadtask.StatusPENDING)
	if !canDeleteSource(artifact) {
		t.Fatal("pending upload should be deletable after source retention")
	}
	artifact = sourceWith(uploadtask.StatusDISPATCHING)
	if canDeleteSource(artifact) {
		t.Fatal("dispatching upload should not be deleted")
	}
	artifact = sourceWith(uploadtask.StatusRUNNING)
	if canDeleteSource(artifact) {
		t.Fatal("running upload should not be deleted")
	}
}

func TestCanDeleteSourceRequiresArchive(t *testing.T) {
	artifact := &ent.MediaArtifact{
		Edges: ent.MediaArtifactEdges{
			SourceTranscodeTask: &ent.TranscodeTask{Status: transcodetask.StatusSUCCEEDED},
		},
	}
	if canDeleteSource(artifact) {
		t.Fatal("source without archive artifact should not be deleted")
	}
}

func sourceWith(status uploadtask.Status) *ent.MediaArtifact {
	return &ent.MediaArtifact{
		Edges: ent.MediaArtifactEdges{
			UploadTask: &ent.UploadTask{Status: status},
			SourceTranscodeTask: &ent.TranscodeTask{
				Status: transcodetask.StatusSUCCEEDED,
				Edges:  ent.TranscodeTaskEdges{ArchiveArtifact: &ent.MediaArtifact{}},
			},
		},
	}
}
