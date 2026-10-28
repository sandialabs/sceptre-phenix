package cmd

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/mitchellh/mapstructure"
	"pgregory.net/rapid"

	"phenix/api/config"
	v1 "phenix/types/version/v1"
)

// imageListFlagValue writes items as a comma-separated flag value, as a user
// can type it: with spaces around the items and empty items between them.
func imageListFlagValue(t *rapid.T, label string, items []string) string {
	space := rapid.SampledFrom([]string{"", " ", "  ", "\t"})

	parts := make([]string, 0, 2*len(items))
	for i, item := range items {
		parts = append(parts, space.Draw(t, label+" before "+strconv.Itoa(i))+item+space.Draw(t, label+" after "+strconv.Itoa(i)))

		if rapid.Bool().Draw(t, label+" empty "+strconv.Itoa(i)) {
			parts = append(parts, space.Draw(t, label+" empty space "+strconv.Itoa(i)))
		}
	}

	return strings.Join(parts, ",")
}

// TestImageCmdListFlagsProperty checks that phenix image create stores the
// items of its comma-separated flags in order, before the variant's packages,
// with no space at their ends and no empty items, for values with spaces and
// empty items. A value with no items keeps the default components.
func TestImageCmdListFlagsProperty(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	// the variant's own packages follow the user's, also with --skip-default-pkgs
	if _, err := imageCmdRun("create", "baseline", "--skip-default-pkgs"); err != nil {
		t.Fatalf("phenix image create baseline error = %v", err)
	}

	baseline := imageCmdSpec(t, "baseline")
	variantPackages, defaultComponents := baseline.Packages, baseline.Components

	item := rapid.StringMatching(`[a-z0-9][a-z0-9.+=_-]{0,8}`)
	run := 0

	rapid.Check(t, func(t *rapid.T) {
		overlays := rapid.SliceOf(item).Draw(t, "overlays")
		packages := rapid.SliceOf(item).Draw(t, "packages")
		components := rapid.SliceOf(item).Draw(t, "components")
		kernel := rapid.SliceOf(item).Draw(t, "kernel")

		run++
		name := "img" + strconv.Itoa(run)

		args := []string{
			"create", name, "--skip-default-pkgs",
			"--overlays=" + imageListFlagValue(t, "overlays", overlays),
			"--packages=" + imageListFlagValue(t, "packages", packages),
			"--components=" + imageListFlagValue(t, "components", components),
			"--kernel-args=" + imageListFlagValue(t, "kernel", kernel),
		}

		if _, err := imageCmdRun(args...); err != nil {
			t.Fatalf("phenix image %q error = %v", args, err)
		}

		c, err := config.Get("image/" + name)
		if err != nil {
			t.Fatalf("config.Get(image/%s) error = %v", name, err)
		}

		var img v1.Image
		if err := mapstructure.Decode(c.Spec, &img); err != nil {
			t.Fatalf("decoding the spec of image %s: %v", name, err)
		}

		got := [][]string{img.Overlays, img.Packages, img.Components, img.Kernel}
		wantComponents := components
		if len(components) == 0 {
			wantComponents = defaultComponents
		}

		want := [][]string{overlays, append(slices.Clone(packages), variantPackages...), wantComponents, kernel}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("phenix image %q stored overlays, packages, components, kernel args mismatch (-want +got):\n%s", args, diff)
		}
	})
}
