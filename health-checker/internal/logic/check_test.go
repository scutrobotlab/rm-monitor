package logic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckRecordsWritable(t *testing.T) {
	dir := t.TempDir()
	if err := checkRecordsWritable(dir); err != nil {
		t.Fatalf("checkRecordsWritable() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rm-monitor-health-check")); !os.IsNotExist(err) {
		t.Fatalf("health check marker should be removed, stat err=%v", err)
	}
}

func TestCheckRecordsWritableFailsForMissingDir(t *testing.T) {
	if err := checkRecordsWritable(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("checkRecordsWritable() should fail for missing directory")
	}
}
