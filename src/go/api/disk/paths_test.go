package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/diskchain"
)

// The disk actions act on images inside the files directory, except in the
// folders the listing leaves out.
func TestResolve(t *testing.T) {
	dir := useFilesDir(t)

	for p, want := range map[string]string{
		"a.qc2":                       dir + "/a.qc2",
		"win/10/a b.qc2":              dir + "/win/10/a b.qc2",
		dir + "/win/./x/../a.qc2":     dir + "/win/a.qc2",
		"files/a.qc2":                 dir + "/files/a.qc2",
		"a/b/files/a.qc2":             dir + "/a/b/files/a.qc2",
		"a/transfer_1/a.qc2":          dir + "/a/transfer_1/a.qc2",
		"win/../a.qc2":                dir + "/a.qc2",
		"":                            "",
		".":                           "",
		dir:                           "",
		"../a.qc2":                    "",
		"win/../../a.qc2":             "",
		"/etc/a.qc2":                  "",
		dir + "-other/a.qc2":          "",
		"exp/files/s.hdd":             "",
		"exp/tmp/snapshot-1.qc2":      "",
		"exp/miniccc_responses/x.qc2": "",
		".hidden/a.qc2":               "",
		"win/.a.qc2":                  "",
		"..a.qc2":                     "",
		"win/lost+found/a.qc2":        "",
		"saved/vm/a.hdd":              "",
		"transfer_1/a.qc2":            "",
	} {
		got, err := Resolve(p)

		switch {
		case want == "" && !errors.Is(err, ErrNotManaged):
			t.Errorf("Resolve(%q) = %q, %v; want error %v", p, got, err, ErrNotManaged)
		case want != "" && (err != nil || got != want):
			t.Errorf("Resolve(%q) = %q, %v; want %q, nil", p, got, err, want)
		}
	}
}

func TestValidateMinimegaPath(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"/f/a+b/x_y.v2.qcow2", "/f/ü/x.qc2", "/f/a&b=c%20(1)@x:y.qc2", "win/win10.qcow2"} {
		if err := ValidateMinimegaPath(p); err != nil {
			t.Errorf("ValidateMinimegaPath(%q) = %v, want nil", p, err)
		}
	}

	for _, bad := range []string{" ", "\t", "\n", " ", "#", `"`, "'", "`", `\`, "*", "?", "[", "{", "$", ",", "\x00"} {
		err := ValidateMinimegaPath("/f/a" + bad + "b.qc2")
		if !errors.Is(err, ErrUnsafeName) || !strings.Contains(err.Error(), "contains") {
			t.Errorf("ValidateMinimegaPath(%q) = %v, want %v saying what it contains", "/f/a"+bad+"b.qc2", err, ErrUnsafeName)
		}
	}
}

// Each accepted path reaches minimega as one argument; minimega's parser would split or alter some refused ones.
func TestValidPathsReachMinimegaWhole(t *testing.T) {
	minicli.Reset()
	t.Cleanup(minicli.Reset)

	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"disk snapshot <image> [dst image]"},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})

	var accepted, splits int

	for _, name := range []string{
		"a.qc2", "a+b.qc2", "a&b.qc2", "a=b.qc2", "a%20b.qc2", "a(1).qc2", "a;b.qc2", "a|b.qc2", "a<b>.qc2",
		"a:b.qc2", "a@b.qc2", "a!b.qc2", "a~b.qc2", "ü.qc2", "a-b_c.qc2", "-a.qc2",
		"a b.qc2", "a\tb.qc2", "a b.qc2", "a　b.qc2", `a"b.qc2`, "a'b.qc2", `a\b.qc2`, "a#b.qc2",
	} {
		src, dst := "/f/win/"+name, "/f/"+name+".new"

		cmd, err := minicli.Compile("disk snapshot " + src + " " + dst)
		whole := err == nil && cmd.StringArgs["image"] == src && cmd.StringArgs["dst"] == dst

		if ValidateMinimegaPath(src) != nil || ValidateMinimegaPath(dst) != nil {
			if !whole {
				splits++
			}

			continue
		}

		accepted++

		if !whole {
			t.Errorf("minicli.Compile(disk snapshot %q %q) = %v, %v; want the two paths whole", src, dst, cmd, err)
		}
	}

	if accepted == 0 || splits == 0 {
		t.Errorf("names accepted = %d, refused names minimega splits = %d; want some of each", accepted, splits)
	}
}

// genPathsRel draws a relative path of segments the disk listing treats specially.
func genPathsRel() *rapid.Generator[string] {
	segment := rapid.SampledFrom([]string{
		"a", "win", "x.qc2", "files", "tmp", "miniccc_responses", "saved", "transfer_1", "lost+found",
		".hidden", "..a", "..", ".", "",
	})

	return rapid.Map(rapid.SliceOfN(segment, 0, 5), func(segs []string) string {
		return strings.TrimLeft(strings.Join(segs, "/"), "/")
	})
}

// A path Resolve accepts is clean, inside the files directory and accepted
// again as it is; a relative path resolves as the absolute one it names; and
// every folder holding an accepted image inside the files directory is accepted.
func TestResolveProperties(t *testing.T) {
	dir := useFilesDir(t)

	rapid.Check(t, func(t *rapid.T) {
		rel := genPathsRel().Draw(t, "rel")

		got, err := Resolve(rel)
		if err != nil && !errors.Is(err, ErrNotManaged) {
			t.Fatalf("Resolve(%q) error = %v, want nil or %v", rel, err, ErrNotManaged)
		}

		abs, absErr := Resolve(dir + "/" + rel)
		if abs != got || (absErr == nil) != (err == nil) {
			t.Fatalf("Resolve(%q) = %q, %v; want %q, %v as for Resolve(%q)", dir+"/"+rel, abs, absErr, got, err, rel)
		}

		if err != nil {
			return
		}

		if got != filepath.Clean(got) || !strings.HasPrefix(got, dir+"/") {
			t.Fatalf("Resolve(%q) = %q, want a clean path under %s", rel, got, dir)
		}

		if again, err := Resolve(got); again != got || err != nil {
			t.Fatalf("Resolve(%q) = %q, %v; want %q, nil", got, again, err, got)
		}

		for folder := filepath.Dir(got); folder != dir; folder = filepath.Dir(folder) {
			if _, err := Resolve(folder); err != nil {
				t.Fatalf("Resolve(%q) error = %v, but Resolve(%q) = %q under it", folder, err, rel, got)
			}
		}
	})
}

// genPathsName draws a file name, most often of characters minimega takes as they are.
func genPathsName() *rapid.Generator[string] {
	safe := []rune("aZ0-_.+&=%()|<>;:@!~ü")
	odd := []rune(" \t 　\"'`\\#*?[]{}$,\x00\n")

	return rapid.OneOf(
		rapid.StringOfN(rapid.SampledFrom(safe), 1, 8, -1),
		rapid.StringOfN(rapid.SampledFrom(append(safe, odd...)), 1, 8, -1),
	)
}

// Every pair of paths ValidateMinimegaPath accepts reaches minimega as two whole arguments.
func TestValidPathsReachMinimegaWholeProperties(t *testing.T) {
	minicli.Reset()
	t.Cleanup(minicli.Reset)

	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"disk snapshot <image> [dst image]"},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})

	rapid.Check(t, func(t *rapid.T) {
		src := "/f/" + genPathsName().Draw(t, "src")
		dst := "/f/" + genPathsName().Draw(t, "dst") + ".new"

		if ValidateMinimegaPath(src) != nil || ValidateMinimegaPath(dst) != nil {
			t.Skip("refused")
		}

		cmd, err := minicli.Compile("disk snapshot " + src + " " + dst)
		if err != nil || cmd.StringArgs["image"] != src || cmd.StringArgs["dst"] != dst {
			t.Fatalf("minicli.Compile(disk snapshot %q %q) = %v, %v; want the two paths whole", src, dst, cmd, err)
		}
	})
}

// skipped leaves out hidden names and lost+found at any depth, minimega's saved and transfer folders at the top, and
// an experiment's files, tmp and miniccc_responses folders one level down.
func TestSkipped(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"a.qc2":                   false,
		"win/10/a.qc2":            false,
		".hidden":                 true,
		"win/.a.qc2":              true,
		"lost+found":              true,
		"a/b/lost+found/x":        true,
		"saved":                   true,
		"transfer_1/a.qc2":        true,
		"win/saved/a.qc2":         false,
		"win/transfer_1":          false,
		"exp/files":               true,
		"exp/tmp/a.qc2":           true,
		"exp/miniccc_responses":   true,
		"files/a.qc2":             false,
		"a/b/files":               false,
		"exp/filesystem/a.qc2":    false,
		"":                        false,
		"savedvm/a.qc2":           false,
		"transfer/a.qc2":          false,
		filepath.Join("e", "tmp"): true,
	}

	for rel, want := range tests {
		if got := skipped(rel); got != want {
			t.Errorf("skipped(%q) = %t, want %t", rel, got, want)
		}
	}
}

// tooDeep allows folders down to maxDepth levels below the files directory, and the files directory itself.
func TestTooDeep(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		".":                                    false,
		"a":                                    false,
		strings.Repeat("d/", maxDepth-1) + "d": false,
		strings.Repeat("d/", maxDepth) + "d":   true,
		strings.Repeat("d/", maxDepth+3) + "d": true,
	}

	for rel, want := range tests {
		if got := tooDeep("/f", rel); got != want {
			t.Errorf("tooDeep(/f, %d segments) = %t, want %t", strings.Count(rel, "/")+1, got, want)
		}
	}
}

// locate places an image relative to the files directory, and marks read-only what the disk actions refuse.
func TestLocate(t *testing.T) {
	t.Parallel()

	tests := map[string]Details{
		"/f/win/a.qc2":     {FullPath: "/f/win/a.qc2", RelativePath: "win/a.qc2"},
		"/f/exp/tmp/a.qc2": {FullPath: "/f/exp/tmp/a.qc2", RelativePath: "exp/tmp/a.qc2", ReadOnly: true},
		"/g/a.qc2":         {FullPath: "/g/a.qc2", OutsideFilesDir: true, ReadOnly: true},
		"/f-other/a.qc2":   {FullPath: "/f-other/a.qc2", OutsideFilesDir: true, ReadOnly: true},
		"/f":               {FullPath: "/f", RelativePath: ".", ReadOnly: true},
	}

	for path, want := range tests {
		got := Details{FullPath: path, RelativePath: "stale", OutsideFilesDir: true, ReadOnly: false}
		locate(&got, "/f")

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("locate(%s, /f) mismatch (-want +got):\n%s", path, diff)
		}
	}
}

// pathsFiles makes, in a new folder, a regular file, a folder, a FIFO and symlinks to each, and a dangling symlink,
// and returns the folder.
func pathsFiles(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.qc2"), "a")

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatalf("os.Mkdir(sub) error = %v", err)
	}

	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.qc2"), 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo(pipe.qc2) error = %v", err)
	}

	for link, target := range map[string]string{
		"link.qc2": "a.qc2", "sublink": "sub", "pipelink.qc2": "pipe.qc2", "dangling.qc2": "gone.qc2",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatalf("os.Symlink(%s, %s) error = %v", target, link, err)
		}
	}

	return dir
}

// openRegular opens a regular file, or a symlink to one, and refuses anything else as not managed, without blocking
// on a FIFO; a missing file is not found.
func TestOpenRegular(t *testing.T) {
	t.Parallel()

	dir := pathsFiles(t)

	tests := map[string]error{
		"a.qc2":        nil,
		"link.qc2":     nil,
		"sub":          ErrNotManaged,
		"pipe.qc2":     ErrNotManaged,
		"pipelink.qc2": ErrNotManaged,
		"gone.qc2":     fs.ErrNotExist,
		"dangling.qc2": fs.ErrNotExist,
		"/dev/null":    ErrNotManaged,
	}

	for name, want := range tests {
		path := name
		if !filepath.IsAbs(name) {
			path = filepath.Join(dir, name)
		}

		f, err := openRegular(path)
		if !errors.Is(err, want) || (err == nil) != (want == nil) {
			t.Errorf("openRegular(%s) error = %v, want %v", name, err, want)
		}

		if errors.Is(want, fs.ErrNotExist) && errors.Is(err, ErrNotManaged) {
			t.Errorf("openRegular(%s) error = %v, want it not %v", name, err, ErrNotManaged)
		}

		if (f != nil) != (want == nil) {
			t.Errorf("openRegular(%s) file = %v, want one only without an error", name, f)
		}

		if f != nil {
			_ = f.Close()
		}
	}
}

// usableImage keeps only what phenix can open as a regular file outside the system directories.
func TestUsableImage(t *testing.T) {
	t.Parallel()

	dir := pathsFiles(t)

	tests := map[string]bool{
		"a.qc2": true, "link.qc2": true, "sub": false, "pipe.qc2": false, "gone.qc2": false, "dangling.qc2": false,
	}

	for name, want := range tests {
		if got := usableImage(filepath.Join(dir, name)); got != want {
			t.Errorf("usableImage(%s) = %t, want %t", name, got, want)
		}
	}

	// most of these paths do not exist, so the error shows that the system directory check refused them
	for _, path := range []string{"/dev/null", "/proc/self/exe", "/run/a.qc2", "/sys/a.qc2", "/dev/../dev/a.qc2"} {
		if usableImage(path) {
			t.Errorf("usableImage(%s) = true, want false for a system directory", path)
		}

		if f, err := openRegular(path); !errors.Is(err, diskchain.ErrSystemDir) {
			if f != nil {
				_ = f.Close()
			}

			t.Errorf("openRegular(%s) error = %v, want %v", path, err, diskchain.ErrSystemDir)
		}
	}
}

// minimegaMayInspect lets minimega inspect a regular file or a symlink to one, and a file phenix cannot see, but not
// a folder, FIFO or dangling symlink, which qemu-img could block on or fail.
func TestMinimegaMayInspect(t *testing.T) {
	t.Parallel()

	dir := pathsFiles(t)

	tests := map[string]bool{
		"a.qc2": true, "link.qc2": true, "gone.qc2": true,
		"sub": false, "sublink": false, "pipe.qc2": false, "pipelink.qc2": false, "dangling.qc2": false,
	}

	for name, want := range tests {
		if got := minimegaMayInspect(filepath.Join(dir, name)); got != want {
			t.Errorf("minimegaMayInspect(%s) = %t, want %t", name, got, want)
		}
	}
}

// requireFile accepts a regular file or a symlink to one, gives a missing file as not found, and refuses the rest.
func TestRequireFile(t *testing.T) {
	t.Parallel()

	dir := pathsFiles(t)

	tests := map[string]error{
		"a.qc2": nil, "link.qc2": nil, "gone.qc2": fs.ErrNotExist, "dangling.qc2": fs.ErrNotExist,
		"sub": ErrNotManaged, "sublink": ErrNotManaged, "pipe.qc2": ErrNotManaged,
	}

	for name, want := range tests {
		err := requireFile(filepath.Join(dir, name))
		if !errors.Is(err, want) || (err == nil) != (want == nil) {
			t.Errorf("requireFile(%s) error = %v, want %v", name, err, want)
		}
	}
}

// imageFiles fails for a files directory that is missing or that it cannot read.
func TestImageFilesErrors(t *testing.T) {
	t.Parallel()

	if _, err := imageFiles(filepath.Join(t.TempDir(), "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("imageFiles(missing folder) error = %v, want %v", err, fs.ErrNotExist)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads folders whatever their permissions")
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.qc2"), "a")

	if err := os.Chmod(dir, 0); err != nil {
		t.Fatalf("os.Chmod(%s, 0) error = %v", dir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })

	paths, err := imageFiles(dir)
	if !errors.Is(err, fs.ErrPermission) || paths != nil {
		t.Errorf("imageFiles(unreadable folder) = %v, %v; want nil, %v", paths, err, fs.ErrPermission)
	}
}
