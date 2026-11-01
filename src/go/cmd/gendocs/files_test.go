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

func TestWritePages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cli")
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

func TestReplaceNav(t *testing.T) {
	pages := []page{{File: indexFile, Title: "Overview"}, {File: "vm.md", Title: "phenix vm"}}

	got, err := replaceNav(filesTestNav, "cli", pages)
	if err != nil {
		t.Fatalf("replaceNav() = %v, want no error", err)
	}

	want := "nav:\n  - Home: index.md\n  - Reference:\n    - CLI:\n" +
		"      " + navBegin + "\n      - Overview: cli/index.md\n      - phenix vm: cli/vm.md\n" +
		"      " + navEnd + "\n    - Schema: schema.md\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("replaceNav() mismatch (-want +got):\n%s", diff)
	}
}

func TestReplaceNavMarkers(t *testing.T) {
	for _, tc := range []struct{ name, config string }{
		{"no markers", "nav:\n  - Home: index.md\n"},
		{"no end marker", navBegin + "\n"},
		{"end before begin", navEnd + "\n" + navBegin + "\n"},
		{"two begin markers", navBegin + "\n" + navBegin + "\n" + navEnd + "\n"},
		{"two end markers", navBegin + "\n" + navEnd + "\n" + navEnd + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := replaceNav(tc.config, "cli", nil); !errors.Is(err, errNavMarkers) {
				t.Errorf("replaceNav() = %v, want %v", err, errNavMarkers)
			}
		})
	}
}

func TestReplaceNavProperties(t *testing.T) {
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
