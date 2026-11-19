package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"phenix/cmd"
)

var (
	slugDrop = regexp.MustCompile(`[^\w\s-]`)
	slugJoin = regexp.MustCompile(`[-\s]+`)
)

// slug is the anchor mkdocs gives an ASCII heading.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(slugDrop.ReplaceAllString(heading, "")))

	return slugJoin.ReplaceAllString(s, "-")
}

func TestSeeAlso(t *testing.T) {
	t.Parallel()

	if got := seeAlso(nil); got != "" {
		t.Errorf("seeAlso(nil) = %q, want nothing", got)
	}

	got := seeAlso([]docLink{{"Disks", "disks.md", "the Disks page"}, {"API", "api.md#downloads", "downloads"}})

	want := "## See Also\n\n* [Disks](../disks.md): the Disks page.\n* [API](../api.md#downloads): downloads.\n"
	if got != want {
		t.Errorf("seeAlso() = %q, want %q", got, want)
	}
}

// A page ends with the related pages of its command, after the last command.
//
//nolint:paralleltest // renders the shared phenix command tree
func TestRenderEndsWithSeeAlso(t *testing.T) {
	body := renderPages(t, cmd.Root())["host.md"].Body

	if want := "\n\n" + seeAlso(relatedPages()["host"]); !strings.HasSuffix(body, want) {
		t.Errorf("render() host.md does not end with %q:\n%s", want, body)
	}
}

// A page has one See Also section, and the overview page has none.
//
//nolint:paralleltest // renders the shared phenix command tree
func TestRenderSeeAlsoOncePerPage(t *testing.T) {
	related := relatedPages()
	for file, p := range renderPages(t, cmd.Root()) {
		want := 0
		if file != indexFile && len(related[strings.TrimSuffix(file, ".md")]) > 0 {
			want = 1
		}

		if got := strings.Count(p.Body, "\n## See Also\n"); got != want {
			t.Errorf("render() %s has %d See Also sections, want %d", file, got, want)
		}
	}
}

// Each command with related pages is a top-level phenix command. Each page is a documentation
// page outside the reference, with the heading that the link names.
//
//nolint:paralleltest // reads the shared phenix command tree
func TestRelatedPagesExist(t *testing.T) {
	content := filepath.Join("..", "..", "..", "..", "docs", "content")

	top := make(map[string]bool)
	for _, c := range available(cmd.Root()) {
		top[c.Name()] = true
	}

	for name, links := range relatedPages() {
		if !top[name] {
			t.Errorf("relatedPages() has %q, which is not a top-level command", name)
		}

		for _, l := range links {
			file, anchor, _ := strings.Cut(l.Path, "#")

			if strings.HasPrefix(file, "cli/") || l.Title == "" || l.About == "" {
				t.Errorf("relatedPages()[%q] has %+v, want a titled page outside the reference", name, l)
			}

			body, err := os.ReadFile(filepath.Join(content, file))
			if err != nil {
				t.Errorf("relatedPages()[%q] links to %s: %v", name, file, err)

				continue
			}

			if anchor != "" && !hasHeadingSlug(string(body), anchor) {
				t.Errorf("relatedPages()[%q] links to %s, which has no such heading", name, l.Path)
			}
		}
	}
}
