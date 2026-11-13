package file

import (
	"path/filepath"
	"strings"
)

// WithinDir reports whether path, once cleaned, is dir or lies under it, so
// neither "../" segments nor a sibling sharing dir's prefix escape it. It
// compares the paths as text, without resolving symlinks. It returns false
// when one path is absolute and the other is relative.
func WithinDir(dir, path string) bool {
	if filepath.IsAbs(dir) != filepath.IsAbs(path) {
		return false
	}

	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
