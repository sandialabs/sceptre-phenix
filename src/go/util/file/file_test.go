package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// fileCommands lists the bases of the `file` commands, sent directly or
// through mesh, that minimega received.
func fileCommands(cmds []mmtest.Command) []string {
	var out []string

	for _, base := range mmtest.Bases(cmds) {
		if strings.HasPrefix(base, "file ") || strings.HasPrefix(base, "mesh send ") {
			out = append(out, base)
		}
	}

	return out
}

// listingHeader is the header of minimega's `file list`.
var listingHeader = []string{"dir", "name", "size", "modified"} //nolint:gochecknoglobals // a fixed header

// The listing asks the mesh nodes and then the headnode for the experiment's
// files, keeps the first copy of each path, leaves out Filebeat files other
// than its log, and categorizes the files by folder and extension.
//
//nolint:paralleltest // replaces the listing cache and minimega
func TestListExperimentFilesCategorizes(t *testing.T) {
	installListing(t, nil)
	experimentListings.invalidateAll()

	const modified = "2026-01-02T03:04:05Z"

	row := func(name, size string) []string { return []string{"", "exp/files/" + name, size, modified} }

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "mesh send all file list"):
			return []*minicli.Response{mmtest.Tabular("compute1", listingHeader,
				row("vm_eth0.pcap", "10"), row("notes.txt", "1"), row("exp-20261105-142233.pcapng", "12"),
				row("old.cap", "13"))}
		case strings.HasPrefix(cmd.Base, "file list"):
			return []*minicli.Response{mmtest.Tabular("head", listingHeader,
				row("notes.txt", "2"),
				row("scorch/1/tap/out.log", "3"),
				row("scorch/1/filebeat/filebeat.log", "4"),
				row("scorch/1/filebeat/registry.json", "5"),
				row("scorch/summary.pcap", "6"),
				row("mem.elf", "7"),
				row("vm__snap.state", "8"),
				row("vm__snap.hdd", "9"),
				row("base.qcow2", "11"),
				[]string{"", "exp/files/blob.bin", "big", "yesterday"},
			)}
		}

		return nil
	})

	got, err := listExperimentFiles(t.Context(), "exp")
	if err != nil {
		t.Fatalf("listExperimentFiles(exp) error = %v", err)
	}

	at, _ := time.Parse(time.RFC3339, modified)

	file := func(path string, size int64, plain bool, categories ...string) File {
		name := path[strings.LastIndex(path, "/")+1:]

		return File{
			Name:       name,
			Path:       path,
			Date:       modified,
			Size:       size,
			Categories: categories,
			PlainText:  plain,
			IsDir:      false,
			dateTime:   at,
		}
	}

	want := Files{
		file("vm_eth0.pcap", 10, false, "Packet Capture"),
		file("exp-20261105-142233.pcapng", 12, false, "Packet Capture"),
		file("old.cap", 13, false, "Packet Capture"),
		file("notes.txt", 1, true, "Unknown"),
		file("scorch/1/tap/out.log", 3, true, "Scorch Artifact", "1", "tap"),
		file("scorch/1/filebeat/filebeat.log", 4, true, "Scorch Artifact", "1", "Filebeat"),
		file("scorch/summary.pcap", 6, false, "Scorch Artifact", "Packet Capture"),
		file("mem.elf", 7, false, "ELF Memory Snapshot"),
		file("vm__snap.state", 8, false, "VM Memory Snapshot"),
		file("vm__snap.hdd", 9, false, "VM Disk Snapshot"),
		file("base.qcow2", 11, false, "Backing Image"),
		{Name: "blob.bin", Path: "blob.bin", Date: "yesterday", Size: 0, Categories: []string{"Unknown"}},
	}

	opts := cmp.Options{cmp.AllowUnexported(File{}), cmpopts.SortSlices(func(a, b File) bool { return a.Path < b.Path })}
	if diff := cmp.Diff(want, got, opts); diff != "" {
		t.Errorf("listExperimentFiles(exp) mismatch (-want +got):\n%s", diff)
	}

	wantCommands := []string{"mesh send all file list exp/files/ recursive", "file list exp/files/ recursive"}
	if diff := cmp.Diff(wantCommands, fileCommands(received())); diff != "" {
		t.Errorf("listExperimentFiles(exp) commands mismatch (-want +got):\n%s", diff)
	}
}

// CopyFile asks the headnode, or through mesh another node, to get the file,
// and returns once the node's transfers do not include it. It does not poll
// after the get fails.
//
//nolint:paralleltest // replaces minimega
func TestCopyFileCommands(t *testing.T) {
	headnode, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname() error = %v", err)
	}

	const path = "/exp/files/a.pcap"

	tests := []struct {
		name, dest string
		fail       bool
		want       []string
	}{
		{name: "headnode", dest: headnode, want: []string{"file get " + path, "file status"}},
		{name: "other node", dest: "compute1", want: []string{
			"mesh send compute1 file get " + path, "mesh send compute1 file status",
		}},
		{name: "failed get", dest: "compute1", fail: true, want: []string{"mesh send compute1 file get " + path}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case strings.Contains(cmd.Base, "file get") && tc.fail:
					return []*minicli.Response{mmtest.Error(tc.dest, "no such file")}
				case strings.HasSuffix(cmd.Base, "file status"):
					// another file's transfer only
					return []*minicli.Response{mmtest.Tabular(tc.dest, []string{"filename", "tempdir", "completed", "queued"},
						[]string{"/exp/files/b.pcap", "/tmp/x", "1/2", "false"})}
				}

				return nil
			})

			var reports []float64

			err := MMClusterFiles{}.CopyFile(path, tc.dest, func(p float64) { reports = append(reports, p) })
			if (err != nil) != tc.fail {
				t.Errorf("CopyFile(%s, %s) error = %v, want an error: %t", path, tc.dest, err, tc.fail)
			}

			if diff := cmp.Diff(tc.want, fileCommands(received())); diff != "" {
				t.Errorf("CopyFile(%s, %s) commands mismatch (-want +got):\n%s", path, tc.dest, diff)
			}

			if len(reports) != 0 {
				t.Errorf("CopyFile(%s, %s) progress reports = %v, want none", path, tc.dest, reports)
			}
		})
	}
}

// SyncFile asks every mesh node to get the file, and returns minimega's
// error.
//
//nolint:paralleltest // replaces minimega
func TestSyncFileCommands(t *testing.T) {
	const path = "/exp/files/a.pcap"

	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%t", fail), func(t *testing.T) {
			received := mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
				if fail {
					return []*minicli.Response{mmtest.Error("compute1", "no such file")}
				}

				return nil
			})

			if err := (MMClusterFiles{}).SyncFile(path); (err != nil) != fail {
				t.Errorf("SyncFile(%s) error = %v, want an error: %t", path, err, fail)
			}

			if diff := cmp.Diff([]string{"mesh send all file get " + path}, fileCommands(received())); diff != "" {
				t.Errorf("SyncFile(%s) commands mismatch (-want +got):\n%s", path, diff)
			}
		})
	}
}

// passingFiles is a ClusterFiles with neither optional part, which records
// the calls it receives and answers each with files and err.
type passingFiles struct {
	files Files
	err   error
	calls []string
}

func (p *passingFiles) recordf(format string, args ...any) {
	p.calls = append(p.calls, fmt.Sprintf(format, args...))
}

func (p *passingFiles) GetExperimentFiles(exp, filter string) (Files, error) {
	p.recordf("files %s %s", exp, filter)

	return p.files, p.err
}

func (p *passingFiles) GetExperimentSnapshots(exp string) ([]string, error) {
	p.recordf("snapshots %s", exp)

	return []string{"snap"}, p.err
}

func (p *passingFiles) CopyFile(path, dest string, status CopyStatus) error {
	p.recordf("copy %s %s %t", path, dest, status != nil)

	return p.err
}

func (p *passingFiles) SyncFile(path string) error {
	p.recordf("sync %s", path)

	return p.err
}

func (p *passingFiles) DeleteFile(path string) error {
	p.recordf("delete %s", path)

	return p.err
}

// The package's functions pass each call and its error to a ClusterFiles
// that cannot report when it listed or stop a listing early. Its listings
// then count as read when the call started.
//
//nolint:paralleltest // replaces DefaultClusterFiles
func TestPackageFunctionsPassThrough(t *testing.T) {
	errList := errors.New("cannot list")

	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%t", fail), func(t *testing.T) {
			fake := &passingFiles{files: Files{{Name: "a.pcap", Path: "a.pcap"}}}
			if fail {
				fake.err = errList
			}

			original := DefaultClusterFiles
			DefaultClusterFiles = fake

			t.Cleanup(func() { DefaultClusterFiles = original })

			before := time.Now()

			files, observed, err := GetExperimentFilesObserved(context.Background(), "exp", "pcap")
			if !errors.Is(err, fake.err) {
				t.Errorf("GetExperimentFilesObserved(exp, pcap) error = %v, want %v", err, fake.err)
			}

			switch {
			case fail && (files != nil || observed.Known):
				t.Errorf("GetExperimentFilesObserved(exp, pcap) = %v, %+v; want nil and an unknown observation", files, observed)
			case !fail && (!observed.Known || observed.At.Before(before) || observed.At.After(time.Now())):
				t.Errorf("GetExperimentFilesObserved(exp, pcap) observed %+v, want known and during the call", observed)
			case !fail && len(files) != 1:
				t.Errorf("GetExperimentFilesObserved(exp, pcap) = %v, want the fake's one file", files)
			}

			if _, err := GetExperimentFiles("exp", ""); !errors.Is(err, fake.err) {
				t.Errorf("GetExperimentFiles(exp) error = %v, want %v", err, fake.err)
			}

			if _, err := GetExperimentSnapshotsContext(t.Context(), "exp"); !errors.Is(err, fake.err) {
				t.Errorf("GetExperimentSnapshotsContext(exp) error = %v, want %v", err, fake.err)
			}

			if err := CopyFile("/exp/files/a.pcap", "compute1", func(float64) {}); !errors.Is(err, fake.err) {
				t.Errorf("CopyFile(/exp/files/a.pcap, compute1) error = %v, want %v", err, fake.err)
			}

			if err := SyncFile("/exp/files/a.pcap"); !errors.Is(err, fake.err) {
				t.Errorf("SyncFile(/exp/files/a.pcap) error = %v, want %v", err, fake.err)
			}

			want := []string{
				"files exp pcap", "files exp ", "snapshots exp", "copy /exp/files/a.pcap compute1 true", "sync /exp/files/a.pcap",
			}
			if diff := cmp.Diff(want, fake.calls); diff != "" {
				t.Errorf("ClusterFiles calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
