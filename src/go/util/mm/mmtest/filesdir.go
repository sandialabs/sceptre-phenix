package mmtest

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

// ownProcessEnv names the variable set in the process that InOwnProcess starts.
const ownProcessEnv = "PHENIX_MMTEST_OWN_PROCESS"

// InOwnProcess runs the top-level test again, alone, in a new process of the test binary, and reports whether the
// caller is that process. The caller runs its body only then. Use it for a test whose fake minimega gives its files
// directory: phenix keeps that directory (see mm.GetMMFullPath) until the process ends.
func InOwnProcess(tb testing.TB) bool {
	tb.Helper()

	if os.Getenv(ownProcessEnv) == tb.Name() {
		return true
	}

	args := []string{"-test.run=^" + tb.Name() + "$", "-test.count=1", "-test.v"}

	cmd := exec.CommandContext(tb.Context(), os.Args[0], args...) //nolint:gosec // the test binary
	cmd.Env = append(os.Environ(), ownProcessEnv+"="+tb.Name())

	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("%s in its own process failed: %v\n%s", tb.Name(), err, out)
	}

	if !strings.Contains(string(out), "--- PASS: "+tb.Name()) {
		tb.Fatalf("%s did not run in its own process:\n%s", tb.Name(), out)
	}

	return false
}

// FileListHeader is the header of minimega's `file list`.
var FileListHeader = []string{"dir", "name", "size", "modified"} //nolint:gochecknoglobals // a fixed header

// ListFiles answers `file list <path> recursive` from host as minimega with -filepath dir does: a row for each file
// below <dir>/<path>, named by its path relative to dir. A path that does not exist lists as empty.
func ListFiles(tb testing.TB, host, dir, path string) *minicli.Response {
	tb.Helper()

	var rows [][]string

	err := filepath.WalkDir(filepath.Join(dir, path), func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err //nolint:wrapcheck // reported below
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err //nolint:wrapcheck // reported below
		}

		rows = append(rows, []string{"", rel, strconv.FormatInt(info.Size(), 10), info.ModTime().Format(time.RFC3339)})

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		tb.Errorf("listing %s in %s: %v", path, dir, err)
	}

	return Tabular(host, FileListHeader, rows...)
}
