package disk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/diskchain/diskchaintest"
	"phenix/util/mm/mmtest"
)

// writeOverlay writes a qcow2 header at path backed by backing, for the walker, and the sidecar the fake qemu-img
// follows; a sidecar of "" leaves it out.
func writeOverlay(t *testing.T, path, backing, sidecar string) {
	t.Helper()

	diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Backing: backing})

	if sidecar != "" {
		writeFile(t, path+".backing", sidecar)
	}
}

// chainFields are the Details fields that tell whether and where a chain is broken.
type chainFields struct {
	BackingImages  []string
	BrokenChain    bool
	ChainProblem   string
	MissingBacking string
}

func fieldsOf(d Details) chainFields {
	return chainFields{
		BackingImages: d.BackingImages, BrokenChain: d.BrokenChain, ChainProblem: d.ChainProblem,
		MissingBacking: d.MissingBacking,
	}
}

func getImage(t *testing.T, path string) Details {
	t.Helper()

	got, err := GetImage(path)
	if err != nil {
		t.Fatalf("GetImage(%s) error = %v", path, err)
	}

	return got
}

// A missing backing file marks the image broken and names the file, and the readable part of the chain is kept. The
// broken image is cached until the missing file appears, and a base deleted later breaks it again.
func TestGetImageReportsMissingBackingFile(t *testing.T) {
	calls := useFakeQemuImg(t)

	dir := t.TempDir()
	over, mid, base := filepath.Join(dir, "over.qc2"), filepath.Join(dir, "mid.qc2"), filepath.Join(dir, "base.qc2")

	writeOverlay(t, over, "mid.qc2", "mid.qc2")
	writeOverlay(t, mid, base, base)

	broken := chainFields{
		BackingImages: []string{mid}, BrokenChain: true,
		ChainProblem: "backing file " + base + " is missing", MissingBacking: base,
	}

	if diff := cmp.Diff(broken, fieldsOf(getImage(t, over))); diff != "" {
		t.Errorf("GetImage(image whose base is missing) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(broken, fieldsOf(getImage(t, over))); diff != "" {
		t.Errorf("GetImage(image whose base is missing) again mismatch (-want +got):\n%s", diff)
	}

	// the chain, then the image alone, once only
	if n := calls(); n != 2 {
		t.Errorf("qemu-img runs for a broken image listed twice = %d, want 2", n)
	}

	writeFile(t, base, "raw base")

	whole := chainFields{BackingImages: []string{mid, base}, BrokenChain: false, ChainProblem: "", MissingBacking: ""}
	if diff := cmp.Diff(whole, fieldsOf(getImage(t, over))); diff != "" {
		t.Errorf("GetImage(image once its base exists) mismatch (-want +got):\n%s", diff)
	}

	if err := os.Remove(base); err != nil {
		t.Fatalf("os.Remove(%s) error = %v", base, err)
	}

	if diff := cmp.Diff(broken, fieldsOf(getImage(t, over))); diff != "" {
		t.Errorf("GetImage(image once its base is deleted) mismatch (-want +got):\n%s", diff)
	}
}

func TestGetImageReportsOtherChainProblems(t *testing.T) {
	tests := map[string]struct {
		// write writes the images in dir and returns the image to get
		write func(t *testing.T, dir string) string
		want  func(dir string) chainFields
	}{
		// qemu-img fails on a missing file the sidecar names; the headers loop
		"a chain that loops is broken, with no missing file": {
			write: func(t *testing.T, dir string) string {
				t.Helper()

				writeOverlay(t, dir+"/a.qc2", "b.qc2", "gone.qc2")
				writeOverlay(t, dir+"/b.qc2", "a.qc2", "")

				return dir + "/a.qc2"
			},
			want: func(dir string) chainFields {
				return chainFields{
					BackingImages: []string{dir + "/b.qc2"}, BrokenChain: true,
					ChainProblem: "backing chain loops back to " + dir + "/a.qc2", MissingBacking: "",
				}
			},
		},
		// phenix cannot follow a vmdk, so it does not call the chain broken
		"a chain phenix cannot follow is not broken": {
			write: func(t *testing.T, dir string) string {
				t.Helper()

				writeOverlay(t, dir+"/a.qc2", "b.vmdk", "gone.qc2")
				diskchaintest.WriteFile(t, dir+"/b.vmdk", []byte("KDMV"))

				return dir + "/a.qc2"
			},
			want: func(string) chainFields { return chainFields{BackingImages: []string{}} },
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useFakeQemuImg(t)

			dir := t.TempDir()
			path := tt.write(t, dir)

			if diff := cmp.Diff(tt.want(dir), fieldsOf(getImage(t, path))); diff != "" {
				t.Errorf("GetImage(%s) mismatch (-want +got):\n%s", path, diff)
			}
		})
	}
}

// Through minimega, a backing file on the last row of `disk info` means the chain was cut short: phenix reads the
// chain itself when it can, and an image it cannot read is not called broken.
func TestGetImageThroughMinimegaReportsBrokenChain(t *testing.T) {
	dir := useFilesDir(t)
	t.Setenv("PATH", t.TempDir())

	writeOverlay(t, dir+"/over.qc2", "base.qc2", "")

	info := []string{"image", "format", "virtualsize", "disksize", "backingfile", "inuse"}
	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "disk info " + dir + "/over.qc2 recursive":
			return []*minicli.Response{
				mmtest.Tabular("head", info, []string{dir + "/over.qc2", "qcow2", "1G", "2K", "base.qc2", "false"}),
			}
		case "disk info " + dir + "/elsewhere.qc2 recursive":
			return []*minicli.Response{
				mmtest.Tabular("head", info, []string{dir + "/elsewhere.qc2", "qcow2", "1G", "2K", "base.qc2", "false"}),
			}
		}

		return nil
	})

	want := chainFields{
		BackingImages: []string{}, BrokenChain: true,
		ChainProblem: "backing file " + dir + "/base.qc2 is missing", MissingBacking: dir + "/base.qc2",
	}

	opts := cmpopts.EquateEmpty()
	if diff := cmp.Diff(want, fieldsOf(getImage(t, "over.qc2")), opts); diff != "" {
		t.Errorf("GetImage(%q) through minimega mismatch (-want +got):\n%s", "over.qc2", diff)
	}

	// minimega sees elsewhere.qc2, phenix does not
	images := resolveImage(dir + "/elsewhere.qc2")
	if len(images) != 1 {
		t.Fatalf("resolveImage(image phenix cannot read) = %d images, want 1", len(images))
	}

	if diff := cmp.Diff(chainFields{}, fieldsOf(images[0]), opts); diff != "" {
		t.Errorf("resolveImage(image phenix cannot read) mismatch (-want +got):\n%s", diff)
	}
}
