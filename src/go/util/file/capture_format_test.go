package file

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/util/testenv"
)

// captureFormatSeeds are the first bytes of files of each capture format, and of files of none.
//
//nolint:gochecknoglobals // read-only test table
var captureFormatSeeds = map[string]string{
	"\xa1\xb2\xc3\xd4":         mm.CaptureFormatPcap,
	"\xd4\xc3\xb2\xa1\x02\x00": mm.CaptureFormatPcap,
	"\xa1\xb2\x3c\x4d":         mm.CaptureFormatPcap,
	"\x4d\x3c\xb2\xa1":         mm.CaptureFormatPcap,
	"\x0a\x0d\x0d\x0a\x00\x00\x00\x1c\x1a\x2b\x3c\x4d": mm.CaptureFormatPcapng,
	"\x0a\x0d\x0d\x0a\x1c\x00\x00\x00\x4d\x3c\x2b\x1a": mm.CaptureFormatPcapng,
	"\x0a\x0d\x0d\x0a\x1c\x00\x00\x00\x4d\x3c\x2b":     "",
	"\x0a\x0d\x0d\x0a\x1c\x00\x00\x00\x00\x00\x00\x00": "",
	"\xa1\xb2\xc3": "",
	"":             "",
	"GIF89a":       "",
}

// modelCaptureFormat is captureFormatOf written another way: from the magic byte sequences.
func modelCaptureFormat(b []byte) string {
	for _, magic := range []string{"\xa1\xb2\xc3\xd4", "\xd4\xc3\xb2\xa1", "\xa1\xb2\x3c\x4d", "\x4d\x3c\xb2\xa1"} {
		if bytes.HasPrefix(b, []byte(magic)) {
			return mm.CaptureFormatPcap
		}
	}

	if len(b) >= captureMagicBytes && bytes.HasPrefix(b, []byte("\x0a\x0d\x0d\x0a")) {
		if order := string(b[8:12]); order == "\x1a\x2b\x3c\x4d" || order == "\x4d\x3c\x2b\x1a" {
			return mm.CaptureFormatPcapng
		}
	}

	return ""
}

func TestCaptureFormatOf(t *testing.T) {
	t.Parallel()

	for b, want := range captureFormatSeeds {
		if got := captureFormatOf([]byte(b)); got != want {
			t.Errorf("captureFormatOf(%q) = %q, want %q", b, got, want)
		}
	}
}

// captureFormatOf reads only the first captureMagicBytes bytes, and agrees with the model.
func TestCaptureFormatOfProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 0, 32).Draw(t, "bytes")

		got := captureFormatOf(b)
		if want := modelCaptureFormat(b); got != want {
			t.Fatalf("captureFormatOf(%q) = %q, want %q", b, got, want)
		}

		if head := captureFormatOf(b[:min(len(b), captureMagicBytes)]); head != got {
			t.Fatalf("captureFormatOf(%q) = %q, but of its first %d bytes %q", b, got, captureMagicBytes, head)
		}
	})
}

func TestSniffedName(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"a.pcap": true, "sub/b.PCAPNG": true, "c.cap": true, "noext": true, "dir.d/noext": true,
		"a.pcap.gz": false, "a.log": false, ".hidden": false,
	}

	for name, want := range tests {
		if got := sniffedName(name); got != want {
			t.Errorf("sniffedName(%q) = %t, want %t", name, got, want)
		}
	}
}

// useFilesDir points common.PhenixBase at a temporary tree whose files directory of the experiment exp holds files,
// by path, and returns that directory.
func useFilesDir(t *testing.T, files map[string]string) string {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // point at a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	dir := filepath.Join(base, "images", "exp", "files")

	for p, contents := range files {
		writeFile(t, filepath.Join(dir, p), contents)
	}

	return dir
}

func writeFile(t *testing.T, local, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
		t.Fatalf("creating the directory of %s: %v", local, err)
	}

	if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", local, err)
	}
}

// The experiment file listing gives the capture format of each headnode file named as a capture or with no extension,
// from its first bytes. It never reads a file through a symbolic link, and reads the files once for each listing.
func TestListingCaptureFormats(t *testing.T) { //nolint:paralleltest // replaces package state
	installListing(t, nil)
	experimentListings.invalidateAll()

	var (
		pcapLE   = "\xd4\xc3\xb2\xa1rest of the header"
		pcapngBE = "\x0a\x0d\x0d\x0a\x00\x00\x00\x1c\x1a\x2b\x3c\x4d"
		pcapngLE = "\x0a\x0d\x0d\x0a\x1c\x00\x00\x00\x4d\x3c\x2b\x1a"
	)

	files := useFilesDir(t, map[string]string{
		"a.pcap": pcapLE, "b.pcapng": pcapngBE, "C.CAP": pcapngLE, "noext": pcapLE, "sub/d.pcapng": pcapngLE,
		"e.log": pcapLE, "f.pcap.gz": pcapLE, "short.pcapng": pcapngBE[:8], "text.pcap": "not a capture",
	})

	outside := filepath.Join(t.TempDir(), "outside.pcap")
	writeFile(t, outside, pcapLE)

	for link, target := range map[string]string{"link.pcap": "a.pcap", "out.pcap": outside, "linkdir": "sub"} {
		if err := os.Symlink(target, filepath.Join(files, link)); err != nil {
			t.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
		}
	}

	row := func(name string) []string { return []string{"", "exp/files/" + name, "1", "2026-01-02T03:04:05Z"} }

	headPaths := []string{
		"a.pcap", "b.pcapng", "C.CAP", "noext", "sub/d.pcapng", "e.log", "f.pcap.gz", "short.pcapng", "text.pcap",
		"link.pcap", "out.pcap", "linkdir/d.pcapng",
	}

	headRows := make([][]string, 0, len(headPaths))
	for _, p := range headPaths {
		headRows = append(headRows, row(p))
	}

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "mesh send all file list"):
			return []*minicli.Response{mmtest.Tabular("compute1", listingHeader, row("remote.pcap"))}
		case strings.HasPrefix(cmd.Base, "file list"):
			return []*minicli.Response{mmtest.Tabular("head", listingHeader, headRows...)}
		}

		return nil
	})

	formats := func() map[string]string {
		t.Helper()

		got, _, err := MMClusterFiles{}.GetExperimentFilesObserved(t.Context(), "exp", "")
		if err != nil {
			t.Fatalf("GetExperimentFilesObserved(exp) error = %v", err)
		}

		formats := make(map[string]string, len(got))
		for _, f := range got {
			formats[f.Path] = f.CaptureFormat
		}

		return formats
	}

	want := map[string]string{
		"a.pcap": "pcap", "b.pcapng": "pcapng", "C.CAP": "pcapng", "noext": "pcap", "sub/d.pcapng": "pcapng",
		"e.log": "", "f.pcap.gz": "", "short.pcapng": "", "text.pcap": "", "link.pcap": "", "out.pcap": "",
		"linkdir/d.pcapng": "", "remote.pcap": "",
	}

	if diff := cmp.Diff(want, formats()); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) capture formats mismatch (-want +got):\n%s", diff)
	}

	writeFile(t, filepath.Join(files, "text.pcap"), pcapLE)

	if diff := cmp.Diff(want, formats()); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) of the cached listing capture formats mismatch (-want +got):\n%s", diff)
	}

	InvalidateExperimentFiles("exp")

	want["text.pcap"] = mm.CaptureFormatPcap

	if diff := cmp.Diff(want, formats()); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) after invalidation capture formats mismatch (-want +got):\n%s", diff)
	}
}

// The listing reads the capture formats from minimega's files directory, which can be other than
// <base-dir.phenix>/images.
func TestListingCaptureFormatsInMinimegaFilesDirectory(t *testing.T) { //nolint:paralleltest // replaces package state
	if !mmtest.InOwnProcess(t) {
		return
	}

	pcap := "\xd4\xc3\xb2\xa1rest of the header"

	useFilesDir(t, map[string]string{"a.pcap": pcap})

	filesDir := t.TempDir()
	writeFile(t, filepath.Join(filesDir, "exp", "files", "sub", "b.pcap"), pcap)

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "args":
			return []*minicli.Response{mmtest.Tabular("head", []string{"filepath"}, []string{filesDir})}
		case strings.HasPrefix(cmd.Base, "mesh send all file list"):
			return []*minicli.Response{mmtest.Tabular("compute1", listingHeader)}
		case strings.HasPrefix(cmd.Base, "file list "):
			return []*minicli.Response{mmtest.ListFiles(t, "head", filesDir, strings.Fields(cmd.Base)[2])}
		}

		return nil
	})

	got, _, err := MMClusterFiles{}.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err != nil {
		t.Fatalf("GetExperimentFilesObserved(exp) error = %v", err)
	}

	formats := make(map[string]string, len(got))
	for _, f := range got {
		formats[f.Path] = f.CaptureFormat
	}

	if diff := cmp.Diff(map[string]string{"sub/b.pcap": mm.CaptureFormatPcap}, formats); diff != "" {
		t.Errorf("GetExperimentFilesObserved(exp) capture formats mismatch (-want +got):\n%s", diff)
	}
}

// addCaptureFormats reads no file once ctx is done, and says so.
func TestAddCaptureFormatsStopsWhenCanceled(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, map[string]string{"a.pcap": "\xa1\xb2\xc3\xd4"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	files := Files{{Name: "a.pcap", Path: "a.pcap"}}

	if err := addCaptureFormats(ctx, "exp", files); !errors.Is(err, context.Canceled) {
		t.Errorf("addCaptureFormats(canceled, exp) error = %v, want %v", err, context.Canceled)
	}

	if got := files[0].CaptureFormat; got != "" {
		t.Errorf("addCaptureFormats(canceled, exp) set capture format %q, want none", got)
	}
}

// A capture the headnode cannot read lists with no capture format.
func TestListingCaptureFormatsOfUnreadableFile(t *testing.T) { //nolint:paralleltest // replaces package state
	if os.Geteuid() == 0 {
		testenv.Skipf(t, "root reads every file")
	}

	installListing(t, nil)
	experimentListings.invalidateAll()

	files := useFilesDir(t, map[string]string{"a.pcap": "\xa1\xb2\xc3\xd4", "locked.pcap": "\xa1\xb2\xc3\xd4"})

	locked := filepath.Join(files, "locked.pcap")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("os.Chmod(%s) error = %v", locked, err)
	}

	got := Files{{Name: "a.pcap", Path: "a.pcap"}, {Name: "locked.pcap", Path: "locked.pcap"}}
	if err := addCaptureFormats(t.Context(), "exp", got); err != nil {
		t.Fatalf("addCaptureFormats(exp) error = %v", err)
	}

	formats := map[string]string{got[0].Path: got[0].CaptureFormat, got[1].Path: got[1].CaptureFormat}
	if diff := cmp.Diff(map[string]string{"a.pcap": mm.CaptureFormatPcap, "locked.pcap": ""}, formats); diff != "" {
		t.Errorf("addCaptureFormats(exp) capture formats mismatch (-want +got):\n%s", diff)
	}
}

// LocalCaptureFormat gives the capture format of a regular headnode file from its first bytes, and none for a file it
// cannot read, a directory, a symbolic link or a missing file.
func TestLocalCaptureFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for name, contents := range map[string]string{
		"a.pcap": "\xd4\xc3\xb2\xa1rest", "b": "\x0a\x0d\x0d\x0a\x00\x00\x00\x1c\x1a\x2b\x3c\x4d", "c.pcap": "text",
		"sub/d.pcap": "\xa1\xb2\xc3\xd4",
	} {
		writeFile(t, filepath.Join(dir, name), contents)
	}

	if err := os.Symlink("a.pcap", filepath.Join(dir, "link.pcap")); err != nil {
		t.Fatalf("os.Symlink(a.pcap, link.pcap) error = %v", err)
	}

	want := map[string]string{
		"a.pcap": mm.CaptureFormatPcap, "b": mm.CaptureFormatPcapng, "c.pcap": "", "sub": "", "link.pcap": "",
		"missing.pcap": "",
	}

	if os.Geteuid() != 0 {
		writeFile(t, filepath.Join(dir, "locked.pcap"), "\xa1\xb2\xc3\xd4")

		if err := os.Chmod(filepath.Join(dir, "locked.pcap"), 0); err != nil {
			t.Fatalf("os.Chmod(locked.pcap) error = %v", err)
		}

		want["locked.pcap"] = ""
	}

	got := make(map[string]string, len(want))
	for name := range want {
		got[name] = LocalCaptureFormat(filepath.Join(dir, name))
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LocalCaptureFormat mismatch (-want +got):\n%s", diff)
	}
}

// A listing whose only caller leaves while the capture formats are read fails with the caller's error, and the cache
// keeps no listing from it.
func TestListingStopsReadingCaptureFormatsOnceNoOneWaits(t *testing.T) { //nolint:paralleltest // replaces package state
	installListing(t, nil)
	experimentListings.invalidateAll()
	useFilesDir(t, map[string]string{"a.pcap": "\xa1\xb2\xc3\xd4"})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	var lists atomic.Int32

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if !strings.HasPrefix(cmd.Base, "file list") {
			return nil
		}

		// the caller leaves once minimega has listed the files, before their formats are read
		if lists.Add(1) == 1 {
			cancel()
			polltest.Until(t, "the listing has no caller", func() bool {
				return experimentListings.flights.Waiting(listingKey("exp")) == 0
			})
		}

		return []*minicli.Response{mmtest.Tabular("head", listingHeader,
			[]string{"", "exp/files/a.pcap", "4", "2026-01-02T03:04:05Z"})}
	})

	if _, _, err := (MMClusterFiles{}).GetExperimentFilesObserved(ctx, "exp", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("GetExperimentFilesObserved(exp) of a caller that left error = %v, want %v", err, context.Canceled)
	}

	got, _, err := MMClusterFiles{}.GetExperimentFilesObserved(t.Context(), "exp", "")
	if err != nil {
		t.Fatalf("GetExperimentFilesObserved(exp) again error = %v", err)
	}

	if n := lists.Load(); n != 2 {
		t.Errorf("headnode listings after the canceled listing = %d, want 2", n)
	}

	if len(got) != 1 || got[0].CaptureFormat != mm.CaptureFormatPcap {
		t.Errorf("GetExperimentFilesObserved(exp) again = %+v, want a.pcap as %s", got, mm.CaptureFormatPcap)
	}
}

// An open file that cannot be checked, or is not a regular file, has no capture format.
func TestOpenCaptureFormatOfUncheckableFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path := filepath.Join(dir, "closed.pcap")
	if err := os.WriteFile(path, []byte("\xa1\xb2\xc3\xd4"), 0o600); err != nil {
		t.Fatal(err)
	}

	closed, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	if got := openCaptureFormat(closed); got != "" {
		t.Errorf("openCaptureFormat(closed pcap file) = %q, want \"\"", got)
	}

	directory, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = directory.Close() })

	if got := openCaptureFormat(directory); got != "" {
		t.Errorf("openCaptureFormat(directory) = %q, want \"\"", got)
	}
}
