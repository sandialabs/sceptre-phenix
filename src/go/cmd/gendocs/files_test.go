package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

const filesTestNav = "nav:\n  - Home: index.md\n  - Reference:\n    - CLI:\n" +
	"      " + navBegin + "\n      - Old: cli/old.md\n      " + navEnd + "\n    - Schema: schema.md\n"

// writePages writes each page, removes the other Markdown files in the folder
// and keeps the other files, also in a folder whose name is a glob pattern.
func TestWritePages(t *testing.T) {
	t.Parallel()

	for _, folder := range []string{"cli", "cli[*?"} {
		t.Run(folder, func(t *testing.T) {
			t.Parallel()

			testWritePages(t, filepath.Join(t.TempDir(), folder))
		})
	}
}

func testWritePages(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"gone.md": "stale", "keep.md": "old", "notes.txt": "not a page"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := writePages(dir, []page{{File: "keep.md", Body: "new\n"}, {File: "added.md", Body: "added\n"}}); err != nil {
		t.Fatalf("writePages() = %v, want no error", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]string)

	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}

		got[e.Name()] = string(data)
	}

	want := map[string]string{"keep.md": "new\n", "added.md": "added\n", "notes.txt": "not a page"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("writePages() directory mismatch (-want +got):\n%s", diff)
	}
}

// replaceNav lists the pages between the markers, indented like the begin
// marker, and refuses a config without exactly one of each in order.
func TestReplaceNav(t *testing.T) {
	t.Parallel()

	pages := []page{{File: indexFile, Title: "Overview"}, {File: "vm.md", Title: "phenix vm"}}

	for _, tc := range []struct {
		name, config string
		pages        []page
		want         string
		wantErr      error
	}{
		{
			name: "replaces the entries", config: filesTestNav, pages: pages,
			want: "nav:\n  - Home: index.md\n  - Reference:\n    - CLI:\n" +
				"      " + navBegin + "\n      - Overview: cli/index.md\n      - phenix vm: cli/vm.md\n" +
				"      " + navEnd + "\n    - Schema: schema.md\n",
		},
		{
			name: "no pages", config: filesTestNav,
			want: "nav:\n  - Home: index.md\n  - Reference:\n    - CLI:\n" +
				"      " + navBegin + "\n      " + navEnd + "\n    - Schema: schema.md\n",
		},
		{
			name: "markers without indentation or a last newline", config: navBegin + "\n" + navEnd, pages: pages[:1],
			want: navBegin + "\n- Overview: cli/index.md\n" + navEnd,
		},
		{name: "no markers", config: "nav:\n  - Home: index.md\n", wantErr: errNavMarkers},
		{name: "no end marker", config: navBegin + "\n", wantErr: errNavMarkers},
		{name: "end before begin", config: navEnd + "\n" + navBegin + "\n", wantErr: errNavMarkers},
		{name: "two begin markers", config: navBegin + "\n" + navBegin + "\n" + navEnd + "\n", wantErr: errNavMarkers},
		{name: "two end markers", config: navBegin + "\n" + navEnd + "\n" + navEnd + "\n", wantErr: errNavMarkers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := replaceNav(tc.config, "cli", tc.pages)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("replaceNav() error = %v, want %v", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("replaceNav() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReplaceNavProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SliceOfDistinct(rapid.StringMatching(`[a-z]{1,8}`), rapid.ID).Draw(t, "names")

		pages := make([]page, 0, len(names))
		for _, name := range names {
			pages = append(pages, page{File: name + ".md", Title: "phenix " + name})
		}

		once, err := replaceNav(filesTestNav, "cli", pages)
		if err != nil {
			t.Fatalf("replaceNav() = %v, want no error", err)
		}

		twice, err := replaceNav(once, "cli", pages)
		if err != nil || twice != once {
			t.Fatalf("replaceNav() of its own output = %q, %v, want %q", twice, err, once)
		}

		lines := strings.Split(once, "\n")
		begin := slices.Index(lines, "      "+navBegin)

		if end := slices.Index(lines, "      "+navEnd); end-begin-1 != len(pages) {
			t.Fatalf("replaceNav() wrote %d entries between the markers, want %d:\n%s", end-begin-1, len(pages), once)
		}

		if !strings.HasPrefix(once, "nav:\n  - Home: index.md\n") || !strings.HasSuffix(once, "    - Schema: schema.md\n") {
			t.Fatalf("replaceNav() changed lines outside the markers:\n%s", once)
		}
	})
}

// writePages reports a folder it cannot create, and a page it cannot write or
// remove.
func TestWritePagesErrors(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		// the folder to write to, under a temp dir that holds the file "file"
		dir string
		// a folder, not empty, to make at this name in dir
		subdir string
		pages  []page
	}{
		"folder under a file":    {dir: "file/cli"},
		"page is a folder":       {dir: "cli", subdir: "keep.md", pages: []page{{File: "keep.md", Body: "x"}}},
		"stale page is a folder": {dir: "cli", subdir: "gone.md"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			if err := os.WriteFile(filepath.Join(base, "file"), nil, 0o644); err != nil {
				t.Fatal(err)
			}

			dir := filepath.Join(base, tc.dir)

			if tc.subdir != "" {
				if err := os.MkdirAll(filepath.Join(dir, tc.subdir, "x"), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := writePages(dir, tc.pages); err == nil {
				t.Errorf("writePages(%s) = nil, want an error", name)
			}
		})
	}
}

// updateNav writes the mkdocs config only when the nav changes, and reports
// a config it cannot read, parse or write.
func TestUpdateNav(t *testing.T) {
	t.Parallel()

	pages := []page{{File: "vm.md", Title: "phenix vm"}}

	current, err := replaceNav(filesTestNav, "cli", pages)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		config string
		// no config file; or one that cannot be written
		missing, readOnly bool
		want              string
		wantErr           bool
	}{
		"changed":              {config: filesTestNav, want: current},
		"unchanged, read-only": {config: current, readOnly: true, want: current},
		"changed, read-only":   {config: filesTestNav, readOnly: true, want: filesTestNav, wantErr: true},
		"missing":              {missing: true, wantErr: true},
		"no markers":           {config: "nav: []\n", want: "nav: []\n", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "mkdocs.yml")

			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if tc.readOnly {
				if os.Geteuid() == 0 {
					t.Skip("root writes a read-only file")
				}

				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
			}

			if err := updateNav(path, "cli", pages); (err != nil) != tc.wantErr {
				t.Errorf("updateNav(%s) = %v, want error %t", name, err, tc.wantErr)
			}

			if tc.missing {
				return
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.want, string(got)); diff != "" {
				t.Errorf("updateNav(%s) config mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}
