package soh

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/file"
)

// The script name of a custom test is one path segment of safe characters, so
// its path never leaves the experiment's folder, and it reaches the VM
// unchanged through cc exec.
func FuzzCustomScriptName(f *testing.F) {
	for _, seed := range [][2]string{
		{"web", "check"}, {"web", "../../etc/x"}, {"..", ".."}, {".", "."}, {"", ""},
		{"web", `'q"#$y`}, {"a/b", `c\d`}, {"web", "a\x00b\xff"}, {"web", "$HOME"},
	} {
		f.Add(seed[0], seed[1])
	}

	const dir = "/srv/minimega/files/exp"

	f.Fuzz(func(t *testing.T, host, name string) {
		script := customScriptName(host, name)

		if script == "." || script == ".." || strings.Trim(script, alnumChars+"-_.") != "" {
			t.Fatalf("customScriptName(%q, %q) = %q, want one segment of safe characters", host, name, script)
		}

		path := filepath.Join(dir, script)
		if filepath.Dir(path) != dir || !file.WithinDir(dir, path) {
			t.Fatalf("customScriptName(%q, %q) = %q, whose path %s leaves %s", host, name, script, path, dir)
		}

		cmd, err := ccExecCommand("bash", "/tmp/miniccc/files/exp/"+script)
		if err != nil {
			t.Fatalf("ccExecCommand(script %q) error = %v", script, err)
		}

		got, err := ccExecArgv(cmd)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"bash", "/tmp/miniccc/files/exp/" + script}, got); diff != "" {
			t.Fatalf("ccExecCommand(script %q) argv mismatch (-want +got):\n%s", script, diff)
		}
	})
}

// Two different test names on one host give two different script names.
func FuzzCustomScriptNameIsUnique(f *testing.F) {
	for _, seed := range [][3]string{
		{"web", "db/check", "db check"}, {"web", "db check", "db_check"}, {"web", "a  b", "a b"},
		{"web", "a\tb", "a_b"}, {"web", "", "-"}, {"web", "x", "x-0"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}

	f.Fuzz(func(t *testing.T, host, a, b string) {
		if a == b {
			return
		}

		if got := customScriptName(host, a); got == customScriptName(host, b) {
			t.Fatalf("customScriptName(%q, %q) = customScriptName(%q, %q) = %q, want different names", host, a, host, b, got)
		}
	})
}
