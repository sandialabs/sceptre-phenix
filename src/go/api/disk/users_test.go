package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// DeleteDisk keeps an image that another image is backed by, or that an experiment uses directly or through a
// backing chain, and refuses with an *InUseError that names them. It deletes an image nothing uses, and, with force,
// an image in use.
func TestDeleteDiskRefusesAnImageInUse(t *testing.T) {
	useFakeQemuImg(t)

	dir := useFilesDir(t)
	outside := t.TempDir()

	var (
		base    = dir + "/base.qc2"
		child   = dir + "/win/child.qc2"
		used    = dir + "/used.qc2"
		lone    = dir + "/lone.qc2"
		overlay = outside + "/o.qc2"
	)

	writeImages(t, base, child, used, lone, overlay)
	writeFile(t, child+".backing", "../base.qc2")
	writeFile(t, overlay+".backing", base)

	storeExperiments(t,
		testExperiment(t, "exp-run", "2026-09-30T00:00:00Z", overlay),
		testExperiment(t, "exp-stop", "", "used.qc2"),
	)

	if err := DeleteDisk(lone, false); err != nil {
		t.Errorf("DeleteDisk(lone.qc2, false) error = %v, want nil", err)
	}

	for name, tc := range map[string]struct {
		path        string
		images      []string
		experiments []ExperimentUse
	}{
		"a backing image": {
			path:        base,
			images:      []string{child},
			experiments: []ExperimentUse{{Name: "exp-run", Running: true}},
		},
		"an image an experiment uses": {
			path:        used,
			images:      []string{},
			experiments: []ExperimentUse{{Name: "exp-stop", Running: false}},
		},
	} {
		err := DeleteDisk(tc.path, false)

		var inUse *InUseError
		if !errors.As(err, &inUse) || !errors.Is(err, ErrInUse) {
			t.Fatalf("DeleteDisk(%s, false) error = %v, want an *InUseError", name, err)
		}

		images := make([]string, 0, len(inUse.Images))
		for _, image := range inUse.Images {
			images = append(images, image.FullPath)
		}

		// the overlay outside the files directory is backed by base.qc2 too
		if tc.path == base {
			tc.images = append(tc.images, overlay)
		}

		if diff := cmp.Diff(tc.images, images); diff != "" {
			t.Errorf("DeleteDisk(%s, false) images mismatch (-want +got):\n%s", name, diff)
		}

		if diff := cmp.Diff(tc.experiments, inUse.Experiments); diff != "" {
			t.Errorf("DeleteDisk(%s, false) experiments mismatch (-want +got):\n%s", name, diff)
		}

		if _, err := os.Stat(tc.path); err != nil {
			t.Errorf("os.Stat(%s) after a refused delete error = %v, want the file kept", name, err)
		}
	}

	if err := DeleteDisk(base, true); err != nil {
		t.Errorf("DeleteDisk(base.qc2, true) error = %v, want nil", err)
	}

	for _, path := range []string{lone, base} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("os.Stat(%s) after DeleteDisk error = %v, want %v", filepath.Base(path), err, fs.ErrNotExist)
		}
	}
}

// UsersText names images and experiments, marks a running experiment, and counts what it does not name.
func TestUsersText(t *testing.T) {
	t.Parallel()

	many := make([]string, MaxNamedUsers+2)
	for i := range many {
		many[i] = fmt.Sprintf("/i/%d.qc2", i)
	}

	for name, tc := range map[string]struct {
		images      []string
		experiments []ExperimentUse
		hidden      int
		want        string
	}{
		"one image": {images: []string{"/i/a.qc2"}, want: "image /i/a.qc2"},
		"images and experiments": {
			images:      []string{"/i/a.qc2"},
			experiments: []ExperimentUse{{Name: "e1", Running: true}, {Name: "e2"}},
			want:        "image /i/a.qc2, experiment e1 (running), experiment e2",
		},
		"hidden ones":  {images: []string{"/i/a.qc2"}, hidden: 2, want: "image /i/a.qc2 and 2 more"},
		"only hidden":  {hidden: 1, want: "1 other"},
		"many hidden":  {hidden: 3, want: "3 others"},
		"over the cap": {images: many, hidden: 1, want: strings.Join(prefixed("image ", many[:MaxNamedUsers]), ", ") + " and 3 more"},
	} {
		if got := UsersText(tc.images, tc.experiments, tc.hidden); got != tc.want {
			t.Errorf("UsersText(%s) = %q, want %q", name, got, tc.want)
		}
	}
}

// UsersText names the first MaxNamedUsers images and experiments in order, and the count it gives is of all the
// others, hidden ones included.
func TestUsersTextProperty(t *testing.T) {
	t.Parallel()

	more := regexp.MustCompile(` and (\d+) more$`)

	rapid.Check(t, func(rt *rapid.T) {
		images := rapid.SliceOfN(rapid.StringMatching(`/[a-z]{1,8}\.qc2`), 0, 15).Draw(rt, "images")
		experiments := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) ExperimentUse {
			return ExperimentUse{Name: rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, "name"), Running: rapid.Bool().Draw(rt, "running")}
		}), 0, 15).Draw(rt, "experiments")
		hidden := rapid.IntRange(0, 20).Draw(rt, "hidden")

		got := UsersText(images, experiments, hidden)

		total := len(images) + len(experiments)
		named := min(total, MaxNamedUsers)

		if total == 0 {
			return
		}

		counted := 0
		if m := more.FindStringSubmatch(got); m != nil {
			counted, _ = strconv.Atoi(m[1])
			got = strings.TrimSuffix(got, m[0])
		}

		if want := total - named + hidden; counted != want {
			rt.Fatalf("UsersText(%d images, %d experiments, %d hidden) counts %d more, want %d",
				len(images), len(experiments), hidden, counted, want)
		}

		names := strings.Split(got, ", ")
		if len(names) != named {
			rt.Fatalf("UsersText(%d images, %d experiments) names %d, want %d: %q", len(images), len(experiments), len(names), named, got)
		}

		for i, name := range names {
			want := "image "
			if i >= len(images) {
				want = "experiment " + experiments[i-len(images)].Name
			} else {
				want += images[i]
			}

			if !strings.HasPrefix(name, want) {
				rt.Fatalf("UsersText name %d = %q, want it to start with %q", i, name, want)
			}
		}
	})
}

// prefixed returns each name with prefix before it.
func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = prefix + name
	}

	return out
}
