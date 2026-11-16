package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/api/image"
	v1 "phenix/types/version/v1"
)

// imageCmdRun runs phenix image with args and returns what the command
// writes to its output, and its error.
func imageCmdRun(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	imageCmd := newImageCmd()
	imageCmd.AddCommand(
		newImageListCmd(), newImageCreateCmd(), newImageCreateFromCmd(), newImageEditCmd(),
		newImageBuildCmd(), newImageDeleteCmd(), newImageAppendCmd(), newImageRemoveCmd(),
		newImageUpdateCmd(), newImageInjectMiniExeCmd(),
	)
	root.AddCommand(imageCmd)
	root.SetArgs(append([]string{"image"}, args...))

	var out bytes.Buffer

	root.SetOut(&out)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return out.String(), err
}

// imageCmdTest is a phenix image command and the error it must return: none,
// one with the text wantErr, or a humanized one that starts with humanized.
type imageCmdTest struct {
	name      string
	args      []string
	wantErr   string
	humanized string
}

// imageCmdRunAll runs each test as a subtest.
func imageCmdRunAll(t *testing.T, tests []imageCmdTest) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := imageCmdRun(test.args...)
			if msg := expCmdCheckErr(err, test.wantErr, test.humanized); msg != "" {
				t.Errorf("phenix image %s %s", strings.Join(test.args, " "), msg)
			}
		})
	}
}

// imageCmdSpec returns the stored spec of the image named name.
func imageCmdSpec(t *testing.T, name string) v1.Image {
	t.Helper()

	c, err := config.Get("image/" + name)
	if err != nil {
		t.Fatalf("config.Get(image/%s) error = %v", name, err)
	}

	var img v1.Image
	if err := mapstructure.Decode(c.Spec, &img); err != nil {
		t.Fatalf("decoding the spec of image %s: %v", name, err)
	}

	return img
}

// imageCmdScript writes a script named name with body to dir and returns its
// path.
func imageCmdScript(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing script %s: %v", path, err)
	}

	return path
}

// imageSummary is the part of an image spec that the image commands change.
type imageSummary struct {
	Size, Variant, Release string
	Overlays, Components   []string
	Kernel                 []string
	Compress, Ramdisk      bool
	Has, HasNot            []string
	Scripts                map[string]string
}

// summarize returns the summary of img, and reports which of the packages
// in has and hasNot img has.
func summarize(img v1.Image, packages []string, scripts ...string) imageSummary {
	s := imageSummary{
		Size: img.Size, Variant: img.Variant, Release: img.Release,
		Overlays: img.Overlays, Components: img.Components, Kernel: img.Kernel,
		Compress: img.Compress, Ramdisk: img.Ramdisk, Scripts: map[string]string{},
	}

	for _, p := range packages {
		if strings.Contains(strings.Join(img.Packages, ","), p) {
			s.Has = append(s.Has, p)
		} else {
			s.HasNot = append(s.HasNot, p)
		}
	}

	for _, name := range scripts {
		if script, ok := img.Scripts[name]; ok {
			s.Scripts[name] = script
		}
	}

	return s
}

// phenix image create stores a size that is a positive number followed by M
// or G. It refuses all other sizes with an error that gives the correct form,
// and stores no image.
func TestImageCmdCreateSize(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	for i, size := range []string{"10G", "500M", "1.5G", "0.5G", "2048M", "010G"} {
		name := fmt.Sprintf("good%d", i)

		t.Run(size, func(t *testing.T) {
			if _, err := imageCmdRun("create", name, "--size", size); err != nil {
				t.Fatalf("phenix image create %s --size %q error = %v, want nil", name, size, err)
			}

			if got := imageCmdSpec(t, name).Size; got != size {
				t.Errorf("image %s size = %q, want %q", name, got, size)
			}
		})
	}

	for _, size := range []string{
		"M", "G", "", "-5G", "+5G", "1.5.2G", "10X", "10", "10g", "10m", "10T", "10GB", "0G", "0.0M",
		".5G", "5.G", " 10G", "10G ", "1 0G", "1e3G", "1,5G", "0x10G", "\u0661\u0660G",
	} {
		t.Run("refuse "+size, func(t *testing.T) {
			_, err := imageCmdRun("create", "bad", "--size", size)
			if !errors.Is(err, image.ErrInvalidSize) {
				t.Fatalf("phenix image create bad --size %q error = %v, want %v", size, err, image.ErrInvalidSize)
			}

			want := fmt.Sprintf("invalid --size %q: give a number followed by M or G, such as 10G", size)
			if got := err.Error(); got != want {
				t.Errorf("phenix image create bad --size %q error = %q, want %q", size, got, want)
			}
		})
	}

	if _, err := config.Get("image/bad"); err == nil {
		t.Error("config.Get(image/bad) error = nil after refused creates, want an error")
	}
}

// phenix image create, create-from, append, remove and update store and
// change image configurations, and refuse missing or wrong arguments.
func TestImageCmdConfigs(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	dir := t.TempDir()
	s1 := imageCmdScript(t, dir, "s1.sh", "echo one\n")
	s2 := imageCmdScript(t, dir, "s2.sh", "echo two\n")
	s3 := imageCmdScript(t, dir, "s3.sh", "echo three\n")

	imageCmdRunAll(t, []imageCmdTest{
		{name: "create", args: []string{
			"create", "base", "--size", "2G", "--variant", "mingui", "--release", "bookworm", "--compress", "--ramdisk",
			"--overlays", "o1,o2", "--packages", "pkg1", "--components", "main", "--scripts", s1, "--kernel-args", "a=1,b=2",
			"--no-virtuals", "--skip-default-pkgs",
		}},
		{name: "create again", args: []string{"create", "base"}, humanized: "Unable to create the base image"},
		{
			name:      "create a bad variant",
			args:      []string{"create", "other", "--variant", "bogus"},
			humanized: "Unable to create the other image",
		},
		{
			name:    "create a bad unit",
			args:    []string{"create", "other", "--size", "10X"},
			wantErr: `invalid --size "10X": give a number followed by M or G, such as 10G`,
		},
		{name: "create with no name", args: []string{"create"}, wantErr: "must provide an image name"},
		{
			name:    "create with two names",
			args:    []string{"create", "a", "b"},
			wantErr: "must provide an image name as the only argument (check that you are using commas where required for flags)",
		},
		{name: "create-from", args: []string{"create-from", "base", "copy", "--overlays", "o3", "--packages", "pkg2", "--scripts", s2}},
		{
			name:      "create-from a missing image",
			args:      []string{"create-from", "missing", "x"},
			humanized: "Unable to create the configuration file x",
		},
		{
			name:    "create-from with one name",
			args:    []string{"create-from", "base"},
			wantErr: "the name of a existing and/or new configuration is required",
		},
		{name: "append", args: []string{"append", "base", "--overlays", "o4", "--packages", "pkg3", "--scripts", s3}},
		{name: "append to a missing image", args: []string{"append", "missing"}, humanized: "Unable to append to the missing image"},
		{name: "append with no name", args: []string{"append"}, wantErr: "the name of a configuration to append to is required"},
		{name: "remove", args: []string{"remove", "base", "--overlays", "o1", "--packages", "pkg1", "--scripts", s1}},
		{name: "remove from a missing image", args: []string{"remove", "missing"}, humanized: "Unable to remove from the missing image"},
		{name: "remove with no name", args: []string{"remove"}, wantErr: "the name of a configuration to remove from is required"},
	})

	base := imageCmdSpec(t, "base")
	wantBase := imageSummary{
		Size: "2G", Variant: "mingui", Release: "bookworm",
		Overlays: []string{"o2", "o4"}, Components: []string{"main"}, Kernel: []string{"a=1", "b=2"},
		Compress: true, Ramdisk: true, Has: []string{"pkg3"}, HasNot: []string{"pkg1"},
		Scripts: map[string]string{s3: "echo three\n"},
	}

	if diff := cmp.Diff(wantBase, summarize(base, []string{"pkg1", "pkg3"}, s1, s3)); diff != "" {
		t.Errorf("image base mismatch (-want +got):\n%s", diff)
	}

	if !base.NoVirtuals || !base.SkipDefaultPackages {
		t.Errorf("image base NoVirtuals, SkipDefaultPackages = %t, %t, want true, true", base.NoVirtuals, base.SkipDefaultPackages)
	}

	wantCopy := imageSummary{
		Size: "2G", Variant: "mingui", Release: "bookworm",
		Overlays: []string{"o1", "o2", "o3"}, Components: []string{"main"}, Kernel: []string{"a=1", "b=2"},
		Compress: true, Ramdisk: true, Has: []string{"pkg1", "pkg2"},
		Scripts: map[string]string{s1: "echo one\n", s2: "echo two\n"},
	}

	if diff := cmp.Diff(wantCopy, summarize(imageCmdSpec(t, "copy"), []string{"pkg1", "pkg2"}, s1, s2)); diff != "" {
		t.Errorf("image copy mismatch (-want +got):\n%s", diff)
	}

	// update rereads each script from its file
	imageCmdScript(t, dir, "s3.sh", "echo changed\n")

	imageCmdRunAll(t, []imageCmdTest{
		{name: "update", args: []string{"update", "base"}},
		{name: "update a missing image", args: []string{"update", "missing"}, humanized: "Unable to update scripts from the missing image"},
		{name: "update with no name", args: []string{"update"}, wantErr: "the name of a configuration to update is required"},
	})

	if got := imageCmdSpec(t, "base").Scripts[s3]; got != "echo changed\n" {
		t.Errorf("script %s after phenix image update = %q, want %q", s3, got, "echo changed\n")
	}
}

// phenix image list prints a table of the image configurations, with the
// optional columns its flags ask for, or a note when there are none.
func TestImageCmdList(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	out, err := imageCmdRun("list")
	if err != nil || !strings.Contains(out, "There are no image configurations available") {
		t.Errorf("phenix image list of no images = %q, %v, want a note that there are none", out, err)
	}

	for _, name := range []string{"one", "two"} {
		if _, err := imageCmdRun("create", name, "--format", "raw", "--mirror", "http://mirror.test/ubuntu"); err != nil {
			t.Fatalf("phenix image create %s error = %v", name, err)
		}
	}

	out, err = imageCmdRun("list", "--format", "--compressed", "--mirror")
	if err != nil {
		t.Fatalf("phenix image list error = %v", err)
	}

	for _, cell := range []string{"one", "two", "FORMAT", "COMPRESSED", "MIRROR", "raw", "http://mirror.test/ubuntu"} {
		if !vmCmdTableHas(out, cell) {
			t.Errorf("phenix image list output has no cell %q:\n%s", cell, out)
		}
	}

	vlanCmdUseFailingStore(t)

	imageCmdRunAll(t, []imageCmdTest{
		{name: "store error", args: []string{"list"}, humanized: "Unable to print a list of configurations"},
	})
}

// phenix image edit saves the edited configuration, and delete removes one.
func TestImageCmdEditDelete(t *testing.T) { //nolint:paralleltest // replaces the phenix store and sets EDITOR
	vmCmdUseStore(t)

	if _, err := imageCmdRun("create", "img"); err != nil {
		t.Fatalf("phenix image create img error = %v", err)
	}

	editor := filepath.Join(t.TempDir(), "editor")
	script := "#!/bin/sh\n[ -n \"$PHENIX_TEST_NO_EDIT\" ] && exit 0\nsed 's/size: 10G/size: 20G/' \"$1\" > \"$1.new\" && mv \"$1.new\" \"$1\"\n"

	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the test editor: %v", err)
	}

	t.Setenv("EDITOR", editor)
	t.Setenv("PHENIX_TEST_NO_EDIT", "")

	imageCmdRunAll(t, []imageCmdTest{
		{name: "edit", args: []string{"edit", "img"}},
		{name: "edit a missing image", args: []string{"edit", "missing"}, humanized: "Unable to edit the missing image"},
	})

	if got := imageCmdSpec(t, "img").Size; got != "20G" {
		t.Errorf("image img size after phenix image edit = %q, want 20G", got)
	}

	t.Setenv("PHENIX_TEST_NO_EDIT", "1")

	imageCmdRunAll(t, []imageCmdTest{
		{name: "edit with no change", args: []string{"edit", "img"}},
		{name: "delete", args: []string{"delete", "img"}},
		{name: "delete a missing image", args: []string{"delete", "img"}, humanized: "Unable to delete the img image"},
		{name: "delete an empty name", args: []string{"delete", ""}, wantErr: "the name of the configuration to delete is required"},
		{name: "delete with no name", args: []string{"delete"}, wantErr: "requires at least 1 arg(s), only received 0"},
	})
}

// phenix image delete deletes each image it names, once, continues past one
// it cannot delete, and then names each failure.
func TestImageCmdDeleteMany(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	for _, name := range []string{"a", "b", "c"} {
		if _, err := imageCmdRun("create", name); err != nil {
			t.Fatalf("phenix image create %s error = %v", name, err)
		}
	}

	if _, err := imageCmdRun("delete", "a", "b", "a"); err != nil {
		t.Fatalf("phenix image delete a b a error = %v, want nil", err)
	}

	_, err := imageCmdRun("delete", "missing", "c", "gone")
	want := "unable to delete 2 images:\n  Unable to delete the missing image"
	if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "Unable to delete the gone image") {
		t.Errorf("phenix image delete missing c gone error = %v, want it to start with %q and name gone", err, want)
	}

	for _, name := range []string{"a", "b", "c"} {
		if _, err := config.Get("image/" + name); err == nil {
			t.Errorf("image %s is still stored after phenix image delete", name)
		}
	}

	if _, err := imageCmdRun("delete", "x", ""); err == nil || err.Error() != "the name of the configuration to delete is required" {
		t.Errorf("phenix image delete x \"\" error = %v, want the empty name refused before any delete", err)
	}
}

// phenix image build writes the vmdb2 file of an image to the output folder,
// or the working folder, and fails when it cannot run vmdb2.
func TestImageCmdBuild(t *testing.T) { //nolint:paralleltest // replaces the phenix store, the working folder, PATH and os.Stdout
	vmCmdUseStore(t)

	if _, err := imageCmdRun("create", "img"); err != nil {
		t.Fatalf("phenix image create img error = %v", err)
	}

	output := t.TempDir()
	cwd := t.TempDir()

	t.Chdir(cwd)
	t.Setenv("PATH", t.TempDir())

	var err error

	stdout := captureStdout(t, func() {
		_, err = imageCmdRun("build", "img", "--dry-run", "--output", output, "--verbose", "--very-verbose", "--cache")
	})
	if err != nil {
		t.Fatalf("phenix image build --dry-run error = %v", err)
	}

	if !strings.Contains(stdout, "DRY RUN: vmdb2 "+output+"/img.vmdb") {
		t.Errorf("phenix image build --dry-run output = %q, want the vmdb2 command", stdout)
	}

	imageCmdRunAll(t, []imageCmdTest{
		{name: "no vmdb2", args: []string{"build", "img"}, humanized: "Unable to build the img image"},
		{name: "missing image", args: []string{"build", "missing", "--output", output}, humanized: "Unable to build the missing image"},
		{name: "no name", args: []string{"build"}, wantErr: "the name of a configuration to build the disk image is required"},
	})

	for _, path := range []string{filepath.Join(output, "img.vmdb"), filepath.Join(cwd, "img.vmdb")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("phenix image build did not write %s: %v", path, err)
		}
	}
}

// phenix image inject-miniexe refuses missing arguments and paths that
// minimega cannot take.
func TestImageCmdInjectMiniExe(t *testing.T) { //nolint:paralleltest // writes the plog records
	imageCmdRunAll(t, []imageCmdTest{
		{
			name:      "unsafe path",
			args:      []string{"inject-miniexe", "a\"b", "disk.qc2"},
			humanized: "Unable to inject a\"b into the disk.qc2 image",
		},
		{
			name:    "one path",
			args:    []string{"inject-miniexe", "miniccc"},
			wantErr: "the path to the minimega executable and the disk is required",
		},
	})
}
