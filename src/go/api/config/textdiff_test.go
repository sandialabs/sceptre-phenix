package config

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// unifiedDiff writes the hunks that diff writes, with three lines of context.
func TestUnifiedDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, a, b, want string
	}{
		{name: "same", a: "a\nb\n", b: "a\nb\n", want: ""},
		{name: "one line changed", a: "a\nb\nc\n", b: "a\nx\nc\n", want: "--- from\n+++ to\n@@ -1,3 +1,3 @@\n a\n-b\n+x\n c\n"},
		{name: "from nothing", a: "", b: "a\nb\n", want: "--- from\n+++ to\n@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{name: "to nothing", a: "a\n", b: "", want: "--- from\n+++ to\n@@ -1 +0,0 @@\n-a\n"},
		{
			name: "two hunks",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n",
			b:    "x\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ny\n",
			want: "--- from\n+++ to\n@@ -1,4 +1,4 @@\n-1\n+x\n 2\n 3\n 4\n@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+y\n",
		},
		{
			name: "changes close together make one hunk",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n",
			b:    "x\n2\n3\n4\n5\n6\n7\ny\n",
			want: "--- from\n+++ to\n@@ -1,8 +1,8 @@\n-1\n+x\n 2\n 3\n 4\n 5\n 6\n 7\n-8\n+y\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(test.want, unifiedDiff("from", "to", test.a, test.b)); diff != "" {
				t.Errorf("unifiedDiff(%q, %q) mismatch (-want +got):\n%s", test.a, test.b, diff)
			}
		})
	}
}

// patch applies the unified diff d to a, as patch does, and returns the text.
func patch(a, d string) (string, error) {
	lines := splitLines(a)
	diffLines := splitLines(d)

	if len(diffLines) < 2 || !strings.HasPrefix(diffLines[0], "--- ") || !strings.HasPrefix(diffLines[1], "+++ ") {
		return "", fmt.Errorf("no diff header in %q", d)
	}

	var (
		out  []string
		next int // the next line of a to copy
	)

	for i := 2; i < len(diffLines); i++ {
		var aStart int

		if _, err := fmt.Sscanf(diffLines[i], "@@ -%d", &aStart); err != nil {
			return "", fmt.Errorf("line %q is not a hunk header: %w", diffLines[i], err)
		}

		header := strings.Fields(diffLines[i])[1]
		if strings.HasSuffix(header, ",0") {
			aStart++ // an empty range names the line before it
		}

		for ; next < aStart-1; next++ {
			out = append(out, lines[next])
		}

		for i+1 < len(diffLines) && !strings.HasPrefix(diffLines[i+1], "@@") {
			i++

			line := diffLines[i]
			switch line[0] {
			case ' ':
				if lines[next] != line[1:] {
					return "", fmt.Errorf("context %q does not match line %d %q", line[1:], next+1, lines[next])
				}

				out = append(out, line[1:])
				next++
			case '-':
				if lines[next] != line[1:] {
					return "", fmt.Errorf("removed %q does not match line %d %q", line[1:], next+1, lines[next])
				}

				next++
			case '+':
				out = append(out, line[1:])
			}
		}
	}

	out = append(out, lines[next:]...)

	if len(out) == 0 {
		return "", nil
	}

	return strings.Join(out, "\n") + "\n", nil
}

// The unified diff of two texts patches the first into the second, and is
// empty only when they are the same.
func TestUnifiedDiffPatches(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		gen := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b", "c", "d", "  key: 1"}), 0, 40)
		a, b := gen.Draw(t, "a"), gen.Draw(t, "b")

		text := func(lines []string) string {
			if len(lines) == 0 {
				return ""
			}

			return strings.Join(lines, "\n") + "\n"
		}

		d := unifiedDiff("from", "to", text(a), text(b))
		if (d == "") != (text(a) == text(b)) {
			t.Fatalf("unifiedDiff(%q, %q) = %q, want empty only for the same texts", a, b, d)
		}

		if d == "" {
			return
		}

		got, err := patch(text(a), d)
		if err != nil {
			t.Fatalf("patch(%q, unifiedDiff) error = %v; diff:\n%s", a, err, d)
		}

		if got != text(b) {
			t.Fatalf("patch(%q, unifiedDiff) = %q, want %q; diff:\n%s", a, got, text(b), d)
		}
	})
}

// A diff too large for the comparison table still patches the first text
// into the second.
func TestUnifiedDiffLarge(t *testing.T) {
	t.Parallel()

	var a, b strings.Builder

	for i := range 3000 {
		a.WriteString("a" + strconv.Itoa(i) + "\n")
		b.WriteString("b" + strconv.Itoa(i) + "\n")
	}

	d := unifiedDiff("from", "to", a.String(), b.String())

	got, err := patch(a.String(), d)
	if err != nil || got != b.String() {
		t.Errorf("patch(a, unifiedDiff(a, b)) = %d bytes, %v, want b", len(got), err)
	}
}
