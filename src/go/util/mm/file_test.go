package mm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

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

	if err := os.Symlink(filepath.Join(dir, "elsewhere", "dir"), filepath.Join(dir, "f", "sym2")); err != nil {
		t.Fatal(err)
	}

	for raw, want := range map[string]string{
		dir + "/f/sub/deeper/../../base.qc2": dir + "/f/base.qc2",
		dir + "/f/base.qc2":                  dir + "/f/base.qc2",
		dir + "/f/./base.qc2":                dir + "/f/base.qc2",
		dir + "//f//base.qc2":                dir + "/f/base.qc2",
		dir + "/f/sub/deeper/":               dir + "/f/sub/deeper",
		dir + "/f/sub/sym/../base.qc2":       dir + "/elsewhere/base.qc2",
		dir + "/f/sym2/../base.qc2":          dir + "/elsewhere/base.qc2",
		dir + "/f/sym2/../../f/base.qc2":     dir + "/f/base.qc2",
		dir + "/f/sub/missing/../x.qc2":      dir + "/f/sub/x.qc2",
		dir + "/f/sub/sym/../missing.qc2":    dir + "/f/sub/missing.qc2",
	} {
		t.Run(strings.TrimPrefix(raw, dir), func(t *testing.T) {
			t.Parallel()

			if got := CleanBackingPath(raw); got != want {
				t.Errorf("CleanBackingPath(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}

// fileUseBase points common.PhenixBase at a new directory until the test ends
// and returns the fallback files directory under it.
func fileUseBase(t *testing.T) string {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the fallback's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	return base + "/images"
}

// fileArgs answers `args` with the given responses.
func fileArgs(resps ...*minicli.Response) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == argsCmd {
			return resps
		}

		return nil
	}
}

func TestMinimegaFilesDirectory(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"base", "filepath"}

	for name, tc := range map[string]struct {
		answer  []*minicli.Response
		want    string
		wantErr string
	}{
		"cleans the filepath": {answer: []*minicli.Response{mmtest.Tabular("head", header, []string{"/b", "/srv/x/../files/"})}, want: "/srv/files"},
		"relative filepath":   {answer: []*minicli.Response{mmtest.Tabular("head", header, []string{"/b", "files"})}, wantErr: "minimega's args have no absolute filepath"},
		"empty filepath":      {answer: []*minicli.Response{mmtest.Tabular("head", header, []string{"/b", ""})}, wantErr: "minimega's args have no absolute filepath"},
		"no filepath column":  {answer: []*minicli.Response{mmtest.Tabular("head", []string{"base"}, []string{"/b"})}, wantErr: "minimega's args have no absolute filepath"},
		"no answer":           {answer: []*minicli.Response{mmtest.Error("head", "boom")}, wantErr: "no args returned"},
		"an answer from each node": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"/b", "/srv/files"}),
				mmtest.Tabular("compute1", header, []string{"/b", "/srv/files"}),
			},
			wantErr: "no args returned",
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, fileArgs(tc.answer...))

			got, err := minimegaFilesDirectory()

			if errText := fmt.Sprint(err); (err != nil || tc.wantErr != "") && errText != tc.wantErr {
				t.Errorf("minimegaFilesDirectory() error = %v, want %q", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("minimegaFilesDirectory() = %q, want %q", got, tc.want)
			}

			want := []mmtest.Command{{Raw: ".record false args", Namespace: "", Base: argsCmd}}
			if diff := cmp.Diff(want, received()); diff != "" {
				t.Errorf("minimegaFilesDirectory() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Once minimega could not give its files directory, phenix uses the fallback and
// asks again only after filesDirRetry.
func TestFilesDirectoryAsksAgainAfterRetry(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var answering atomic.Bool

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != argsCmd || !answering.Load() {
			return []*minicli.Response{mmtest.Tabular("head", []string{"filepath"}, []string{"relative"})}
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"filepath"}, []string{"/srv/files"})}
	})

	forgetFilesDirectory(t)

	fallback := fileUseBase(t)

	for range 2 {
		if got := GetMMFullPath("a.qc2"); got != fallback+"/a.qc2" {
			t.Errorf("GetMMFullPath(a.qc2) with minimega giving a relative filepath = %q, want %q", got, fallback+"/a.qc2")
		}
	}

	if got := mmtest.Count(received(), argsCmd); got != 1 {
		t.Errorf("GetMMFullPath twice within filesDirRetry sent %d args commands, want 1", got)
	}

	answering.Store(true)

	filesDir.mu.Lock()
	filesDir.askedAt = time.Now().Add(-filesDirRetry - time.Second)
	filesDir.mu.Unlock()

	if got, want := GetMMFullPath("a.qc2"), "/srv/files/a.qc2"; got != want {
		t.Errorf("GetMMFullPath(a.qc2) after filesDirRetry = %q, want %q", got, want)
	}

	if got := mmtest.Count(received(), argsCmd); got != 2 {
		t.Errorf("GetMMFullPath after filesDirRetry sent %d args commands in all, want 2", got)
	}

	select {
	case <-FilesDirectoryGiven():
	default:
		t.Error("FilesDirectoryGiven() is not closed once minimega gave its files directory")
	}
}

// A caller waiting on another caller's question gets the fallback when minimega
// cannot answer it.
func TestFilesDirectoryWaiterGetsFallbackOnFailure(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	var (
		release = make(chan struct{})
		asked   = make(chan struct{}, 1)
	)

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == argsCmd {
			asked <- struct{}{}
			<-release
		}

		return []*minicli.Response{mmtest.Error("head", "boom")}
	})

	forgetFilesDirectory(t)

	fallback := fileUseBase(t)
	finish := sync.OnceFunc(func() { close(release) })

	t.Cleanup(finish)

	first := make(chan string, 1)

	go func() { first <- GetMMFullPath("a.qc2") }()

	<-asked

	second := make(chan string, 1)

	go func() { second <- GetMMFullPath("b.qc2") }()

	select {
	case got := <-second:
		t.Fatalf("GetMMFullPath(b.qc2) = %q while minimega was asked, want it to wait", got)
	case <-time.After(100 * time.Millisecond):
	}

	finish()

	if got := <-first; got != fallback+"/a.qc2" {
		t.Errorf("GetMMFullPath(a.qc2) with minimega failing = %q, want %q", got, fallback+"/a.qc2")
	}

	if got := <-second; got != fallback+"/b.qc2" {
		t.Errorf("GetMMFullPath(b.qc2) waiting on a failed question = %q, want %q", got, fallback+"/b.qc2")
	}

	if got := mmtest.Count(received(), argsCmd); got != 1 {
		t.Errorf("two GetMMFullPath callers sent %d args commands, want 1", got)
	}
}
