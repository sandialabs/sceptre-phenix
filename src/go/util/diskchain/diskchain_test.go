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
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.qc2"},
					Notes: []diskchain.Note{
						{Path: dir + "/top.qc2", Kind: diskchain.Qcow2V2}, {Path: dir + "/base.qc2", Kind: diskchain.Qcow2V2},
					},
				}
			},
		},
		{
			name: "a qcow version 1 image is followed by its own header, with a note",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "old.qcow", BackingFormat: "qcow", HeaderLength: 112})
				dct.WriteQcow(t, dir+"/old.qcow", dct.Qcow{Backing: "base.img"})
				raw(t, dir+"/base.img")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/old.qcow", dir + "/base.img"},
					Notes:   []diskchain.Note{{Path: dir + "/old.qcow", Kind: diskchain.QcowV1}},
				}
			},
		},
		{
			name: "a qcow version 1 image with a missing backing file is missing",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				dct.WriteQcow(t, dir+"/top.qc2", dct.Qcow{Backing: dir + "/gone.qcow"})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.Missing, At: dir + "/gone.qcow",
					Notes: []diskchain.Note{{Path: dir + "/top.qc2", Kind: diskchain.QcowV1}},
				}
			},
		},
		{
			name: "a qcow backing format on a qcow2 image cannot be read",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2", BackingFormat: "qcow"})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.qc2"}, Problem: diskchain.Unreadable,
					At: dir + "/base.qc2", Detail: "not the qcow image its parent names",
				}
			},
		},
		{
			name: "a qcow version 1 image with AES encryption is refused",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "old.qcow"})
				dct.WriteQcow(t, dir+"/old.qcow", dct.Qcow{Backing: "gone.img", Encryption: 1})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/old.qcow"}, Problem: diskchain.AESEncrypted, At: dir + "/old.qcow",
				}
			},
		},
		{
			name: "a qcow2 image with AES encryption is refused",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Version: 2, Encryption: 1})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2"}, Problem: diskchain.AESEncrypted, At: dir + "/top.qc2"}
			},
		},
		{
			name: "a qcow2 image with LUKS encryption is refused",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Encryption: 2})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2"}, Problem: diskchain.LUKSEncrypted, At: dir + "/top.qc2"}
			},
		},
		{
			name: "a corrupt image is followed, with a note, since qemu reads it as a backing file",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2"})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{Features: 1 << 1, Backing: "base.img"})
				raw(t, dir+"/base.img")
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.qc2", dir + "/base.img"},
					Notes:   []diskchain.Note{{Path: dir + "/base.qc2", Kind: diskchain.Corrupt}},
				}
			},
		},
		{
			name: "an encrypted corrupt image is refused for its encryption",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Features: 1 << 1, Encryption: 2})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{Members: []string{dir + "/top.qc2"}, Problem: diskchain.LUKSEncrypted, At: dir + "/top.qc2"}
			},
		},
		{
			name: "unknown incompatible feature bits are refused before a corrupt mark",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Features: 1<<1 | 1<<5 | 1<<40})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.UnknownFeatures, At: dir + "/top.qc2",
					Detail: "0x10000000020",
				}
			},
		},
		{
			name: "dirty, zstd and extended L2 images are followed, with notes",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				qcow2(t, dir+"/top.qc2", dct.Qcow2{Backing: "base.qc2", Features: 1, Zstd: true})
				qcow2(t, dir+"/base.qc2", dct.Qcow2{Features: 1 << 4})
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2", dir + "/base.qc2"},
					Notes: []diskchain.Note{
						{Path: dir + "/top.qc2", Kind: diskchain.Dirty}, {Path: dir + "/top.qc2", Kind: diskchain.Zstd},
						{Path: dir + "/base.qc2", Kind: diskchain.ExtendedL2},
					},
				}
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
					Notes: []diskchain.Note{{Path: dir + "/base.qed", Kind: diskchain.QED}},
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
					Notes: []diskchain.Note{{Path: dir + "/top.qc2", Kind: diskchain.VMDK}},
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
			name: "an unknown qcow2 version is refused",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				header := dct.Qcow2{}.Bytes()
				binary.BigEndian.PutUint32(header[4:], 4)
				dct.WriteFile(t, dir+"/top.qc2", header)
			},
			want: func(dir string) diskchain.Result {
				return diskchain.Result{
					Members: []string{dir + "/top.qc2"}, Problem: diskchain.BadVersion, At: dir + "/top.qc2", Detail: "4",
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
		result  diskchain.Result
		want    string
		broken  bool
		refused bool
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
		{
			result: diskchain.Result{Problem: diskchain.BadVersion, At: "/i/kali.qc2", Detail: "4"},
			want:   "/i/kali.qc2 is a qcow2 version 4 image, which qemu cannot open", refused: true,
		},
		{
			result: diskchain.Result{Problem: diskchain.UnknownFeatures, At: "/i/kali.qc2", Detail: "0x20"},
			want:   "/i/kali.qc2 uses qcow2 features that qemu does not know (incompatible feature bits 0x20)", refused: true,
		},
		{
			result: diskchain.Result{Problem: diskchain.AESEncrypted, At: "/i/kali.qc2"},
			want:   "/i/kali.qc2 uses the old qcow AES encryption, which qemu does not open for VMs", refused: true,
		},
		{
			result: diskchain.Result{Problem: diskchain.LUKSEncrypted, At: "/i/kali.qc2"},
			want:   "/i/kali.qc2 uses LUKS encryption, and phenix cannot give qemu its key", refused: true,
		},
	}

	for _, tc := range tests {
		if got := tc.result.Sentence(); got != tc.want {
			t.Errorf("%+v Sentence() = %q, want %q", tc.result, got, tc.want)
		}

		if got := tc.result.Broken(); got != tc.broken {
			t.Errorf("%+v Broken() = %t, want %t", tc.result, got, tc.broken)
		}

		if got := tc.result.Refused(); got != tc.refused {
			t.Errorf("%+v Refused() = %t, want %t", tc.result, got, tc.refused)
		}
	}
}

func TestNoteSentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind    diskchain.NoteKind
		want    string
		atStart bool
	}{
		{
			kind: diskchain.QcowV1, atStart: true,
			want: "/i/kali.qcow is a qcow version 1 image, an old format that some qemu builds cannot open. Convert it to qcow2",
		},
		{kind: diskchain.Qcow2V2, want: "/i/kali.qcow is a qcow2 version 2 image"},
		{kind: diskchain.Dirty, atStart: true, want: "qemu did not close /i/kali.qcow cleanly. Run qemu-img check -r all on it"},
		{kind: diskchain.Corrupt, want: "qemu marked /i/kali.qcow as corrupt"},
		{kind: diskchain.Zstd, want: "/i/kali.qcow uses zstd compression, which needs qemu 5.1 or later"},
		{kind: diskchain.ExtendedL2, want: "/i/kali.qcow uses extended L2 entries, which needs qemu 5.2 or later"},
		{kind: diskchain.QED, want: "/i/kali.qcow is a qed image"},
		{kind: diskchain.VMDK, want: "/i/kali.qcow is a vmdk image"},
	}

	for _, tc := range tests {
		note := diskchain.Note{Path: "/i/kali.qcow", Kind: tc.kind}

		if got := note.Sentence(); got != tc.want {
			t.Errorf("Note{Kind: %d}.Sentence() = %q, want %q", tc.kind, got, tc.want)
		}

		if got := note.AtStart(); got != tc.atStart {
			t.Errorf("Note{Kind: %d}.AtStart() = %t, want %t", tc.kind, got, tc.atStart)
		}
	}
}

// MapPaths maps every file a result names, its notes included, and keeps the backing name of a NotAFile.
func TestMapPaths(t *testing.T) {
	t.Parallel()

	upper := strings.ToUpper

	tests := []struct {
		in, want diskchain.Result
	}{
		{
			in: diskchain.Result{
				Members: []string{"/a", "/b"}, Problem: diskchain.Missing, At: "/c",
				Notes: []diskchain.Note{{Path: "/b", Kind: diskchain.QcowV1}},
			},
			want: diskchain.Result{
				Members: []string{"/A", "/B"}, Problem: diskchain.Missing, At: "/C",
				Notes: []diskchain.Note{{Path: "/B", Kind: diskchain.QcowV1}},
			},
		},
		{
			in:   diskchain.Result{Members: []string{"/a"}, Problem: diskchain.NotAFile, At: "nbd://h/x"},
			want: diskchain.Result{Members: []string{"/A"}, Problem: diskchain.NotAFile, At: "nbd://h/x"},
		},
	}

	for _, tc := range tests {
		if diff := cmp.Diff(tc.want, tc.in.MapPaths(upper)); diff != "" {
			t.Errorf("MapPaths mismatch (-want +got):\n%s", diff)
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

	// qcow version 1 headers with one field changed
	qcowWith := func(at int, value ...byte) []byte {
		data := dct.Qcow{Backing: "b.img"}.Bytes()
		copy(data[at:], value)

		return data
	}

	// a version 3 header with byte 104, its compression type, changed
	compression := func(features uint64, value byte) []byte {
		data := dct.Qcow2{HeaderLength: 112, Features: features}.Bytes()
		data[104] = value

		return data
	}

	qcow := diskchain.Header{Format: diskchain.FormatQcow, Version: 1}
	qcow2 := diskchain.Header{Format: diskchain.FormatQcow2, Version: 3}
	compressionBit := diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Incompatible: 1 << 3}

	tests := []struct {
		name    string
		data    []byte
		want    diskchain.Header
		wantErr string
	}{
		{name: "empty", data: nil, want: diskchain.Header{}},
		{name: "raw", data: []byte("hello"), want: diskchain.Header{}},
		{name: "a qcow2 magic with version 0 is raw", data: []byte("QFI\xfb\x00\x00\x00\x00"), want: diskchain.Header{}},
		{name: "a qcow2 magic with no version is raw", data: []byte("QFI\xfb\x00"), want: diskchain.Header{}},
		{
			name: "version 4", data: dct.Qcow2{Version: 4}.Bytes(),
			want:    diskchain.Header{Format: diskchain.FormatQcow2, Version: 4},
			wantErr: "unsupported qcow2 version 4",
		},
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
		{
			name: "qcow version 1 with a backing name",
			data: dct.Qcow{Backing: "../b.img"}.Bytes(),
			want: diskchain.Header{Format: diskchain.FormatQcow, Version: 1, Backing: "../b.img"},
		},
		{
			name: "qcow version 1 with AES encryption",
			data: dct.Qcow{Encryption: 1}.Bytes(),
			want: diskchain.Header{Format: diskchain.FormatQcow, Version: 1, Encryption: diskchain.EncryptionAES},
		},
		{
			name: "qcow version 1 shorter than its header", data: dct.Qcow{}.Bytes()[:40], want: qcow,
			wantErr: "invalid qcow2 header: 40 bytes long",
		},
		{
			name: "qcow version 1 of size 1", data: qcowWith(24, 0, 0, 0, 0, 0, 0, 0, 1), want: qcow,
			wantErr: "invalid qcow2 header: virtual size 1",
		},
		{
			name: "qcow version 1 with clusters of 128 KiB", data: qcowWith(32, 17), want: qcow,
			wantErr: "invalid qcow2 header: cluster bits 17",
		},
		{
			name: "qcow version 1 with L2 tables of 256 bytes", data: qcowWith(33, 5), want: qcow,
			wantErr: "invalid qcow2 header: L2 bits 5",
		},
		{
			name: "qcow version 1 with an unknown encryption method", data: qcowWith(36, 0, 0, 0, 2), want: qcow,
			wantErr: "invalid qcow2 header: encryption method 2",
		},
		{
			name: "qcow version 1 with a backing name over 1023 bytes", data: qcowWith(16, 0, 0, 4, 0), want: qcow,
			wantErr: "invalid qcow2 header: backing file name of 1024 bytes at 48",
		},
		{
			name: "qcow version 1 with a backing name past the end of the file", data: qcowWith(8, 0, 0, 0, 0, 0, 1, 0, 0),
			want:    qcow,
			wantErr: "invalid qcow2 header: reading the backing file name: EOF",
		},
		{
			name: "version 3 with LUKS, dirty, corrupt and unknown bits",
			data: dct.Qcow2{Encryption: 2, Features: 1 | 2 | 1<<9}.Bytes(),
			want: diskchain.Header{
				Format: diskchain.FormatQcow2, Version: 3, Encryption: diskchain.EncryptionLUKS, Incompatible: 1 | 2 | 1<<9,
			},
		},
		{
			name: "version 3 with zstd compression",
			data: dct.Qcow2{Zstd: true}.Bytes(),
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Incompatible: 1 << 3, Zstd: true},
		},
		{
			name: "version 3 with an unknown encryption method", data: dct.Qcow2{Encryption: 3}.Bytes(), want: qcow2,
			wantErr: "invalid qcow2 header: encryption method 3",
		},
		{
			name: "version 3 with an unknown compression type", data: compression(1<<3, 2), want: compressionBit,
			wantErr: "invalid qcow2 header: compression type 2",
		},
		{
			name: "version 3 with zstd compression and no compression bit", data: compression(0, 1), want: qcow2,
			wantErr: "invalid qcow2 header: compression type 1 with compression bit false",
		},
		{
			name: "version 3 with the compression bit and zlib compression", data: compression(1<<3, 0), want: compressionBit,
			wantErr: "invalid qcow2 header: compression type 0 with compression bit true",
		},
		{
			name: "a compression type past a header of 104 bytes is not read",
			data: append(dct.Qcow2{}.Bytes(), 1),
			want: qcow2,
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
		dct.Qcow{Backing: "b.img"}.Bytes(),
		dct.Qcow{Encryption: 1}.Bytes(),
		dct.Qcow2{Encryption: 2, Features: 1 | 2 | 1<<9}.Bytes(),
		dct.Qcow2{Zstd: true, Features: 1 << 4}.Bytes(),
	}
}

// checkHeader fails when h, parsed from data with error err, says what no header qemu parses can say: a qcow or qcow2
// image that is not one, a version or field qemu refuses as invalid, or a backing name qemu would not read.
func checkHeader(data []byte, h diskchain.Header, err error) error {
	isQcow := bytes.HasPrefix(data, []byte("QFI\xfb"))
	isV1 := isQcow && len(data) >= 8 && binary.BigEndian.Uint32(data[4:]) == 1
	isQcow2 := isQcow && len(data) >= 8 && binary.BigEndian.Uint32(data[4:]) >= 2
	compression := h.Incompatible&diskchain.FeatureCompression != 0

	switch {
	case isV1 != (h.Format == diskchain.FormatQcow), isQcow2 != (h.Format == diskchain.FormatQcow2):
		return fmt.Errorf("format %q for data starting %q", h.Format, data[:min(8, len(data))])
	case err != nil && !isV1 && !isQcow2:
		return fmt.Errorf("error %w for data that is not qcow or qcow2", err)
	case err != nil:
		return nil //nolint:nilerr // ParseHeader refuses a header qemu would refuse
	case len(h.Backing) > 1023:
		return fmt.Errorf("backing name of %d bytes", len(h.Backing))
	case (isV1 || isQcow2) && h.Version != 1 && h.Version != 2 && h.Version != 3:
		return fmt.Errorf("version %d without an error", h.Version)
	case h.Version != 3 && (h.Incompatible != 0 || h.Zstd):
		return fmt.Errorf("version %d with feature bits 0x%x", h.Version, h.Incompatible)
	case h.Zstd != compression:
		return fmt.Errorf("zstd %t with compression bit %t", h.Zstd, compression)
	case h.Encryption > diskchain.EncryptionLUKS, h.Format == diskchain.FormatQcow && h.Encryption > diskchain.EncryptionAES:
		return fmt.Errorf("encryption method %d for a %s image", h.Encryption, h.Format)
	case h.DataFile != (h.Incompatible&diskchain.FeatureDataFile != 0):
		return fmt.Errorf("data file %t with feature bits 0x%x", h.DataFile, h.Incompatible)
	}

	return nil
}
