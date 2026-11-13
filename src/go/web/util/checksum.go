package util

import (
	"encoding/hex"
	"fmt"
	"os"
)

// FileChecksum computes the SHA-256 checksum of the file at path, named display, at once, as the checksum routes
// report it, without Digests. It fails for a file that cannot be read, is not regular (ErrNotRegular) or changed
// while hashed (ErrChangedWhileHashing).
func FileChecksum(path, display string) (Checksum, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Checksum{}, fmt.Errorf("reading file for checksum: %w", err)
	}

	if !info.Mode().IsRegular() {
		return Checksum{}, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}

	sum, err := hashFile(keyOf(path, info))
	if err != nil {
		return Checksum{}, err
	}

	return Checksum{
		Path: display, Size: info.Size(), Modified: info.ModTime(), ETag: FileETag(info), SHA256: hex.EncodeToString(sum),
	}, nil
}
