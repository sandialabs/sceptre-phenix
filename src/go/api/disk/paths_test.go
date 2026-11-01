package disk

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"pgregory.net/rapid"
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
