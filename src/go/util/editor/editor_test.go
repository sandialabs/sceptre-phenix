package editor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestModified checks that any difference in the edited bytes counts as a
// change, and that identical bytes do not.
func TestModified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		old, edited []byte
		want        bool
	}{
		{name: "same", old: []byte("a: 1\n"), edited: []byte("a: 1\n"), want: false},
		{name: "nil and empty", old: nil, edited: []byte{}, want: false},
		{name: "changed", old: []byte("a: 1\n"), edited: []byte("a: 2\n"), want: true},
		{name: "trailing newline", old: []byte("a: 1"), edited: []byte("a: 1\n"), want: true},
		{name: "emptied", old: []byte("a: 1\n"), edited: nil, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := modified(tt.old, tt.edited); got != tt.want {
				t.Errorf("modified(%q, %q) = %t, want %t", tt.old, tt.edited, got, tt.want)
			}
		})
	}
}

// editorTestScript writes the shell script body to dir as an executable
// file, and returns its path.
func editorTestScript(t *testing.T, dir, body string) string {
	t.Helper()

	path := filepath.Join(dir, "fake-editor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}

	return path
}

// EditData gives the editor a temporary file that holds the data, returns
// what the editor left in it, and removes the file. Unchanged data gives
// ErrNoChange, and a failed editor gives its error.
func TestEditorEditData(t *testing.T) { //nolint:paralleltest // sets EDITOR and TMPDIR
	tests := map[string]struct {
		data     string
		script   string // the editor's body; $1 is the file
		want     string
		wantErr  error
		wantExit int // the editor's exit code when EditData must fail; -1 for any other error
	}{
		"edited":            {data: "a: 1\n", script: `printf 'a: 2\n' > "$1"`, want: "a: 2\n"},
		"emptied":           {data: "a: 1\n", script: `: > "$1"`, want: ""},
		"from empty":        {data: "", script: `printf 'new' > "$1"`, want: "new"},
		"unchanged":         {data: "a: 1\n", script: "exit 0", want: "a: 1\n", wantErr: ErrNoChange},
		"empty, unchanged":  {data: "", script: "exit 0", want: "", wantErr: ErrNoChange},
		"editor fails":      {data: "a: 1\n", script: "exit 3", wantExit: 3},
		"editor removes it": {data: "a: 1\n", script: `rm -f "$1"`, wantExit: -1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tmp := filepath.Join(dir, "tmp")

			if err := os.Mkdir(tmp, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%s) error = %v", tmp, err)
			}

			seen := filepath.Join(dir, "seen")
			t.Setenv("TMPDIR", tmp)
			t.Setenv("EDITOR", editorTestScript(t, dir, `printf '%s' "$1" > `+seen+"\n"+tc.script+"\n"))

			got, err := EditData([]byte(tc.data))

			if tc.wantExit != 0 {
				var exitErr *exec.ExitError

				switch {
				case err == nil || errors.Is(err, ErrNoChange):
					t.Errorf("EditData(%q) error = %v, want the editor's error", tc.data, err)
				case tc.wantExit > 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantExit):
					t.Errorf("EditData(%q) error = %v, want exit status %d", tc.data, err, tc.wantExit)
				}

				if got != nil {
					t.Errorf("EditData(%q) = %q, want nil with an error", tc.data, got)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("EditData(%q) error = %v, want %v", tc.data, err, tc.wantErr)
				}

				if diff := cmp.Diff(tc.want, string(got)); diff != "" {
					t.Errorf("EditData(%q) mismatch (-want +got):\n%s", tc.data, diff)
				}
			}

			file, err := os.ReadFile(seen)
			if err != nil {
				t.Fatalf("the editor did not run: %v", err)
			}

			if filepath.Dir(string(file)) != tmp {
				t.Errorf("EditData() edited %s, want a file in %s", file, tmp)
			}

			if _, err := os.Stat(string(file)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("EditData() left %s behind (stat error %v)", file, err)
			}
		})
	}
}

// EditData fails before it starts the editor when it cannot make the
// temporary file.
func TestEditorEditDataNoTempDir(t *testing.T) { //nolint:paralleltest // sets EDITOR and TMPDIR
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")

	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	t.Setenv("EDITOR", editorTestScript(t, dir, "printf x > "+ran+"\n"))

	if got, err := EditData([]byte("a")); err == nil || got != nil {
		t.Errorf("EditData() with no temporary directory = %q, %v, want nil and an error", got, err)
	}

	if _, err := os.Stat(ran); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("EditData() started the editor with no temporary file (stat error %v)", err)
	}
}

// OpenFileInEditor runs $EDITOR, or vim when EDITOR is empty, with the path,
// and fails when the editor is not on PATH or fails.
func TestEditorOpenFileInEditor(t *testing.T) { //nolint:paralleltest // sets EDITOR and PATH
	tests := map[string]struct {
		editor  string // "record" or "fail" for a fake editor at an absolute path
		onPath  string // the name of a fake editor on PATH, or ""
		wantErr string
	}{
		"editor":                     {editor: "record"},
		"editor on PATH":             {editor: "my-editor", onPath: "my-editor"},
		"default editor on PATH":     {onPath: DefaultEditor},
		"editor fails":               {editor: "fail", wantErr: "exit status 1"},
		"editor not on PATH":         {editor: "no-such-editor", wantErr: `"no-such-editor": executable file not found`},
		"default editor not on PATH": {wantErr: `"` + DefaultEditor + `": executable file not found`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			arg := filepath.Join(dir, "arg")
			record := `printf '%s' "$1" > ` + arg + "\n"

			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatalf("os.Mkdir(%s) error = %v", bin, err)
			}

			editor := tc.editor

			switch editor {
			case "record":
				editor = editorTestScript(t, dir, record)
			case "fail":
				editor = editorTestScript(t, dir, "exit 1\n")
			}

			if tc.onPath != "" {
				if err := os.Rename(editorTestScript(t, dir, record), filepath.Join(bin, tc.onPath)); err != nil {
					t.Fatalf("os.Rename() error = %v", err)
				}
			}

			t.Setenv("PATH", bin)
			t.Setenv("EDITOR", editor)

			err := OpenFileInEditor("file.yaml")

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("OpenFileInEditor() error = %v, want one containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("OpenFileInEditor() error = %v", err)
			}

			if got, err := os.ReadFile(arg); err != nil || string(got) != "file.yaml" {
				t.Errorf("the editor got the argument %q (%v), want %q", got, err, "file.yaml")
			}
		})
	}
}
