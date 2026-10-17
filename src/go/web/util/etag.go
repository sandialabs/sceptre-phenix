package util

import (
	"io/fs"
	"strconv"
)

// FileETag is a strong validator for a regular file, built from its inode,
// size and modification time (in nanoseconds) rather than its contents, so it
// costs nothing to compute however large the file is. It changes whenever the
// file is rewritten, appended to, truncated or replaced (a renamed-in file
// has a new inode).
//
// It is strong on purpose: net/http only honors an If-Range ETag with a
// strong comparison, so a weak one would make every resumed download start
// over.
func FileETag(info fs.FileInfo) string {
	return `"` + strconv.FormatUint(fileInode(info), 16) +
		"-" + strconv.FormatInt(info.Size(), 16) +
		"-" + strconv.FormatInt(info.ModTime().UnixNano(), 16) + `"`
}
