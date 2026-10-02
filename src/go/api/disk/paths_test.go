package disk

import (
	"errors"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
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
			t.Errorf("Resolve(%q) = %q, %v; want ErrNotManaged", p, got, err)
		case want != "" && (err != nil || got != want):
			t.Errorf("Resolve(%q) = %q, %v; want %q", p, got, err, want)
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
			t.Errorf("ValidateMinimegaPath with %q = %v, want ErrUnsafeName naming it", bad, err)
		}
	}
}

// Every path ValidateMinimegaPath accepts reaches a minimega command as the
// one argument it was, and minimega's command line would split, unquote or
// cut short some of the paths it refuses.
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
			t.Errorf("minimega reads %q as %v (%v), not the paths %q and %q", name, cmd, err, src, dst)
		}
	}

	if accepted == 0 || splits == 0 {
		t.Fatalf("%d names accepted and %d refused names split; want some of each", accepted, splits)
	}
}
