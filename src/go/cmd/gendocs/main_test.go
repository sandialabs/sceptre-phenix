package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mainArgsEnv holds, tab-separated, the arguments TestMainExitStatus runs
// main with in a child test process.
const mainArgsEnv = "PHENIX_GENDOCS_MAIN_ARGS"

// run fails without both paths, and reports a folder or mkdocs config it
// cannot write or read.
//
//nolint:paralleltest // run renders the shared phenix command tree
func TestRunErrors(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct{ out, mkdocs string }{
		"no paths":         {},
		"no mkdocs":        {out: "out"},
		"no out":           {mkdocs: "mkdocs.yml"},
		"out under a file": {out: filepath.Join(file, "cli"), mkdocs: filepath.Join(dir, "mkdocs.yml")},
		"missing mkdocs":   {out: filepath.Join(dir, "cli"), mkdocs: filepath.Join(dir, "missing.yml")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(tc.out, tc.mkdocs); err == nil {
				t.Errorf("run(%q, %q) = nil, want an error", tc.out, tc.mkdocs)
			}
		})
	}
}

//nolint:paralleltest // run renders the shared phenix command tree
func TestRun(t *testing.T) {
	dir := t.TempDir()
	mkdocs := filepath.Join(dir, "mkdocs.yml")

	if err := os.WriteFile(mkdocs, []byte(filesTestNav), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(filepath.Join(dir, "cli"), mkdocs); err != nil {
		t.Fatalf("run() = %v, want no error", err)
	}

	config, err := os.ReadFile(mkdocs)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{indexFile, "experiment.md", "vm.md"} {
		if _, err := os.Stat(filepath.Join(dir, "cli", file)); err != nil {
			t.Errorf("run() did not write %s: %v", file, err)
		}

		if !strings.Contains(string(config), ": cli/"+file+"\n") {
			t.Errorf("run() did not list cli/%s in the nav:\n%s", file, config)
		}
	}
}

// main writes the reference its -out and -mkdocs flags name, and exits 1
// with the error on stderr without them. It runs in a child test process.
func TestMainExitStatus(t *testing.T) {
	t.Parallel()

	if args, ok := os.LookupEnv(mainArgsEnv); ok {
		os.Args = append([]string{"gendocs"}, strings.Split(args, "\t")...) //nolint:reassign // the child runs main with them

		main()

		return
	}

	for name, tc := range map[string]struct {
		mkdocs bool
		failed bool
	}{
		"writes the reference": {mkdocs: true},
		"no -mkdocs":           {failed: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			mkdocs := filepath.Join(dir, "mkdocs.yml")

			if err := os.WriteFile(mkdocs, []byte(filesTestNav), 0o644); err != nil {
				t.Fatal(err)
			}

			args := "-out\t" + filepath.Join(dir, "cli")
			if tc.mkdocs {
				args += "\t-mkdocs\t" + mkdocs
			}

			var stderr bytes.Buffer

			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainExitStatus$")
			child.Env = append(os.Environ(), mainArgsEnv+"="+args)
			child.Stderr = &stderr

			err := child.Run()

			var exit *exec.ExitError
			if failed := errors.As(err, &exit) && exit.ExitCode() == 1; failed != tc.failed || (!tc.failed && err != nil) {
				t.Fatalf("gendocs %q = %v, want exit status 1 %t; stderr:\n%s", args, err, tc.failed, &stderr)
			}

			if tc.failed && !strings.HasPrefix(stderr.String(), "gendocs: ") {
				t.Errorf("gendocs %q stderr = %q, want it to start with %q", args, &stderr, "gendocs: ")
			}

			if _, err := os.Stat(filepath.Join(dir, "cli", indexFile)); (err == nil) != tc.mkdocs {
				t.Errorf("gendocs %q wrote %s = %t, want %t", args, indexFile, err == nil, tc.mkdocs)
			}
		})
	}
}
