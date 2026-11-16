package file_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/file"
)

// WriteNew adds a suffix for a name that is taken and never replaces a file; it does not make a folder.
func TestWriteNew(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for name, contents := range map[string]string{"a.png": "old", "a-1.png": "older", "b": "plain"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name, want, contents string
	}{
		{"c.png", "c.png", "c"},
		{"a.png", "a-2.png", "a2"},
		{"a.png", "a-3.png", "a3"},
		{"b", "b-1", "b1"},
	}

	for _, tc := range tests {
		got, err := file.WriteNew(dir, tc.name, []byte(tc.contents))
		if err != nil {
			t.Fatalf("WriteNew(%q) error = %v", tc.name, err)
		}

		if got != tc.want {
			t.Errorf("WriteNew(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	want := map[string]string{
		"a.png": "old", "a-1.png": "older", "a-2.png": "a2", "a-3.png": "a3", "c.png": "c", "b": "plain", "b-1": "b1",
	}

	got := make(map[string]string, len(want))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}

		got[entry.Name()] = string(data)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("files after WriteNew mismatch (-want +got):\n%s", diff)
	}

	if _, err := file.WriteNew(filepath.Join(dir, "none"), "d.png", []byte("d")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteNew in a folder that does not exist error = %v, want %v", err, os.ErrNotExist)
	}
}

// WriteNew gives up with ErrExists when the name and each suffixed name are taken.
func TestWriteNewAllTaken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var err error
	for range 101 {
		if _, err = file.WriteNew(dir, "a.png", nil); err != nil {
			break
		}
	}

	if !errors.Is(err, file.ErrExists) {
		t.Errorf("WriteNew after 100 files error = %v, want %v", err, file.ErrExists)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 100 {
		t.Errorf("WriteNew made %d files, want 100", len(entries))
	}
}
