package shell_test

import (
	"os"
	"path/filepath"
	"testing"

	"phenix/util/shell"
)

// CommandExists looks up the command in Go. It must not depend on a which
// program, which a minimal container does not have.
func TestCommandExists(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "phenix-exists"), 0o755)
	writeFile(t, filepath.Join(dir, "phenix-noexec"), 0o644)

	err := os.Mkdir(filepath.Join(dir, "phenix-dir"), 0o755)
	if err != nil {
		t.Fatalf("making folder: %v", err)
	}

	t.Setenv("PATH", dir)

	if _, err := os.Stat(filepath.Join(dir, "which")); err == nil {
		t.Fatal("PATH must not hold a which program")
	}

	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "on PATH", cmd: "phenix-exists", want: true},
		{name: "absolute path", cmd: filepath.Join(dir, "phenix-exists"), want: true},
		{name: "not executable", cmd: "phenix-noexec", want: false},
		{name: "folder", cmd: "phenix-dir", want: false},
		{name: "missing", cmd: "phenix-missing", want: false},
		{name: "missing path", cmd: filepath.Join(dir, "phenix-missing"), want: false},
		{name: "empty", cmd: "", want: false},
		{name: "which", cmd: "which", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shell.CommandExists(tt.cmd)
			if got != tt.want {
				t.Errorf("CommandExists(%q) = %t, want %t", tt.cmd, got, tt.want)
			}
		})
	}
}

// A command found only through a relative PATH entry does not exist, because
// exec refuses to run it.
func TestCommandExistsRelativePATH(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "phenix-exists"), 0o755)

	t.Chdir(dir)
	t.Setenv("PATH", ".")

	if shell.CommandExists("phenix-exists") {
		t.Error("CommandExists found a command through a relative PATH entry")
	}
}
