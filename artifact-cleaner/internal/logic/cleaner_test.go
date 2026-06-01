package logic

import (
	"path/filepath"
	"testing"
)

func TestArchivePathForSource(t *testing.T) {
	got := archivePathForSource("/records", "event/match/Round-1/主视角.flv")
	want := filepath.Join("/records", "event", "match", "Round-1", "主视角.mp4")
	if got != want {
		t.Fatalf("archive path = %q, want %q", got, want)
	}
}

func TestArchivePathForSourceRejectsEmptyOrExtensionless(t *testing.T) {
	if got := archivePathForSource("/records", ""); got != "" {
		t.Fatalf("empty source should not produce archive path: %q", got)
	}
	if got := archivePathForSource("/records", "event/source"); got != "" {
		t.Fatalf("extensionless source should not produce archive path: %q", got)
	}
}
