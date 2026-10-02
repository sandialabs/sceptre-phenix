package mm

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// forgetFilesDirectory drops the files directory minimega gave, and when it
// was last asked, now and when the test ends.
func forgetFilesDirectory(t *testing.T) {
	t.Helper()

	forget := func() {
		filesDir.mu.Lock()
		filesDir.dir, filesDir.askedAt, filesDir.given = "", time.Time{}, make(chan struct{})
		filesDir.mu.Unlock()
	}

	forget()
	t.Cleanup(forget)
}

func TestGetMMFullPath(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var answering atomic.Bool

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != "args" || !answering.Load() {
			return nil
		}

		return []*minicli.Response{
			mmtest.Tabular("head", []string{"base", "filepath"}, []string{"/tmp/minimega", "/srv/files/"}),
		}
	})

	forgetFilesDirectory(t)

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the default files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	// minimega not answering: the base directory's images, asking minimega
	// once rather than on every call
	for path, want := range map[string]string{
		"":                     base + "/images",
		"win/win10.qcow2":      base + "/images/win/win10.qcow2",
		"win/../a.qc2":         base + "/images/a.qc2",
		"/data/./vms/../o.qc2": "/data/o.qc2",
	} {
		if got := GetMMFullPath(path); got != want {
			t.Errorf("GetMMFullPath(%q) = %q, want %q", path, got, want)
		}
	}

	if got := mmtest.Count(received(), "args"); got != 1 {
		t.Fatalf("asked minimega for its args %d times, want once", got)
	}

	select {
	case <-FilesDirectoryGiven():
		t.Fatal("files directory reported given while minimega was not answering")
	default:
	}

	// once minimega answers, its files directory is used and kept
	answering.Store(true)
	forgetFilesDirectory(t)

	given := FilesDirectoryGiven()

	for range 2 {
		if got := GetMMFullPath("win/win10.qcow2"); got != "/srv/files/win/win10.qcow2" {
			t.Fatalf("GetMMFullPath with minimega answering = %q, want /srv/files/win/win10.qcow2", got)
		}
	}

	select {
	case <-given:
	default:
		t.Fatal("files directory not reported given once minimega gave it")
	}

	answering.Store(false)

	if got := GetMMFullPath(""); got != "/srv/files" {
		t.Fatalf("GetMMFullPath(\"\") after minimega answered = %q, want /srv/files", got)
	}

	if got := mmtest.Count(received(), "args"); got != 2 {
		t.Fatalf("asked minimega for its args %d times, want twice", got)
	}
}

// A backing file named relative to its overlay is reported with ".."
// segments; cleaning them as text is right unless one follows a symlinked
// directory.
func TestCleanBackingPath(t *testing.T) {
	t.Parallel()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"f/sub/deeper", "f/base.qc2", "elsewhere/dir", "elsewhere/base.qc2", "f/sub/base.qc2"} {
		p = filepath.Join(dir, p)

		if filepath.Ext(p) == "" {
			if err := os.MkdirAll(p, 0o750); err != nil {
				t.Fatal(err)
			}

			continue
		}

		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Symlink(filepath.Join(dir, "elsewhere", "dir"), filepath.Join(dir, "f", "sub", "sym")); err != nil {
		t.Fatal(err)
	}

	for raw, want := range map[string]string{
		dir + "/f/sub/deeper/../../base.qc2": dir + "/f/base.qc2",
		dir + "/f/base.qc2":                  dir + "/f/base.qc2",
		dir + "/f/sub/sym/../base.qc2":       dir + "/elsewhere/base.qc2",
		dir + "/f/sub/missing/../x.qc2":      dir + "/f/sub/x.qc2",
	} {
		if got := CleanBackingPath(raw); got != want {
			t.Errorf("CleanBackingPath(%q) = %q, want %q", raw, got, want)
		}
	}
}
