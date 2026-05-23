package jobcontract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DirName, ResultFile)
	in := TranscodeResult{Schema: "test", TaskID: 42, ArchivePath: "a/b.mp4"}
	if err := AtomicWriteJSON(path, in); err != nil {
		t.Fatalf("AtomicWriteJSON() error = %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file still exists: %v", err)
	}
	var out TranscodeResult
	ok, err := ReadJSON(path, &out)
	if err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if !ok {
		t.Fatalf("ReadJSON() ok = false")
	}
	if out.TaskID != in.TaskID || out.ArchivePath != in.ArchivePath {
		t.Fatalf("ReadJSON() = %+v, want %+v", out, in)
	}
}

func TestContextFromEnv(t *testing.T) {
	t.Setenv(EnvName, `{"task_id":7,"source_path":"in.flv","archive_path":"out.mp4"}`)
	var ctx TranscodeContext
	if err := ContextFromEnv(&ctx); err != nil {
		t.Fatalf("ContextFromEnv() error = %v", err)
	}
	if ctx.TaskID != 7 || ctx.SourcePath != "in.flv" || ctx.ArchivePath != "out.mp4" {
		t.Fatalf("ContextFromEnv() = %+v", ctx)
	}
}

func TestClearRemovesStaleFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName, "upload-1")
	for _, name := range []string{ContextFile, ResultFile, ErrorFile, ResultFile + ".tmp"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Clear(dir); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	for _, name := range []string{ContextFile, ResultFile, ErrorFile, ResultFile + ".tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", name, err)
		}
	}
}
