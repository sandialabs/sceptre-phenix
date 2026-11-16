package disk

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// A new image's name is relative to the folder of its source. A snapshot is a qcow2 image, so it gets .qcow2 unless
// it has a qcow2 extension. A copy or a rename keeps the format of its source, so it keeps or gets the source's
// extension, and a name with a known image extension of another format is refused.
func TestNewImageName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src, dst   string
		sameFormat bool
		want       string // empty when refused
	}{
		"snapshot":                    {src: "/files/a.qc2", dst: "new", want: "/files/new.qcow2"},
		"snapshot to .qc2":            {src: "/files/a.qc2", dst: "new.qc2", want: "/files/new.qc2"},
		"snapshot of qcow":            {src: "/files/old.qcow", dst: "new", want: "/files/new.qcow2"},
		"snapshot of qcow to .qcow":   {src: "/files/old.qcow", dst: "new.qcow", want: "/files/new.qcow.qcow2"},
		"snapshot of an ISO to .iso":  {src: "/files/boot.iso", dst: "new.iso", want: "/files/new.iso.qcow2"},
		"copy":                        {src: "/files/win/b.qcow2", dst: "new", sameFormat: true, want: "/files/win/new.qcow2"},
		"copy of qcow2 to .qc2":       {src: "/files/win/b.qcow2", dst: "/abs/new.qc2", sameFormat: true, want: "/abs/new.qc2"},
		"copy of qcow2 to .qcow":      {src: "/files/a.qc2", dst: "new.qcow", sameFormat: true},
		"copy of qcow2 to .iso":       {src: "/files/a.qc2", dst: "new.iso", sameFormat: true},
		"copy of qcow":                {src: "/files/old.qcow", dst: "new", sameFormat: true, want: "/files/new.qcow"},
		"copy of qcow to .qcow":       {src: "/files/old.qcow", dst: "/abs/new.qcow", sameFormat: true, want: "/abs/new.qcow"},
		"copy of qcow to .qc2":        {src: "/files/old.qcow", dst: "new.qc2", sameFormat: true, want: "/files/new.qc2"},
		"copy of qcow to other":       {src: "/files/old.qcow", dst: "new.img", sameFormat: true, want: "/files/new.img.qcow"},
		"copy of qcow to .hdd":        {src: "/files/old.qcow", dst: "new.hdd", sameFormat: true},
		"copy of a .qcow2.qcow image": {src: "/files/a.qcow2.qcow", dst: "../b", sameFormat: true, want: "/b.qcow"},
		"copy of an ISO":              {src: "/files/boot.iso", dst: "bar", sameFormat: true, want: "/files/bar.iso"},
		"copy of an ISO to .iso":      {src: "/files/boot.iso", dst: "sub/bar.iso", sameFormat: true, want: "/files/sub/bar.iso"},
		"copy of an ISO to a folder":  {src: "/files/iso/boot.iso", dst: "../bar", sameFormat: true, want: "/files/bar.iso"},
		"copy of an ISO to other":     {src: "/files/boot.iso", dst: "/abs/bar.img", sameFormat: true, want: "/abs/bar.img.iso"},
		"copy of an ISO to .qcow2":    {src: "/files/boot.iso", dst: "bar.qcow2", sameFormat: true},
		"copy of an ISO to .qcow":     {src: "/files/boot.iso", dst: "/abs/bar.qcow", sameFormat: true},
		"copy of an HDD":              {src: "/files/d.hdd", dst: "/abs/e", sameFormat: true, want: "/abs/e.hdd"},
		"copy of an HDD to .hdd":      {src: "/files/d.hdd", dst: "e.hdd", sameFormat: true, want: "/files/e.hdd"},
		"copy of an HDD to .iso":      {src: "/files/d.hdd", dst: "e.iso", sameFormat: true},
		"copy of a rootfs":            {src: "/files/c_rootfs.tgz", dst: "d", sameFormat: true, want: "/files/d_rootfs.tgz"},
		"copy of a rootfs to rootfs":  {src: "/files/c_rootfs.tgz", dst: "d_rootfs.tgz", sameFormat: true, want: "/files/d_rootfs.tgz"},
		"copy of a rootfs to .tgz":    {src: "/files/c_rootfs.tgz", dst: "d.tgz", sameFormat: true, want: "/files/d.tgz_rootfs.tgz"},
		"copy of a rootfs to .qc2":    {src: "/files/c_rootfs.tgz", dst: "d.qc2", sameFormat: true},
		"copy of an unknown image":    {src: "/files/x.img", dst: "y", sameFormat: true, want: "/files/y.qcow2"},
		"copy of unknown to .iso":     {src: "/files/x.img", dst: "y.iso", sameFormat: true, want: "/files/y.iso.qcow2"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := newImageName(tt.src, tt.dst, tt.sameFormat)

			switch {
			case tt.want == "" && !errors.Is(err, ErrWrongExtension):
				t.Errorf("newImageName(%q, %q, %t) = %q, %v, want an error wrapping %v",
					tt.src, tt.dst, tt.sameFormat, got, err, ErrWrongExtension)
			case tt.want != "" && (err != nil || got != tt.want):
				t.Errorf("newImageName(%q, %q, %t) = %q, %v, want %q", tt.src, tt.dst, tt.sameFormat, got, err, tt.want)
			}
		})
	}
}

// The extensions each source extension's copies may end in; a snapshot, or a copy of an image with none of these
// extensions, is named as a qcow2 image.
var newImageNameGroups = map[string][]string{ //nolint:gochecknoglobals // test table
	".qcow2":      {".qcow2", ".qc2"},
	".qc2":        {".qcow2", ".qc2"},
	".qcow":       {".qcow", ".qcow2", ".qc2"},
	".iso":        {".iso"},
	".hdd":        {".hdd"},
	"_rootfs.tgz": {"_rootfs.tgz"},
}

// A new image's name becomes an absolute path that ends in an extension of its source's group, kept as given once it
// has one, so normalizing it again changes nothing. A copy or a rename to a known image extension of another group is
// refused instead.
func TestNewImageNameProperties(t *testing.T) {
	t.Parallel()

	extOf := func(name string) string {
		for ext := range newImageNameGroups {
			if strings.HasSuffix(name, ext) {
				return ext
			}
		}

		return ""
	}

	rapid.Check(t, func(t *rapid.T) {
		src := rapid.SampledFrom([]string{
			"/files/a.qc2", "/files/win/b.qcow2", "/outside/c.img", "/files/old.qcow",
			"/files/boot.iso", "/files/d.hdd", "/files/e_rootfs.tgz",
		}).Draw(t, "src")
		dst := rapid.SampledFrom([]string{"", "/abs/", "sub/", "../", "./"}).Draw(t, "folder") + "new" +
			rapid.SampledFrom([]string{"", ".qc2", ".qcow2", ".qcow", ".img", ".qcow2.bak", ".iso", ".hdd", "_rootfs.tgz", ".tgz"}).
				Draw(t, "extension")
		sameFormat := rapid.Bool().Draw(t, "sameFormat")

		exts, strict := newImageNameGroups[".qcow2"], false
		if group, ok := newImageNameGroups[extOf(src)]; sameFormat && ok {
			exts, strict = group, true
		}

		ends := func(name string) bool {
			return slices.ContainsFunc(exts, func(ext string) bool { return strings.HasSuffix(name, ext) })
		}

		got, err := newImageName(src, dst, sameFormat)

		if refuse := strict && !ends(dst) && extOf(dst) != ""; refuse != (err != nil) {
			t.Fatalf("newImageName(%q, %q, %t) = %q, %v, want refused %t", src, dst, sameFormat, got, err, refuse)
		}

		if err != nil {
			if !errors.Is(err, ErrWrongExtension) {
				t.Fatalf("newImageName(%q, %q, %t) error = %v, want it to wrap %v",
					src, dst, sameFormat, err, ErrWrongExtension)
			}

			return
		}

		if !filepath.IsAbs(got) || !ends(got) {
			t.Fatalf("newImageName(%q, %q, %t) = %q, want an absolute path ending in one of %q",
				src, dst, sameFormat, got, exts)
		}

		if again, err := newImageName(src, got, sameFormat); err != nil || again != got {
			t.Fatalf("newImageName(%q, %q, %t) = %q, %v, want it unchanged", src, got, sameFormat, again, err)
		}
	})
}

// A new image must name a file inside the files directory; a folder, an empty name or a path out of the files
// directory is refused before anything changes.
func TestNewImagePath(t *testing.T) {
	dir := useFilesDir(t)

	tests := map[string]struct {
		dst     string
		want    string
		wantErr error
	}{
		"relative":           {dst: "new", want: dir + "/new.qcow2"},
		"sub folder":         {dst: "sub/new.qc2", want: dir + "/sub/new.qc2"},
		"empty":              {dst: "", wantErr: ErrNotManaged},
		"folder":             {dst: "sub/", wantErr: ErrNotManaged},
		"dot":                {dst: ".", wantErr: ErrNotManaged},
		"dot dot":            {dst: "sub/..", wantErr: ErrNotManaged},
		"out of the files":   {dst: "../../new", wantErr: ErrNotManaged},
		"other format":       {dst: "new.iso", wantErr: ErrWrongExtension},
		"absolute elsewhere": {dst: "/etc/new", wantErr: ErrNotManaged},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := NewImagePath(dir+"/a.qc2", tt.dst, true)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("NewImagePath(a.qc2, %q) = %q, %v, want %q, %v", tt.dst, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// ValidateSize takes an optionally signed integer and one unit letter, and nothing else.
func TestValidateSizeProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		size := rapid.SampledFrom([]string{"", "+", "-"}).Draw(t, "sign") +
			rapid.StringMatching(`[0-9]{1,6}`).Draw(t, "number") +
			rapid.SampledFrom([]string{"K", "M", "G", "T", "P", "E"}).Draw(t, "unit")

		if err := ValidateSize(size); err != nil {
			t.Fatalf("ValidateSize(%q) error = %v, want none", size, err)
		}

		bad := size + rapid.StringMatching(`[^\n]{1,4}`).Draw(t, "after")
		if err := ValidateSize(bad); !errors.Is(err, ErrInvalidSize) {
			t.Fatalf("ValidateSize(%q) error = %v, want %v", bad, err, ErrInvalidSize)
		}
	})
}
