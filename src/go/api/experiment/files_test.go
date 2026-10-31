package experiment_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
)

// listedFiles lists experiment files, copies mesh-only ones to the headnode and records deletions;
// other ClusterFiles methods panic.
type listedFiles struct {
	file.ClusterFiles

	files    file.Files
	remote   map[string]string // contents of the files only a mesh node has, by path
	listings int
	deleted  []string
	err      error
}

func (l *listedFiles) GetExperimentFiles(string, string) (file.Files, error) {
	l.listings++

	return l.files, nil
}

// CopyFile copies /<exp>/files/<path> from the mesh node that has it to the
// headnode's files directory.
func (l *listedFiles) CopyFile(src, _ string, _ file.CopyStatus) error {
	exp, p, _ := strings.Cut(strings.TrimPrefix(src, "/"), "/files/")

	contents, ok := l.remote[p]
	if !ok {
		return errors.New("no mesh node has " + src)
	}

	local := filepath.Join(common.PhenixBase, "images", exp, "files", p)

	if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
		return err
	}

	return os.WriteFile(local, []byte(contents), 0o600)
}

func (l *listedFiles) DeleteExistingFiles(names []string) error {
	l.deleted = append(l.deleted, names...)

	return l.err
}

func useListedFiles(t *testing.T, files ...file.File) *listedFiles {
	t.Helper()

	fake := &listedFiles{files: files}

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = fake //nolint:reassign // install test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double

	return fake
}

// useExperiment makes the store hold the experiment, running or not.
func useExperiment(t *testing.T, name string, running bool) {
	t.Helper()

	ctrl := gomock.NewController(t)

	status := map[string]any{}
	if running {
		status["startTime"] = "2024-01-01T00:00:00Z"
	}

	c := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec: map[string]any{
			"experimentName": name,
			"baseDir":        t.TempDir(),
			"topology":       map[string]any{"nodes": []map[string]any{}},
		},
		Status: status,
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	original := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double
}

// capturingMM reports one capture writing to path and counts lookups; other mm.MM methods panic.
type capturingMM struct {
	mm.MM

	path    string
	lookups int
}

func (c *capturingMM) GetExperimentCaptures(...mm.Option) []mm.Capture {
	c.lookups++

	return []mm.Capture{{VM: "vm", Interface: 0, Filepath: c.path}}
}

func useCapture(t *testing.T, path string) *capturingMM {
	t.Helper()

	fake := &capturingMM{path: path}

	original := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	return fake
}

func TestLocalFileFindsTheExperimentsFile(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := map[string]struct {
		running  bool
		path     string
		want     string
		err      error
		listings int // of the cluster's files
		lookups  int // of the experiment's captures
	}{
		"on the headnode, without listing the cluster's files": {
			running: true, path: "scorch/run-0/out.json", want: `{"ok":true}`,
		},
		"only on a mesh node, copied to the headnode": {running: true, path: "remote.log", want: "remote", listings: 1},
		"not one of the experiment's files":           {running: true, path: "missing.log", err: experiment.ErrFileNotFound, listings: 1},
		"a pcap no capture is writing":                {running: true, path: "old.pcap", want: "old", lookups: 1},
		"a pcap a capture is still writing":           {running: true, path: "live.pcap", err: mm.ErrCaptureExists, lookups: 1},
		// a stopped experiment has no captures, and asking minimega about one
		// would recreate its namespace
		"a pcap of a stopped experiment":    {running: false, path: "live.pcap", want: "live"},
		"a path out of the files directory": {path: "../../etc/passwd", err: experiment.ErrInvalidFilePath},
		"a path through a parent":           {path: "scorch/../../x", err: experiment.ErrInvalidFilePath},
		"an absolute path":                  {path: "/scorch/run-0/out.json", err: experiment.ErrInvalidFilePath},
		"an empty path":                     {path: "", err: experiment.ErrInvalidFilePath},
		"a doubled slash":                   {path: "scorch//run-0/out.json", err: experiment.ErrInvalidFilePath},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useExperiment(t, "exp", tc.running)
			captures := useCapture(t, "/phenix/images/exp/files/live.pcap")
			useFilesDir(t, "exp", map[string]string{
				"scorch/run-0/out.json": `{"ok":true}`,
				"old.pcap":              "old",
				"live.pcap":             "live",
			})

			files := useListedFiles(t, file.File{Name: "remote.log", Path: "remote.log"})
			files.remote = map[string]string{"remote.log": "remote"}

			local, err := experiment.LocalFile("exp", tc.path)

			var got []byte
			if err == nil {
				got, err = os.ReadFile(local)
			}

			switch {
			case tc.err != nil && !errors.Is(err, tc.err):
				t.Errorf("LocalFile(exp, %q) = %q, %v; want error %v", tc.path, local, err, tc.err)
			case tc.err == nil && (err != nil || string(got) != tc.want):
				t.Errorf("LocalFile(exp, %q) = %q holding %q, %v; want a file holding %q, nil", tc.path, local, got, err, tc.want)
			}

			if files.listings != tc.listings {
				t.Errorf("LocalFile(exp, %q) GetExperimentFiles calls = %d, want %d", tc.path, files.listings, tc.listings)
			}

			if captures.lookups != tc.lookups {
				t.Errorf("LocalFile(exp, %q) GetExperimentCaptures calls = %d, want %d", tc.path, captures.lookups, tc.lookups)
			}
		})
	}
}

func TestDeleteFilesDeletesListedFilesAndRefusesTheRest(t *testing.T) { //nolint:paralleltest // replaces package state
	useExperiment(t, "exp", true)
	useCapture(t, "/phenix/images/exp/files/live.pcap")

	fake := useListedFiles(t,
		file.File{Name: "a.log", Path: "a.log"},
		file.File{Name: "b.json", Path: "scorch/run-0/b.json"},
		file.File{Name: "old.pcap", Path: "old.pcap"},
		file.File{Name: "live.pcap", Path: "live.pcap"},
		file.File{Name: "ive.pcap", Path: "ive.pcap"},
		file.File{Name: "live.pcap", Path: "sub/live.pcap"},
		file.File{Name: "a*.log", Path: "a*.log"},
		file.File{Name: "x #y", Path: "x #y"},
	)

	deleted, refused, err := experiment.DeleteFiles("exp", []string{
		"a.log", "scorch/run-0/b.json", "old.pcap", "a.log", "live.pcap", "ive.pcap", "sub/live.pcap",
		"a*.log", "x #y", "missing.txt", "scorch", "../exp2/files/a.log", "/a.log", "",
	})
	if err != nil {
		t.Fatalf("DeleteFiles(exp, ...) error = %v", err)
	}

	// only the file the capture writes is refused, not one whose name or base
	// name resembles it
	wantDeleted := []string{"a.log", "scorch/run-0/b.json", "old.pcap", "ive.pcap", "sub/live.pcap"}
	if diff := cmp.Diff(wantDeleted, deleted); diff != "" {
		t.Errorf("DeleteFiles(exp, ...) deleted mismatch (-want +got):\n%s", diff)
	}

	wantNames := []string{
		"exp/files/a.log", "exp/files/scorch/run-0/b.json", "exp/files/old.pcap", "exp/files/ive.pcap",
		"exp/files/sub/live.pcap",
	}
	if diff := cmp.Diff(wantNames, fake.deleted); diff != "" {
		t.Errorf("DeleteFiles(exp, ...) DeleteExistingFiles names mismatch (-want +got):\n%s", diff)
	}

	wantRefused := map[string]error{
		"live.pcap":           mm.ErrCaptureExists,
		"a*.log":              experiment.ErrInvalidFilePath,
		"x #y":                experiment.ErrInvalidFilePath,
		"missing.txt":         experiment.ErrFileNotFound,
		"scorch":              experiment.ErrFileNotFound,
		"../exp2/files/a.log": experiment.ErrInvalidFilePath,
		"/a.log":              experiment.ErrInvalidFilePath,
		"":                    experiment.ErrInvalidFilePath,
	}

	if diff := cmp.Diff(wantRefused, refused, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("DeleteFiles(exp, ...) refused mismatch (-want +got):\n%s", diff)
	}

	var writing *experiment.CaptureWritingError
	if !errors.As(refused["live.pcap"], &writing) || writing.Capture.VM != "vm" {
		t.Errorf("DeleteFiles(exp, ...) refused live.pcap with %v, want a %T naming VM vm", refused["live.pcap"], writing)
	}
}

func TestCaptureFile(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/phenix/images/exp/files/a.pcap":    "a.pcap",
		"/other/base/exp/files/sub/a.pcap":   "sub/a.pcap",
		"/phenix/images/exp2/files/a.pcap":   "",
		"/phenix/images/myexp/files/a.pcap":  "",
		"/phenix/images/exp/files/":          "",
		"/phenix/images/exp/captures/a.pcap": "",
		"relative/exp/files/a.pcap":          "a.pcap",
	}

	for capturePath, want := range tests {
		got, ok := experiment.CaptureFile("exp", capturePath)
		if got != want || ok != (want != "") {
			t.Errorf("CaptureFile(exp, %q) = %q, %t; want %q, %t", capturePath, got, ok, want, want != "")
		}
	}
}

func TestDeleteFilesReportsDeleteFailure(t *testing.T) { //nolint:paralleltest // replaces package state
	fake := useListedFiles(t, file.File{Name: "a.log", Path: "a.log"})
	fake.err = errors.New("mesh down")

	deleted, _, err := experiment.DeleteFiles("exp", []string{"a.log"})
	if err == nil || deleted != nil {
		t.Errorf("DeleteFiles(exp, [a.log]) with a failing delete = %v, %v; want nil and the failure", deleted, err)
	}
}

func TestDeleteFileReturnsTheRefusal(t *testing.T) { //nolint:paralleltest // replaces package state
	fake := useListedFiles(t, file.File{Name: "a.log", Path: "a.log"})

	if err := experiment.DeleteFile("exp", "a.log"); err != nil {
		t.Errorf("DeleteFile(exp, a.log) error = %v", err)
	}

	if err := experiment.DeleteFile("exp", "b.log"); !errors.Is(err, experiment.ErrFileNotFound) {
		t.Errorf("DeleteFile(exp, b.log) error = %v, want %v", err, experiment.ErrFileNotFound)
	}

	if err := experiment.DeleteFile("bad*", "a.log"); !errors.Is(err, experiment.ErrInvalidFilePath) {
		t.Errorf("DeleteFile(bad*, a.log) error = %v, want %v", err, experiment.ErrInvalidFilePath)
	}

	if diff := cmp.Diff([]string{"exp/files/a.log"}, fake.deleted); diff != "" {
		t.Errorf("DeleteFile() DeleteExistingFiles names mismatch (-want +got):\n%s", diff)
	}
}

// useFilesDir points the headnode's files at a temporary tree holding files
// (path relative to the experiment's files directory: contents).
func useFilesDir(t *testing.T, exp string, files map[string]string) {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // point at a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	for p, contents := range files {
		local := filepath.Join(base, "images", exp, "files", p)

		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			t.Fatalf("creating the directory of %s: %v", p, err)
		}

		if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
}

var zipLimits = experiment.ZipLimits{MaxFiles: 10, MaxBytes: 1 << 20} //nolint:gochecknoglobals // test limits

type zipEntry struct {
	Name   string
	Method uint16
	Data   string
}

// readZip returns the archive's entries in order.
func readZip(t *testing.T, buf *bytes.Buffer) []zipEntry {
	t.Helper()

	archive, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}

	entries := make([]zipEntry, 0, len(archive.File))

	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening zip entry %s: %v", f.Name, err)
		}

		data, err := io.ReadAll(rc)
		_ = rc.Close()

		if err != nil {
			t.Fatalf("reading zip entry %s: %v", f.Name, err)
		}

		entries = append(entries, zipEntry{Name: f.Name, Method: f.Method, Data: string(data)})
	}

	return entries
}

func TestZipFilesArchivesTheFiles(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, "exp", map[string]string{
		"a.log":               "alpha",
		"scorch/run-0/b.json": `{"ok":true}`,
		"c.gz":                "gzipped",
		"d.qcow":              "qcow",
	})
	useListedFiles(t)

	var buf bytes.Buffer

	zipped, err := experiment.ZipFiles("exp", []string{"a.log", "scorch/run-0/b.json", "a.log", "c.gz", "d.qcow"}, zipLimits,
		func() io.Writer { return &buf })
	if err != nil {
		t.Fatalf("ZipFiles(exp, ...) error = %v", err)
	}

	if diff := cmp.Diff([]string{"a.log", "scorch/run-0/b.json", "c.gz", "d.qcow"}, zipped); diff != "" {
		t.Errorf("ZipFiles(exp, ...) zipped mismatch (-want +got):\n%s", diff)
	}

	// each file once, and the gzipped one and the disk image stored rather than compressed again
	want := []zipEntry{
		{Name: "a.log", Method: zip.Deflate, Data: "alpha"},
		{Name: "scorch/run-0/b.json", Method: zip.Deflate, Data: `{"ok":true}`},
		{Name: "c.gz", Method: zip.Store, Data: "gzipped"},
		{Name: "d.qcow", Method: zip.Store, Data: "qcow"},
	}
	if diff := cmp.Diff(want, readZip(t, &buf)); diff != "" {
		t.Errorf("ZipFiles(exp, ...) archive mismatch (-want +got):\n%s", diff)
	}
}

func TestZipFilesRefusesBeforeWriting(t *testing.T) { //nolint:paralleltest // replaces package state
	useExperiment(t, "exp", true)
	useCapture(t, "/phenix/images/exp/files/live.pcap")
	useFilesDir(
		t,
		"exp",
		map[string]string{
			"a.log":     "alpha",
			"big.bin":   string(make([]byte, 2048)),
			"big2.bin":  string(make([]byte, 2048)),
			"live.pcap": "x",
		},
	)
	useListedFiles(t, file.File{Name: "remote.bin", Path: "remote.bin", Size: 4096})

	small := experiment.ZipLimits{MaxFiles: 2, MaxBytes: 3000}

	tests := map[string]struct {
		paths []string
		want  error
	}{
		"none":            {paths: nil, want: experiment.ErrInvalidFilePath},
		"too many":        {paths: []string{"a.log", "b", "c"}, want: experiment.ErrDownloadTooLarge},
		"repeats once":    {paths: []string{"a.log", "big.bin", "big.bin"}, want: nil},
		"too big locally": {paths: []string{"big.bin", "big2.bin"}, want: experiment.ErrDownloadTooLarge},
		"too big listed":  {paths: []string{"remote.bin"}, want: experiment.ErrDownloadTooLarge},
		"escapes":         {paths: []string{"../../etc/passwd"}, want: experiment.ErrInvalidFilePath},
		"absolute":        {paths: []string{"/a.log"}, want: experiment.ErrInvalidFilePath},
		"missing":         {paths: []string{"a.log", "missing.log"}, want: experiment.ErrFileNotFound},
		"capturing":       {paths: []string{"live.pcap"}, want: mm.ErrCaptureExists},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var started bool

			_, err := experiment.ZipFiles("exp", tc.paths, small, func() io.Writer {
				started = true

				return io.Discard
			})

			if !errors.Is(err, tc.want) {
				t.Errorf("ZipFiles(exp, %q) error = %v, want %v", tc.paths, err, tc.want)
			}

			// an archive is started only when nothing is refused
			if want := tc.want == nil; started != want {
				t.Errorf("ZipFiles(exp, %q) started the archive = %t, want %t", tc.paths, started, want)
			}

			if tc.want == nil {
				return
			}

			if _, err := experiment.PlanZip("exp", tc.paths, small); !errors.Is(err, tc.want) {
				t.Errorf("PlanZip(exp, %q) error = %v, want %v", tc.paths, err, tc.want)
			}
		})
	}
}

func TestZipFilesReturnsTheFilesSentBeforeFailing(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, "partial", map[string]string{
		"a.log": "alpha",
		"b.gz":  string(make([]byte, 16<<10)), // stored, so larger than the zip writer's buffer
		"c.log": "gamma",
	})
	useListedFiles(t)

	// takes the first write, a.log flushed, and fails every later one
	var writes int

	failing := writerFunc(func(p []byte) (int, error) {
		writes++
		if writes > 1 {
			return 0, errors.New("client went away")
		}

		return len(p), nil
	})

	zipped, err := experiment.ZipFiles("partial", []string{"a.log", "b.gz", "c.log"}, zipLimits,
		func() io.Writer { return failing })
	if err == nil {
		t.Error("ZipFiles(partial, ...) to a failing writer error = nil, want the write error")
	}

	if diff := cmp.Diff([]string{"a.log"}, zipped); diff != "" {
		t.Errorf("ZipFiles(partial, ...) to a failing writer zipped mismatch (-want +got):\n%s", diff)
	}
}

// writerFunc is an [io.Writer] of a function.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// With SkipFailed, a file that vanished after the plan is left out, reported,
// and listed in MISSING.txt, and the rest are archived.
func TestZipPlanSkipsFailedFiles(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, "exp", map[string]string{"a.log": "alpha", "gone.log": "soon gone", "c.log": "gamma"})
	useListedFiles(t)

	paths := []string{"a.log", "gone.log", "c.log"}

	plan, err := experiment.PlanZip("exp", paths, zipLimits)
	if err != nil {
		t.Fatalf("PlanZip(exp, %q) error = %v", paths, err)
	}

	wantPlan := experiment.ZipPlan{
		Name:  "exp",
		Paths: paths,
		Sizes: map[string]int64{"a.log": 5, "gone.log": 9, "c.log": 5},
		Total: 19,
	}
	if diff := cmp.Diff(wantPlan, plan); diff != "" {
		t.Errorf("PlanZip(exp, %q) mismatch (-want +got):\n%s", paths, diff)
	}

	if err := os.Remove(filepath.Join(common.PhenixBase, "images", "exp", "files", "gone.log")); err != nil {
		t.Fatalf("removing gone.log: %v", err)
	}

	var (
		buf     bytes.Buffer
		bytesIn int64
		failed  = map[string]error{}
		started []string
	)

	archived, err := plan.Write(t.Context(), &buf, experiment.ZipHooks{
		OnStart:    func(p string) { started = append(started, p) },
		OnFile:     func(p string, err error) { failed[p] = err },
		OnBytes:    func(n int64) { bytesIn += n },
		SkipFailed: true,
	})
	if err != nil {
		t.Fatalf("ZipPlan.Write(SkipFailed) error = %v", err)
	}

	if diff := cmp.Diff([]string{"a.log", "c.log"}, archived); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) archived mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(paths, started); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) OnStart paths mismatch (-want +got):\n%s", diff)
	}

	wantFailed := map[string]error{"a.log": nil, "gone.log": experiment.ErrFileNotFound, "c.log": nil}
	if diff := cmp.Diff(wantFailed, failed, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) OnFile outcomes mismatch (-want +got):\n%s", diff)
	}

	if want := int64(len("alpha") + len("gamma")); bytesIn != want {
		t.Errorf("ZipPlan.Write(SkipFailed) OnBytes total = %d, want %d", bytesIn, want)
	}

	entries := readZip(t, &buf)
	names := make([]string, 0, len(entries))

	for _, e := range entries {
		names = append(names, e.Name)

		if e.Name == "MISSING.txt" && !strings.Contains(e.Data, "gone.log: ") {
			t.Errorf("ZipPlan.Write(SkipFailed) MISSING.txt = %q, want it to list gone.log", e.Data)
		}
	}

	if diff := cmp.Diff([]string{"a.log", "c.log", "MISSING.txt"}, names); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) archive entries mismatch (-want +got):\n%s", diff)
	}

	// without SkipFailed the archive fails
	if _, err := plan.Write(t.Context(), io.Discard, experiment.ZipHooks{}); !errors.Is(err, experiment.ErrFileNotFound) {
		t.Errorf("ZipPlan.Write() error = %v, want %v", err, experiment.ErrFileNotFound)
	}
}

func TestZipPlanWriteStopsWhenCanceled(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, "exp2", map[string]string{"a.log": "alpha", "b.log": "beta"})
	useListedFiles(t)

	plan, err := experiment.PlanZip("exp2", []string{"a.log", "b.log"}, zipLimits)
	if err != nil {
		t.Fatalf("PlanZip(exp2, [a.log b.log]) error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err = plan.Write(ctx, io.Discard, experiment.ZipHooks{OnStart: func(string) { cancel() }})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ZipPlan.Write() canceled on the first file error = %v, want %v", err, context.Canceled)
	}
}

// growFile appends n bytes to the experiment's file.
func growFile(t *testing.T, exp, p string, n int) {
	t.Helper()

	local := filepath.Join(common.PhenixBase, "images", exp, "files", p)

	f, err := os.OpenFile(local, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("opening %s to grow it: %v", p, err)
	}

	defer f.Close()

	if _, err := f.Write(make([]byte, n)); err != nil {
		t.Fatalf("growing %s: %v", p, err)
	}
}

// With SkipFailed, a file that grows before or while it is archived keeps its
// planned size of it in the archive, and MISSING.txt says it was cut short.
func TestZipPlanKeepsThePlannedPartOfAFileThatGrows(t *testing.T) { //nolint:paralleltest // replaces package state
	big := string(make([]byte, 40<<10)) // more than one io.Copy buffer
	useFilesDir(t, "grow", map[string]string{"before.gz": "alpha", "during.gz": big, "c.log": "gamma"})
	useListedFiles(t)

	limits := experiment.ZipLimits{MaxFiles: 3, MaxBytes: 64 << 10}

	plan, err := experiment.PlanZip("grow", []string{"before.gz", "during.gz", "c.log"}, limits)
	if err != nil {
		t.Fatalf("PlanZip(grow, [before.gz during.gz c.log]) error = %v", err)
	}

	growFile(t, "grow", "before.gz", 1<<20)

	var (
		buf     bytes.Buffer
		bytesIn int64
		current string
		grown   bool
		outcome = map[string]error{}
	)

	archived, err := plan.Write(t.Context(), &buf, experiment.ZipHooks{
		OnStart: func(p string) { current = p },
		OnFile:  func(p string, err error) { outcome[p] = err },
		OnBytes: func(n int64) {
			bytesIn += n

			if current == "during.gz" && !grown {
				grown = true

				growFile(t, "grow", "during.gz", 1<<20)
			}
		},
		SkipFailed: true,
	})
	if err != nil {
		t.Fatalf("ZipPlan.Write(SkipFailed) error = %v", err)
	}

	if diff := cmp.Diff([]string{"before.gz", "during.gz", "c.log"}, archived); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) archived mismatch (-want +got):\n%s", diff)
	}

	wantOutcome := map[string]error{
		"before.gz": experiment.ErrFileTruncated,
		"during.gz": experiment.ErrFileTruncated,
		"c.log":     nil,
	}
	if diff := cmp.Diff(wantOutcome, outcome, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) OnFile outcomes mismatch (-want +got):\n%s", diff)
	}

	if bytesIn > plan.Total {
		t.Errorf("ZipPlan.Write(SkipFailed) OnBytes total = %d, want at most the planned %d", bytesIn, plan.Total)
	}

	if int64(buf.Len()) > limits.MaxBytes {
		t.Errorf("ZipPlan.Write(SkipFailed) wrote %d bytes, want at most %d", buf.Len(), limits.MaxBytes)
	}

	got := map[string]string{}

	for _, e := range readZip(t, &buf) {
		got[e.Name] = e.Data
	}

	notes := got["MISSING.txt"]
	delete(got, "MISSING.txt")

	// the planned part of each file
	if diff := cmp.Diff(map[string]string{"before.gz": "alpha", "during.gz": big, "c.log": "gamma"}, got); diff != "" {
		t.Errorf("ZipPlan.Write(SkipFailed) archived files mismatch (-want +got):\n%s", diff)
	}

	for _, want := range []string{
		"changed while archiving",
		"before.gz: changed while archiving, so the archive includes only the first 5 bytes",
		fmt.Sprintf("during.gz: changed while archiving, so the archive includes only the first %d bytes", len(big)),
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("ZipPlan.Write(SkipFailed) MISSING.txt = %q, want it to contain %q", notes, want)
		}
	}

	if strings.Contains(notes, "phenix could not add these files") {
		t.Errorf("ZipPlan.Write(SkipFailed) MISSING.txt = %q, want no file left out", notes)
	}

	// without SkipFailed a grown file fails the archive, which stays within the limit
	buf.Reset()

	if _, err := plan.Write(t.Context(), &buf, experiment.ZipHooks{}); !errors.Is(err, experiment.ErrDownloadTooLarge) {
		t.Errorf("ZipPlan.Write() of a grown file error = %v, want %v", err, experiment.ErrDownloadTooLarge)
	}

	if int64(buf.Len()) > limits.MaxBytes {
		t.Errorf("ZipPlan.Write() of a grown file wrote %d bytes, want at most %d", buf.Len(), limits.MaxBytes)
	}
}

// ZipFiles archives each file asked for once, in the order first asked, when
// all of them are files within the limits, and otherwise refuses before writing.
func TestZipFilesProperties(t *testing.T) { //nolint:paralleltest // replaces package state
	files := map[string]string{"a.log": "alpha", "b/c.json": `{"ok":true}`, "d.gz": "gzipped", "e.txt": ""}
	useFilesDir(t, "exp", files)
	useListedFiles(t)

	rapid.Check(t, func(t *rapid.T) {
		// mostly the files, so most draws are archived
		listed := rapid.SampledFrom([]string{"a.log", "b/c.json", "d.gz", "e.txt"})
		paths := rapid.SliceOfN(rapid.OneOf(
			listed, listed, listed,
			rapid.SampledFrom([]string{"missing.log", "../a.log", "/a.log", "b//c.json", ""}),
		), 0, 6).Draw(t, "paths")
		var (
			unique []string
			total  int64
			bad    bool
		)

		for _, p := range paths {
			if !slices.Contains(unique, p) {
				unique = append(unique, p)
				contents, ok := files[p]
				bad = bad || !ok
				total += int64(len(contents))
			}
		}

		// limits at, just under or just over what is asked for
		limits := experiment.ZipLimits{
			MaxFiles: max(0, len(unique)+rapid.IntRange(-1, 1).Draw(t, "files over")),
			MaxBytes: max(0, total+rapid.Int64Range(-1, 1).Draw(t, "bytes over")),
		}

		var buf bytes.Buffer

		started := false

		zipped, err := experiment.ZipFiles("exp", paths, limits, func() io.Writer {
			started = true

			return &buf
		})

		if len(unique) == 0 || len(unique) > limits.MaxFiles || bad || total > limits.MaxBytes {
			if started || !errors.Is(err, experiment.ErrInvalidFilePath) && !errors.Is(err, experiment.ErrFileNotFound) &&
				!errors.Is(err, experiment.ErrDownloadTooLarge) {
				t.Fatalf("ZipFiles(exp, %q, %+v) = %q, %v, started %t; want a refusal before writing", paths, limits, zipped, err, started)
			}

			return
		}

		if err != nil {
			t.Fatalf("ZipFiles(exp, %q, %+v) error = %v", paths, limits, err)
		}

		archive, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("reading the zip of ZipFiles(exp, %q, %+v): %v", paths, limits, err)
		}

		entries := make([]string, 0, len(archive.File))

		for _, f := range archive.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("opening zip entry %s: %v", f.Name, err)
			}

			data, err := io.ReadAll(rc)
			_ = rc.Close()

			if err != nil || string(data) != files[f.Name] {
				t.Fatalf("zip entry %s = %q, %v; want %q", f.Name, data, err, files[f.Name])
			}

			entries = append(entries, f.Name)
		}

		if diff := cmp.Diff(unique, zipped); diff != "" {
			t.Fatalf("ZipFiles(exp, %q, %+v) zipped mismatch (-want +got):\n%s", paths, limits, diff)
		}

		if diff := cmp.Diff(unique, entries); diff != "" {
			t.Fatalf("ZipFiles(exp, %q, %+v) archive entries mismatch (-want +got):\n%s", paths, limits, diff)
		}
	})
}
