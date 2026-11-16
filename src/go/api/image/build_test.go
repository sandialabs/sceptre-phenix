package image_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/image"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
)

// captureStdout runs f and returns what it wrote to [os.Stdout].
func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	stdout := os.Stdout
	os.Stdout = w //nolint:reassign // capture the CLI output

	t.Cleanup(func() { os.Stdout = stdout }) //nolint:reassign // restore stdout

	done := make(chan string)

	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()

	f()

	os.Stdout = stdout //nolint:reassign // restore stdout
	_ = w.Close()

	return <-done
}

// Build in dry-run mode writes the vmdb2 config from the stored image and
// prints the vmdb2 command, with -v and --log as the verbosity asks.
//
//nolint:paralleltest // replaces the phenix store and stdout
func TestBuildDryRun(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "web", v1.Image{
		Name: "web", Release: "noble", Mirror: "http://mirror", Size: "5G", Format: v1.FormatQcow2,
		Packages: []string{"git"}, Scripts: map[string]string{"S": "echo hi"}, ScriptOrder: []string{"S"},
	}, nil, "")

	tests := []struct {
		verbosity int
		cache     bool
		wantArgs  string
	}{
		{verbosity: 0, wantArgs: ""},
		{verbosity: image.VVerbose, cache: true, wantArgs: " -v"},
		{verbosity: image.VVVerbose, wantArgs: " -v --log OUT/web.log"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("verbosity %d", tt.verbosity), func(t *testing.T) {
			out := t.TempDir()

			var err error

			got := captureStdout(t, func() { err = image.Build(context.Background(), "web", tt.verbosity, tt.cache, true, out) })
			if err != nil {
				t.Fatalf("image.Build(web, dry run) error = %v", err)
			}

			want := "DRY RUN: vmdb2 OUT/web.vmdb --output OUT/web --rootfs-tarball OUT/web.tar" + tt.wantArgs + "\n"
			if want = strings.ReplaceAll(want, "OUT", out); got != want {
				t.Errorf("image.Build(web, dry run) printed %q, want %q", got, want)
			}

			vmdb, err := os.ReadFile(out + "/web.vmdb")
			if err != nil {
				t.Fatalf("reading the vmdb2 config: %v", err)
			}

			for _, line := range []string{"# web build", "  - debootstrap: noble", "    mirror: http://mirror",
				"    size: 5G", "    format: qcow2", "      - git", "      echo hi"} {
				if !slices.Contains(strings.Split(string(vmdb), "\n"), line) {
					t.Errorf("vmdb2 config has no line %q:\n%s", line, vmdb)
				}
			}

			if got := strings.Contains(string(vmdb), "cache-rootfs: root"); got != tt.cache {
				t.Errorf("vmdb2 config has a cache-rootfs step = %t, want %t", got, tt.cache)
			}
		})
	}
}

// Build uses kali-rolling as the debootstrap release of a kali image.
//
//nolint:paralleltest // replaces the phenix store and stdout
func TestBuildKaliRelease(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "kali", v1.Image{Name: "kali", Release: "kali"}, nil, "")

	out := t.TempDir()

	var err error

	captureStdout(t, func() { err = image.Build(context.Background(), "kali", 0, false, true, out) })
	if err != nil {
		t.Fatalf("image.Build(kali, dry run) error = %v", err)
	}

	vmdb, err := os.ReadFile(out + "/kali.vmdb")
	if err != nil {
		t.Fatalf("reading the vmdb2 config: %v", err)
	}

	if !slices.Contains(strings.Split(string(vmdb), "\n"), "  - debootstrap: kali-rolling") {
		t.Errorf("vmdb2 config has no kali-rolling debootstrap step:\n%s", vmdb)
	}
}

// Build of a .vmdb file uses the file as it is, and names the outputs after it.
//
//nolint:paralleltest // replaces the phenix store and stdout
func TestBuildVmdbFile(t *testing.T) {
	storetest.UseBolt(t)

	out := t.TempDir()

	var err error

	got := captureStdout(t, func() { err = image.Build(context.Background(), "/configs/my.img.vmdb", 0, false, true, out) })
	if err != nil {
		t.Fatalf("image.Build(/configs/my.img.vmdb, dry run) error = %v", err)
	}

	want := fmt.Sprintf("DRY RUN: vmdb2 /configs/my.img.vmdb --output %s/my.img --rootfs-tarball %s/my.img.tar\n", out, out)
	if got != want {
		t.Errorf("image.Build(/configs/my.img.vmdb, dry run) printed %q, want %q", got, want)
	}

	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("image.Build(/configs/my.img.vmdb) wrote %v to the output directory, want nothing", entries)
	}
}

// Build returns an error for an image that is not stored, and for a build
// that is not a dry run when vmdb2 is not on the PATH.
//
//nolint:paralleltest // replaces the phenix store, stdout and PATH
func TestBuildErrors(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "web", v1.Image{Name: "web", Release: "noble"}, nil, "")

	out := t.TempDir()

	if err := image.Build(context.Background(), "nope", 0, false, true, out); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("image.Build(nope) error = %v, want %v", err, store.ErrNotExist)
	}

	t.Setenv("PATH", t.TempDir())

	var err error

	got := captureStdout(t, func() { err = image.Build(context.Background(), "web", 0, false, false, out) })
	if err == nil {
		t.Errorf("image.Build(web) with no vmdb2 on the PATH error = nil, want an error")
	}

	if got != "" {
		t.Errorf("image.Build(web) with no vmdb2 on the PATH printed %q, want nothing", got)
	}
}

// pad makes the fake vmdb2 output more than a pipe buffer holds.
var pad = strings.Repeat("x", 60) //nolint:gochecknoglobals // a constant of the fake vmdb2

// fakeVmdb2 puts a vmdb2 script first on the PATH. It writes its arguments
// to the returned file, prints n lines to stdout and one to stderr, and
// exits with code.
func fakeVmdb2(t *testing.T, n, code int) string {
	t.Helper()

	dir := t.TempDir()
	args := dir + "/args"
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
i=0
while [ "$i" -lt %d ]; do echo "out $i %s"; i=$((i + 1)); done
echo "err line" >&2
exit %d
`, args, n, pad, code)

	if err := os.WriteFile(dir+"/vmdb2", []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake vmdb2: %v", err)
	}

	t.Setenv("PATH", dir+":/usr/bin:/bin")

	return args
}

// Build runs vmdb2 with the config it wrote, prints all of its stdout and
// stderr lines before it returns, and returns an error when vmdb2 fails.
//
//nolint:paralleltest // replaces the phenix store, stdout and PATH
func TestBuildRunsVmdb2(t *testing.T) {
	storetest.UseBolt(t)
	storeImage(t, "web", v1.Image{Name: "web", Release: "noble"}, nil, "")

	const lines = 3000

	args := fakeVmdb2(t, lines, 0)
	out := t.TempDir()

	var err error

	got := captureStdout(t, func() { err = image.Build(context.Background(), "web", image.VVerbose, false, false, out) })
	if err != nil {
		t.Fatalf("image.Build(web) error = %v", err)
	}

	want := make([]string, 0, lines+1)

	want = append(want, "err line")
	for i := range lines {
		want = append(want, fmt.Sprintf("out %d %s", i, pad))
	}

	printed := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	slices.Sort(printed)
	slices.Sort(want)

	if diff := cmp.Diff(want, printed); diff != "" {
		t.Errorf("image.Build(web) output mismatch (-want +got):\n%s", diff)
	}

	gotArgs, err := os.ReadFile(args)
	if err != nil {
		t.Fatalf("reading the fake vmdb2 arguments: %v", err)
	}

	wantArgs := strings.Join([]string{out + "/web.vmdb", "--output", out + "/web", "--rootfs-tarball", out + "/web.tar", "-v"}, "\n") + "\n"
	if string(gotArgs) != wantArgs {
		t.Errorf("vmdb2 arguments = %q, want %q", gotArgs, wantArgs)
	}

	fakeVmdb2(t, 0, 3)

	captureStdout(t, func() { err = image.Build(context.Background(), "web", 0, false, false, out) })
	if err == nil {
		t.Errorf("image.Build(web) with a vmdb2 that fails error = nil, want an error")
	}
}
