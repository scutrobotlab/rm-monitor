package storagepath

import (
	"path/filepath"
	"strings"
)

// Resolve returns a filesystem path under base unless p is already absolute.
// Stored task paths are normally slash-separated and relative to the records
// volume, but local overrides and manual test rows may provide /records/...
// directly.
func Resolve(base, p string) string {
	if p == "" {
		return filepath.Clean(base)
	}
	if strings.HasPrefix(filepath.ToSlash(p), "/") {
		return filepath.Clean(filepath.FromSlash(p))
	}
	return filepath.Join(base, filepath.FromSlash(p))
}
