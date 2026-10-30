package util

import (
	"io/fs"
	"strconv"
)

// FileETag is a strong validator for a regular file built from its inode, size and modification time, so it costs
// nothing however large the file is and changes whenever the file is rewritten or replaced. It is strong because
// net/http honors only a strong If-Range ETag, which resumed downloads need.
func FileETag(info fs.FileInfo) string {
	return `"` + strconv.FormatUint(fileInode(info), 16) +
		"-" + strconv.FormatInt(info.Size(), 16) +
		"-" + strconv.FormatInt(info.ModTime().UnixNano(), 16) + `"`
}
