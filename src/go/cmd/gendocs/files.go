package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	navBegin = "# BEGIN generated CLI reference (make cli-docs)"
	navEnd   = "# END generated CLI reference"
)

var errNavMarkers = errors.New("needs one " + navBegin + " line followed by one " + navEnd + " line")

// writePages writes pages into dir and removes any other Markdown file there, such as the page of
// a command that no longer exists.
func writePages(dir string, pages []page) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301 committed docs are public
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	keep := make(map[string]bool, len(pages))

	for _, p := range pages {
		keep[p.File] = true

		name := filepath.Join(dir, p.File)

		if err := os.WriteFile(name, []byte(p.Body), 0o644); err != nil { //nolint:gosec // G306 committed docs are public
			return fmt.Errorf("writing %s: %w", p.File, err)
		}
	}

	existing, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return fmt.Errorf("listing %s: %w", dir, err)
	}

	for _, f := range existing {
		if keep[filepath.Base(f)] {
			continue
		}

		if err := os.Remove(f); err != nil {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}

	return nil
}

// updateNav lists pages, under dir, between the generated nav markers of the mkdocs config at path.
func updateNav(path, dir string, pages []page) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	updated, err := replaceNav(string(data), dir, pages)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	if updated == string(data) {
		return nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil { //nolint:gosec // G306 committed docs config is public
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// replaceNav returns config with the lines between its generated nav markers replaced by a nav
// entry for each page, indented like the begin marker.
func replaceNav(config, dir string, pages []page) (string, error) {
	lines := strings.SplitAfter(config, "\n")
	begin, end := -1, -1

	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case navBegin:
			if begin >= 0 {
				return "", errNavMarkers
			}

			begin = i
		case navEnd:
			if end >= 0 {
				return "", errNavMarkers
			}

			end = i
		}
	}

	if begin < 0 || end < begin {
		return "", errNavMarkers
	}

	indent := lines[begin][:len(lines[begin])-len(strings.TrimLeft(lines[begin], " "))]

	var b strings.Builder

	for _, line := range lines[:begin+1] {
		b.WriteString(line)
	}

	for _, p := range pages {
		fmt.Fprintf(&b, "%s- %s: %s/%s\n", indent, p.Title, dir, p.File)
	}

	for _, line := range lines[end:] {
		b.WriteString(line)
	}

	return b.String(), nil
}
