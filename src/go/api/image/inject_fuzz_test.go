package image_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/image"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// FuzzInjectMiniExe checks that, for any executable directory and disk,
// InjectMiniExe sends no command or one disk inject that minicli reads back
// as the disk and src:dst pairs that minimega takes, with the executable as
// the source of its pair.
func FuzzInjectMiniExe(f *testing.F) {
	f.Add("/opt/minimega/bin", "/phenix/images/foo.qc2", false, false)
	f.Add("/opt/minimega/bin", "/phenix/images/win.qc2:2", true, true)
	f.Add("/dir with spaces/#x", "/images/a b.qc2", true, false)
	f.Add(`/q"uote\`, "d.qc2", false, true)
	f.Add("/$HOME/bin", "${DISK}", false, false)
	f.Add("/a:b", "d.qc2:none", false, false)
	f.Add("/new\nline\ttab", "d\n.qc2", true, true)
	f.Add("", "", false, false)
	f.Add("/x/..", "a:b:c", false, true)

	f.Fuzz(func(t *testing.T, dir, disk string, windows, startup bool) {
		useDiskInject(t)

		var (
			exe = dir + "/miniccc"
			svc = "systemd"
		)

		if windows {
			exe += ".exe"
		}

		if startup {
			svc = "sysinitv"
			if windows {
				svc = "startup"
			}
		}

		received := mmtest.Use(t, nil)

		err := image.InjectMiniExe(exe, disk, svc)
		cmds := received()

		if err != nil {
			if !errors.Is(err, mm.ErrUnsafeArg) {
				t.Fatalf("image.InjectMiniExe(%q, %q, %s) error = %v, want nil or %v", exe, disk, svc, err, mm.ErrUnsafeArg)
			}

			if len(cmds) != 0 {
				t.Errorf("image.InjectMiniExe(%q, %q, %s) = %v and sent %q, want no command", exe, disk, svc, err, mmtest.Bases(cmds))
			}

			return
		}

		if len(cmds) != 1 {
			t.Fatalf("image.InjectMiniExe(%q, %q, %s) sent %q, want one command", exe, disk, svc, mmtest.Bases(cmds))
		}

		base := cmds[0].Base
		if strings.Contains(base, "$") {
			t.Errorf("disk inject %q has a \"$\", which minimega expands", base)
		}

		c, err := minicli.Compile(base)
		if err != nil {
			t.Fatalf("minicli.Compile(%q) error = %v", base, err)
		}

		wantImage := disk
		if !strings.Contains(disk, ":") {
			wantImage += ":1"
		}

		if got := c.StringArgs["image"]; got != wantImage {
			t.Errorf("disk inject %q image = %q, want %q", base, got, wantImage)
		}

		var srcs, dsts []string

		for _, pair := range c.ListArgs["files"] {
			// minimega splits each pair on ":" and takes exactly two parts
			parts := strings.Split(pair, ":")
			if len(parts) != 2 {
				t.Fatalf("disk inject %q pair %q is not one src:dst pair", base, pair)
			}

			if slices.Contains(dsts, parts[1]) {
				t.Errorf("disk inject %q has the destination %q twice", base, parts[1])
			}

			srcs, dsts = append(srcs, parts[0]), append(dsts, parts[1])
		}

		if !slices.Contains(srcs, exe) {
			t.Errorf("disk inject %q sources = %q, want %q among them", base, srcs, exe)
		}
	})
}
