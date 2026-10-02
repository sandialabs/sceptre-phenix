package experiment_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
)

// listedFiles lists the given experiment files, copies those only a mesh node
// has to the headnode, and records deletions; any other file.ClusterFiles
// method panics through the nil embedded interface.
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

// capturingMM reports one capture writing to the named file, counting the
// times it is asked; any other mm.MM method panics through the nil embedded
// interface.
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

func TestFileReadsTheExperimentsFile(t *testing.T) { //nolint:paralleltest // replaces package state
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

			got, err := experiment.File("exp", tc.path)

			switch {
			case tc.err != nil && !errors.Is(err, tc.err):
				t.Fatalf("File = %q, %v; want %v", got, err, tc.err)
			case tc.err == nil && (err != nil || string(got) != tc.want):
				t.Fatalf("File = %q, %v; want %q", got, err, tc.want)
			}

			if files.listings != tc.listings || captures.lookups != tc.lookups {
				t.Errorf("listed the cluster's files %d times and the captures %d times, want %d and %d",
					files.listings, captures.lookups, tc.listings, tc.lookups)
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
		t.Fatalf("DeleteFiles: %v", err)
	}

	// only the file the capture writes is refused, not one whose name or base
	// name resembles it
	wantDeleted := []string{"a.log", "scorch/run-0/b.json", "old.pcap", "ive.pcap", "sub/live.pcap"}
	if !slices.Equal(deleted, wantDeleted) {
		t.Errorf("deleted %v, want %v", deleted, wantDeleted)
	}

	wantNames := []string{
		"exp/files/a.log", "exp/files/scorch/run-0/b.json", "exp/files/old.pcap", "exp/files/ive.pcap",
		"exp/files/sub/live.pcap",
	}
	if !slices.Equal(fake.deleted, wantNames) {
		t.Errorf("asked minimega to delete %v, want %v", fake.deleted, wantNames)
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

	if len(refused) != len(wantRefused) {
		t.Errorf("refused %v, want %v", refused, wantRefused)
	}

	for p, want := range wantRefused {
		if !errors.Is(refused[p], want) {
			t.Errorf("refused[%q] = %v, want %v", p, refused[p], want)
		}
	}

	var writing *experiment.CaptureWritingError
	if !errors.As(refused["live.pcap"], &writing) || writing.Capture.VM != "vm" {
		t.Errorf("live.pcap refused with %v, want the capture writing it", refused["live.pcap"])
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
			t.Errorf("CaptureFile(exp, %q) = %q, %v; want %q", capturePath, got, ok, want)
		}
	}
}

func TestDeleteFilesReportsDeleteFailure(t *testing.T) { //nolint:paralleltest // replaces package state
	fake := useListedFiles(t, file.File{Name: "a.log", Path: "a.log"})
	fake.err = errors.New("mesh down")

	deleted, _, err := experiment.DeleteFiles("exp", []string{"a.log"})
	if err == nil || deleted != nil {
		t.Fatalf("DeleteFiles = %v, %v; want the failure", deleted, err)
	}
}

func TestDeleteFileReturnsTheRefusal(t *testing.T) { //nolint:paralleltest // replaces package state
	fake := useListedFiles(t, file.File{Name: "a.log", Path: "a.log"})

	if err := experiment.DeleteFile("exp", "a.log"); err != nil {
		t.Fatalf("DeleteFile(a.log) = %v", err)
	}

	if err := experiment.DeleteFile("exp", "b.log"); !errors.Is(err, experiment.ErrFileNotFound) {
		t.Fatalf("DeleteFile(b.log) = %v, want ErrFileNotFound", err)
	}

	if err := experiment.DeleteFile("bad*", "a.log"); !errors.Is(err, experiment.ErrInvalidFilePath) {
		t.Fatalf("DeleteFile in experiment bad* = %v, want ErrInvalidFilePath", err)
	}

	if !slices.Equal(fake.deleted, []string{"exp/files/a.log"}) {
		t.Fatalf("deleted %v", fake.deleted)
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
			t.Fatal(err)
		}

		if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

var zipLimits = experiment.ZipLimits{MaxFiles: 10, MaxBytes: 1 << 20} //nolint:gochecknoglobals // test limits

func TestZipFilesArchivesTheFiles(t *testing.T) { //nolint:paralleltest // replaces package state
	useFilesDir(t, "exp", map[string]string{
		"a.log":               "alpha",
		"scorch/run-0/b.json": `{"ok":true}`,
		"c.gz":                "gzipped",
	})
	useListedFiles(t)

	var buf bytes.Buffer

	zipped, err := experiment.ZipFiles("exp", []string{"a.log", "scorch/run-0/b.json", "a.log", "c.gz"}, zipLimits,
		func() io.Writer { return &buf })
	if err != nil {
		t.Fatalf("ZipFiles: %v", err)
	}

	if want := []string{"a.log", "scorch/run-0/b.json", "c.gz"}; !slices.Equal(zipped, want) {
		t.Errorf("zipped %v, want %v", zipped, want)
	}

	archive, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}

	want := map[string]string{"a.log": "alpha", "scorch/run-0/b.json": `{"ok":true}`, "c.gz": "gzipped"}
	if len(archive.File) != len(want) {
		t.Fatalf("zip has %d files, want %d", len(archive.File), len(want))
	}

	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}

		got, _ := io.ReadAll(rc)
		_ = rc.Close()

		if string(got) != want[f.Name] {
			t.Errorf("%s = %q, want %q", f.Name, got, want[f.Name])
		}

		if f.Name == "c.gz" && f.Method != zip.Store {
			t.Errorf("c.gz compressed again (method %d)", f.Method)
		}
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

			if tc.want == nil {
				if err != nil || !started {
					t.Fatalf("ZipFiles = %v (started %v), want an archive", err, started)
				}

				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("ZipFiles = %v, want %v", err, tc.want)
			}

			if started {
				t.Fatal("started the archive before refusing it")
			}
		})
	}
}
