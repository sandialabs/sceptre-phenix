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
		filesDir.dir, filesDir.askedAt, filesDir.given, filesDir.asking = "", time.Time{}, make(chan struct{}), nil
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
		t.Errorf("GetMMFullPath with minimega not answering sent %d args commands, want 1", got)
	}

	select {
	case <-FilesDirectoryGiven():
		t.Error("FilesDirectoryGiven() is closed while minimega was not answering")
	default:
	}

	// once minimega answers, its files directory is used and kept
	answering.Store(true)
	forgetFilesDirectory(t)

	given := FilesDirectoryGiven()

	for range 2 {
		if got, want := GetMMFullPath("win/win10.qcow2"), "/srv/files/win/win10.qcow2"; got != want {
			t.Errorf("GetMMFullPath(%q) with minimega answering = %q, want %q", "win/win10.qcow2", got, want)
		}
	}

	select {
	case <-given:
	default:
		t.Error("FilesDirectoryGiven() is not closed once minimega gave its files directory")
	}

	answering.Store(false)

	if got, want := GetMMFullPath(""), "/srv/files"; got != want {
		t.Errorf("GetMMFullPath(%q) after minimega answered = %q, want %q", "", got, want)
	}

	if got := mmtest.Count(received(), "args"); got != 2 {
		t.Errorf("GetMMFullPath sent %d args commands in all, want 2", got)
	}
}

// While minimega is slow to give its files directory, FilesDirectoryGiven answers at once and
// GetMMFullPath callers wait for the answer rather than take a fallback minimega does not use.
func TestFilesDirectoryDoesNotWaitOnASlowMinimega(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var (
		release = make(chan struct{})
		asked   = make(chan struct{}, 1)
	)

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != "args" {
			return nil
		}

		asked <- struct{}{}
		<-release

		return []*minicli.Response{
			mmtest.Tabular("head", []string{"base", "filepath"}, []string{"/tmp/minimega", "/srv/files/"}),
		}
	})

	forgetFilesDirectory(t)

	base := t.TempDir()

	originalBase := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the fallback's parent

	t.Cleanup(func() { common.PhenixBase = originalBase }) //nolint:reassign // restore

	first := make(chan string, 1)

	go func() { first <- GetMMFullPath("a.qc2") }()

	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("GetMMFullPath(a.qc2) did not ask minimega for its args")
	}

	given := make(chan struct{})

	go func() {
		_ = FilesDirectoryGiven()
		close(given)
	}()

	select {
	case <-given:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("FilesDirectoryGiven() waited on a slow minimega")
	}

	second := make(chan string, 1)

	go func() { second <- GetMMFullPath("b.qc2") }()

	// minimega is still busy, so an answer now would be the fallback
	select {
	case got := <-second:
		close(release)
		t.Fatalf("GetMMFullPath(b.qc2) = %q before minimega answered, want it to wait", got)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	if got, want := <-first, "/srv/files/a.qc2"; got != want {
		t.Errorf("GetMMFullPath(a.qc2) asking minimega = %q, want %q", got, want)
	}

	select {
	case got := <-second:
		if want := "/srv/files/b.qc2"; got != want {
			t.Errorf("GetMMFullPath(b.qc2) waiting on minimega = %q, want %q, not the fallback under %s", got, want, base)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetMMFullPath(b.qc2) still waits after minimega answered")
	}

	if got, want := GetMMFullPath("a.qc2"), "/srv/files/a.qc2"; got != want {
		t.Errorf("GetMMFullPath(a.qc2) after minimega answered = %q, want %q", got, want)
	}
}

// A backing file named relative to its overlay has ".." segments; cleaning them as text is right
// unless one follows a symlinked directory.
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
