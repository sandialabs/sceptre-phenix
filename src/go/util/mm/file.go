package mm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"phenix/util/common"
	"phenix/util/plog"
)

// filesDirRetry is how long phenix waits, after minimega could not give its
// files directory, before asking again.
const filesDirRetry = time.Minute

//nolint:gochecknoglobals // the process's view of minimega's files directory
var filesDir = struct {
	mu      sync.Mutex
	dir     string        // minimega's -filepath, once minimega gave it
	askedAt time.Time     // when minimega last could not give it
	given   chan struct{} // closed once minimega gave it
}{mu: sync.Mutex{}, dir: "", askedAt: time.Time{}, given: make(chan struct{})}

// GetMMFullPath returns path cleaned when it is absolute, and otherwise
// joined onto the minimega files directory: minimega's -filepath, or
// <PhenixBase>/images until minimega gives it.
func GetMMFullPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return filepath.Clean(path)
	}

	return filepath.Join(filesDirectory(), path)
}

// CleanBackingPath returns the absolute path p without its "." and ".."
// segments, naming the file the kernel opens for p. qemu-img and minimega
// report a backing file as its overlay's directory joined with the stored
// (often relative) name, so p may hold ".." segments. That is
// [filepath.Clean] of p unless a ".." follows a symlinked directory, which the
// kernel leaves from the link's target rather than from the directory holding
// the link; then p is returned with every symlink resolved. A file phenix
// cannot stat keeps the cleaned form.
func CleanBackingPath(p string) string {
	clean := filepath.Clean(p)
	if clean == p {
		return clean
	}

	rawInfo, rawErr := os.Stat(p)
	if rawErr != nil {
		return clean
	}

	if cleanInfo, err := os.Stat(clean); err == nil && os.SameFile(cleanInfo, rawInfo) {
		return clean
	}

	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return clean
	}

	return resolved
}

// FilesDirectoryGiven returns a channel closed once minimega has given its
// files directory, from when GetMMFullPath no longer changes.
func FilesDirectoryGiven() <-chan struct{} {
	filesDir.mu.Lock()
	defer filesDir.mu.Unlock()

	return filesDir.given
}

// filesDirectory returns minimega's files directory. It asks minimega on first
// use rather than at startup, when neither the configured base directories
// nor minimega may be ready, and keeps the answer. Until minimega answers it
// returns <PhenixBase>/images, asking again at most once every filesDirRetry.
func filesDirectory() string {
	filesDir.mu.Lock()
	defer filesDir.mu.Unlock()

	if filesDir.dir != "" {
		return filesDir.dir
	}

	fallback := common.PhenixBase + "/images"

	if !filesDir.askedAt.IsZero() && time.Since(filesDir.askedAt) < filesDirRetry {
		return fallback
	}

	dir, err := minimegaFilesDirectory()
	if err != nil {
		filesDir.askedAt = time.Now()

		plog.Warn(plog.TypeSystem, "minimega did not give its files directory", "using", fallback, "err", err)

		return fallback
	}

	filesDir.dir = dir
	close(filesDir.given)

	return dir
}

// minimegaFilesDirectory asks minimega itself, whatever DefaultMM is, for its
// -filepath.
func minimegaFilesDirectory() (string, error) {
	args, err := Minimega{}.GetMMArgs()
	if err != nil {
		return "", err
	}

	dir := args["filepath"]
	if !filepath.IsAbs(dir) {
		return "", errors.New("minimega's args have no absolute filepath")
	}

	return filepath.Clean(dir), nil
}
