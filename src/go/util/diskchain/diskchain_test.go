package diskchain_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// walkCase is one image layout and the walk of its top image, top.qc2 in dir.
type walkCase struct {
	name  string
	setup func(t *testing.T, dir string)
	want  func(dir string) diskchain.Result
}

func qcow2(t *testing.T, path string, q dct.Qcow2) { t.Helper(); dct.WriteQcow2(t, path, q) }

func raw(t *testing.T, path string) { t.Helper(); dct.WriteFile(t, path, []byte("raw image")) }

//nolint:maintidx // the table
func walkCases() []walkCase {
	return []walkCase{
		{
			name: "a raw image is the base of its own chain",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				raw(t, dir+"/top.qc2")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2"}}
			},
		},
		{
			name:  "a missing image is missing, with no members",
			setup: func(*testing.T, string) {},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Problem: diskchain.Missing, At: dir + "/top.qc2"}
			},
		},
		{
			name: "an absolute backing name is followed to a raw base",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: dir + "/base/base.img"})
				raw(t, dir+"/base/base.img")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2", dir + "/base/base.img"}}
			},
		},
		{
			name: "a relative backing name is joined onto the folder of the image naming it",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "sub/mid.qc2"})
				qcow2(t, dir+"/sub/mid.qc2", dct.Qcow2{Backing: "../base.qc2"})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{
					dir + "/top.qc2", dir + "/sub/mid.qc2", dir + "/sub/../base.qc2",
				}}
			},
		},
		{
			name: "a \"..\" after a symlinked folder goes up from the folder the symlink names, as qemu does",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				mkdir(t, dir+"/ssd/win")
				mkdir(t, dir+"/images")
				symlink(t, dir+"/ssd/win", dir+"/images/win")
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "images/win/ov.qc2"})
				qcow2(t, dir+"/ssd/win/ov.qc2", dct.Qcow2{Backing: "../base/mid.qc2"})
				qcow2(t, dir+"/ssd/base/mid.qc2", dct.Qcow2{Backing: "root.qc2"})
				qcow2(t, dir+"/ssd/base/root.qc2", dct.Qcow2{})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{
					dir + "/top.qc2", dir + "/images/win/ov.qc2", dir + "/images/win/../base/mid.qc2",
					dir + "/images/win/../base/root.qc2",
				}}
			},
		},
		{
			name: "a version 2 header is followed",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Version: 2, Backing: "base.qc2"})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{Version: 2})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2", dir + "/base.qc2"}}
			},
		},
		{
			name: "a missing backing file ends the chain",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "ro_top.qc2"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Missing, At: dir + "/ro_top.qc2",
				}
			},
		},
		{
			name: "a dangling symlink is missing",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "link.qc2"})
				symlink(t, dir+"/gone.qc2", dir+"/link.qc2")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Missing, At: dir + "/link.qc2",
				}
			},
		},
		{
			name: "a path through a file is missing",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "top.qc2/base.qc2"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Missing, At: dir + "/top.qc2/base.qc2",
				}
			},
		},
		{
			name: "a chain naming one of its images again loops",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "a.qc2"})
				qcow2(t, dir+"/a.qc2", dct.Qcow2{Backing: "top.qc2"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/a.qc2"}, Problem: diskchain.Loop, At: dir + "/top.qc2",
				}
			},
		},
		{
			name: "a loop through a symlink is found by file, not name",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "alias.qc2"})
				symlink(t, dir+"/top.qc2", dir+"/alias.qc2")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Loop, At: dir + "/alias.qc2",
				}
			},
		},
		{
			name: "a chain deeper than MaxDepth is too deep",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				paths := make([]string, 0, diskchain.MaxDepth+2)
				paths = append(paths, dir+"/top.qc2")

				for i := range diskchain.MaxDepth + 1 {
					paths = append(paths, fmt.Sprintf("%s/%02d.qc2", dir, i))
				}

				dct.Chain(t, paths...)
			},
			want: func(dir string) diskchain.Result {
				members := make([]string, 0, diskchain.MaxDepth)
				members = append(members, dir+"/top.qc2")

				for i := range diskchain.MaxDepth - 1 {
					members = append(members, fmt.Sprintf("%s/%02d.qc2", dir, i))
				}

				return diskchain.Result{
					Members: members, Problem: diskchain.TooDeep, At: fmt.Sprintf("%s/%02d.qc2", dir, diskchain.MaxDepth-1),
				}
			},
		},
		{
			name: "a raw backing format stops at its file, even one that looks like a qcow2 image",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2", BackingFormat: "raw", HeaderLength: 112})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{Backing: "missing.qc2"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2", dir + "/base.qc2"}}
			},
		},
		{
			name: "a missing raw backing file is missing",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.img", BackingFormat: "raw"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Missing, At: dir + "/base.img",
				}
			},
		},
		{
			name: "a qcow2 backing format on an image that is not qcow2 cannot be read",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.img", BackingFormat: "qcow2"})
				raw(t, dir+"/base.img")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.img"}, Problem: diskchain.Unreadable,
					At: dir + "/base.img", Detail: "not the qcow2 image its parent names",
				}
			},
		},
		{
			name: "another backing format is not followed",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.vmdk", BackingFormat: "vmdk"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Unsupported,
					At: dir + "/top.qc2", Detail: "backed by a vmdk image",
				}
			},
		},
		{
			name: "a qed image is not followed",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qed"})
				dct.WriteFile(t, dir+"/base.qed", []byte("QED\x00rest of header"))
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.qed"}, Problem: diskchain.Unsupported,
					At: dir + "/base.qed", Detail: "a qed image",
				}
			},
		},
		{
			name: "a vmdk descriptor is not followed",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				dct.WriteFile(t, dir+"/top.qc2", []byte("# Disk DescriptorFile\nversion=1\n"))
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Unsupported,
					At: dir + "/top.qc2", Detail: "a vmdk image",
				}
			},
		},
		{
			name: "an external data file is not checked",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2", DataFile: true})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.DataFile, At: dir + "/top.qc2",
				}
			},
		},
		{
			name: "a backing file named by a protocol is not a file",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "nbd://storage:10809/base"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.NotAFile, At: "nbd://storage:10809/base",
				}
			},
		},
		{
			name: "a json: backing name is not a file",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: `json:{"file":{"driver":"file","filename":"/b"}}`})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.NotAFile,
					At: `json:{"file":{"driver":"file","filename":"/b"}}`,
				}
			},
		},
		{
			name: "a truncated qcow2 header cannot be read",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				dct.WriteFile(t, dir+"/top.qc2", []byte("QFI\xfb\x00\x00\x00\x03"))
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Unreadable,
					At: dir + "/top.qc2", Detail: "invalid qcow2 header: 8 bytes long",
				}
			},
		},
		{
			name: "an unknown qcow2 version is not followed",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				header := dct.Qcow2{}.Bytes()
				binary.BigEndian.PutUint32(header[4:], 4)
				dct.WriteFile(t, dir+"/top.qc2", header)
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Unsupported,
					At: dir + "/top.qc2", Detail: "a qcow2 version 4 image",
				}
			},
		},
		{
			name: "a folder is not a regular file",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				mkdir(t, dir+"/top.qc2")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Problem: diskchain.Unreadable, At: dir + "/top.qc2", Detail: "not a regular file"}
			},
		},
		{
			name: "a FIFO is not opened for good",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := syscall.Mkfifo(dir+"/top.qc2", 0o600); err != nil {
					t.Fatalf("mkfifo: %v", err)
				}
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Problem: diskchain.Unreadable, At: dir + "/top.qc2", Detail: "not a regular file"}
			},
		},
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func TestWalk(t *testing.T) {
	t.Parallel()

	for _, tc := range walkCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.setup(t, dir)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			got := diskchain.Walk(ctx, dir+"/top.qc2")
			if diff := cmp.Diff(tc.want(dir), got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Walk mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// An image phenix may not read cannot be read; root reads every file, so the case needs another user.
func TestWalkPermissionDenied(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}

	dir := t.TempDir()
	qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2"})
	raw(t, dir+"/base.qc2")

	if err := os.Chmod(dir+"/base.qc2", 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	want := diskchain.Result{
		Members: []string{dir + "/top.qc2"}, Problem: diskchain.Unreadable, At: dir + "/base.qc2", Detail: "permission denied",
	}

	if diff := cmp.Diff(want, diskchain.Walk(t.Context(), dir+"/top.qc2")); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}

// A file in a system directory is never opened, since opening one can block or have side effects.
func TestWalkRefusesSystemDirectories(t *testing.T) {
	t.Parallel()

	want := diskchain.Result{Problem: diskchain.Unreadable, At: "/dev/zero", Detail: "in a system directory"}

	if diff := cmp.Diff(want, diskchain.Walk(t.Context(), "/dev/zero")); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}

// A walk whose context is done stops at once.
func TestWalkStopsWhenCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	want := diskchain.Result{Problem: diskchain.Unreadable, At: "/x.qc2", Detail: "context canceled"}

	if diff := cmp.Diff(want, diskchain.Walk(ctx, "/x.qc2")); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}

func TestSentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		result diskchain.Result
		want   string
		broken bool
	}{
		{result: diskchain.Result{Members: []string{"/a"}}},
		{
			result: diskchain.Result{Problem: diskchain.Missing, At: "/i/a.qc2"},
			want:   "disk image /i/a.qc2 is missing", broken: true,
		},
		{
			result: diskchain.Result{Members: []string{"/i/a.qc2"}, Problem: diskchain.Missing, At: "/i/ro_a.qc2"},
			want:   "backing file /i/ro_a.qc2 is missing", broken: true,
		},
		{
			result: diskchain.Result{Problem: diskchain.Loop, At: "/i/a.qc2"},
			want:   "backing chain loops back to /i/a.qc2", broken: true,
		},
		{result: diskchain.Result{Problem: diskchain.TooDeep}, want: "backing chain is more than 64 images deep", broken: true},
		{
			result: diskchain.Result{Problem: diskchain.Unreadable, At: "/i/a.qc2", Detail: "permission denied"},
			want:   "cannot read /i/a.qc2 (permission denied), so phenix does not check past it",
		},
		{
			result: diskchain.Result{Problem: diskchain.Unsupported, At: "/i/a.vmdk", Detail: "a vmdk image"},
			want:   "/i/a.vmdk is a vmdk image, so phenix does not check past it",
		},
		{
			result: diskchain.Result{Problem: diskchain.NotAFile, At: "nbd://h/x"},
			want:   "backing file nbd://h/x is not a local file, so phenix does not check it",
		},
		{
			result: diskchain.Result{Problem: diskchain.DataFile, At: "/i/a.qc2"},
			want:   "/i/a.qc2 has an external data file, so phenix does not check it",
		},
	}

	for _, tc := range tests {
		if got := tc.result.Sentence(); got != tc.want {
			t.Errorf("%+v Sentence() = %q, want %q", tc.result, got, tc.want)
		}

		if got := tc.result.Broken(); got != tc.broken {
			t.Errorf("%+v Broken() = %t, want %t", tc.result, got, tc.broken)
		}
	}
}

func TestParseHeader(t *testing.T) {
	t.Parallel()

	long := dct.Qcow2{Backing: strings.Repeat("b", 1024)}.Bytes()

	// a backing name 80 KiB in, past the first 64 KiB, in a 128 KiB cluster
	far := dct.Qcow2{}.Bytes()
	binary.BigEndian.PutUint32(far[20:], 17)
	binary.BigEndian.PutUint64(far[8:], 80<<10)
	binary.BigEndian.PutUint32(far[16:], 8)
	far = append(far, make([]byte, 80<<10-len(far))...)
	far = append(far, "base.qc2"...)

	// an extension whose length runs past the header
	runaway := dct.Qcow2{HeaderLength: 112}.Bytes()
	binary.BigEndian.PutUint32(runaway[112:], 0xE2792ACA)
	binary.BigEndian.PutUint32(runaway[116:], 1<<20)

	// a backing name past the first cluster
	outside := dct.Qcow2{Backing: "b"}.Bytes()
	binary.BigEndian.PutUint64(outside[8:], 1<<17)

	tests := []struct {
		name    string
		data    []byte
		want    diskchain.Header
		wantErr string
	}{
		{name: "empty", data: nil, want: diskchain.Header{}},
		{name: "raw", data: []byte("hello"), want: diskchain.Header{}},
		{
			name: "version 3 with a backing format after a longer header",
			data: dct.Qcow2{Backing: "b.qc2", BackingFormat: "qcow2", HeaderLength: 112}.Bytes(),
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Backing: "b.qc2", BackingFormat: "qcow2"},
		},
		{
			name: "version 2 reads extensions from byte 72",
			data: dct.Qcow2{Version: 2, Backing: "b.img", BackingFormat: "raw"}.Bytes(),
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 2, Backing: "b.img", BackingFormat: "raw"},
		},
		{
			name: "a backing name past the first 64 KiB",
			data: far,
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Backing: "base.qc2"},
		},
		{
			name: "a backing name over 1023 bytes", data: long,
			want:    diskchain.Header{Format: diskchain.FormatQcow2, Version: 3},
			wantErr: "invalid qcow2 header: backing file name of 1024 bytes at 112",
		},
		{
			name: "a backing name past the first cluster", data: outside,
			want:    diskchain.Header{Format: diskchain.FormatQcow2, Version: 3},
			wantErr: "invalid qcow2 header: backing file name of 1 bytes at 131072",
		},
		{
			name: "an extension past the header", data: runaway,
			want:    diskchain.Header{Format: diskchain.FormatQcow2, Version: 3},
			wantErr: "invalid qcow2 header: header extension 0xe2792aca runs past the header",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := diskchain.ParseHeader(bytes.NewReader(tc.data))

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != tc.wantErr {
				t.Errorf("ParseHeader error = %q, want %q", gotErr, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParseHeader mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// headerSeeds are the headers the table tests use, as seeds for the properties and the fuzz target.
func headerSeeds() [][]byte {
	return [][]byte{
		nil,
		[]byte("QED\x00"),
		[]byte("# Disk DescriptorFile"),
		dct.Qcow2{}.Bytes(),
		dct.Qcow2{Version: 2, Backing: "b.img", BackingFormat: "raw"}.Bytes(),
		dct.Qcow2{Backing: "b.qc2", BackingFormat: "qcow2", HeaderLength: 112}.Bytes(),
		dct.Qcow2{Backing: "../b.qc2", DataFile: true}.Bytes(),
		dct.Qcow2{Backing: strings.Repeat("b", 1024)}.Bytes(),
	}
}

// checkHeader fails when h, parsed from data with error err, says what no qcow2 header qemu opens can say.
func checkHeader(data []byte, h diskchain.Header, err error) error {
	isQcow2 := bytes.HasPrefix(data, []byte("QFI\xfb"))

	switch {
	case isQcow2 != (h.Format == diskchain.FormatQcow2):
		return fmt.Errorf("format %q for data starting %q", h.Format, data[:min(4, len(data))])
	case err != nil && !isQcow2:
		return fmt.Errorf("error %w for data that is not qcow2", err)
	case err == nil && len(h.Backing) > 1023:
		return fmt.Errorf("backing name of %d bytes", len(h.Backing))
	case err == nil && isQcow2 && h.Version != 2 && h.Version != 3:
		return fmt.Errorf("version %d without an error", h.Version)
	}

	return nil
}
