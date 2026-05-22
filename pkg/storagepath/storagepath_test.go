package storagepath

import (
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{
			name: "relative path",
			base: "/records",
			path: "Event/Zone/file.flv",
			want: filepath.Join("/records", "Event", "Zone", "file.flv"),
		},
		{
			name: "absolute path",
			base: "/records",
			path: "/records/Event/Zone/file.flv",
			want: filepath.Join(string(filepath.Separator), "records", "Event", "Zone", "file.flv"),
		},
		{
			name: "empty path",
			base: "/records",
			path: "",
			want: filepath.Clean("/records"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.base, tt.path); got != tt.want {
				t.Fatalf("Resolve(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}
