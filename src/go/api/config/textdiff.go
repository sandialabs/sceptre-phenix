package config

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// diffContext is the number of unchanged lines around each change.
	diffContext = 3
	// diffMaxCells caps the table of the line comparison. Above it, the
	// changed middle part is shown as removed and then added in full.
	diffMaxCells = 4 << 20
)

// diffOp is one line of a line diff: ' ' kept, '-' removed or '+' added.
type diffOp struct {
	kind byte
	line string
}

// Diff returns a unified diff from the stored config, named from, to the
// file, named to. It returns "" when the two do not differ. A config that the
// store does not hold is compared with no lines.
func (c *Comparison) Diff(from, to string) string {
	return unifiedDiff(from, to, string(c.StoredYAML), string(c.FileYAML))
}

// unifiedDiff returns the unified diff of the lines of a and b, with headers
// that name them from and to, or "" when they are the same.
func unifiedDiff(from, to, a, b string) string {
	if a == b {
		return ""
	}

	ops := lineDiff(splitLines(a), splitLines(b))

	var out strings.Builder

	fmt.Fprintf(&out, "--- %s\n+++ %s\n", from, to)

	for start := 0; start < len(ops); {
		first := nextChange(ops, start)
		if first == len(ops) {
			break
		}

		lo := max(first-diffContext, start)
		hi := hunkEnd(ops, first)

		writeHunk(&out, ops, lo, hi)

		start = hi
	}

	return out.String()
}

// splitLines splits s into lines without their newlines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// nextChange returns the index of the first changed op at or after i, or len(ops).
func nextChange(ops []diffOp, i int) int {
	for i < len(ops) && ops[i].kind == ' ' {
		i++
	}

	return i
}

// hunkEnd returns the end of the hunk whose first change is at first: the
// hunk takes in each change within twice the context of the one before.
func hunkEnd(ops []diffOp, first int) int {
	end := first

	for i := first; i < len(ops); i++ {
		if ops[i].kind != ' ' {
			end = i + 1

			continue
		}

		if i-end >= 2*diffContext {
			break
		}
	}

	return min(end+diffContext, len(ops))
}

// writeHunk writes ops[lo:hi] as one hunk, with its @@ header.
func writeHunk(out *strings.Builder, ops []diffOp, lo, hi int) {
	aStart, bStart := 1, 1

	for _, op := range ops[:lo] {
		if op.kind != '+' {
			aStart++
		}

		if op.kind != '-' {
			bStart++
		}
	}

	var aLen, bLen int

	for _, op := range ops[lo:hi] {
		if op.kind != '+' {
			aLen++
		}

		if op.kind != '-' {
			bLen++
		}
	}

	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(aStart, aLen), hunkRange(bStart, bLen))

	for _, op := range ops[lo:hi] {
		out.WriteByte(op.kind)
		out.WriteString(op.line)
		out.WriteByte('\n')
	}
}

// hunkRange is the start,length range of a hunk header. An empty range starts
// at the line before it, as diff writes it.
func hunkRange(start, length int) string {
	if length == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}

	if length == 1 {
		return strconv.Itoa(start)
	}

	return fmt.Sprintf("%d,%d", start, length)
}

// lineDiff returns the ops that change a into b: the common start and end are
// kept, and the middle comes from a longest common subsequence.
func lineDiff(a, b []string) []diffOp {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	ops := make([]diffOp, 0, len(a)+len(b))

	for _, line := range a[:prefix] {
		ops = append(ops, diffOp{kind: ' ', line: line})
	}

	ops = append(ops, middleDiff(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)

	for _, line := range a[len(a)-suffix:] {
		ops = append(ops, diffOp{kind: ' ', line: line})
	}

	return ops
}

// middleDiff returns the ops that change a into b from a longest common
// subsequence table, or all of a removed and all of b added when the table
// would be larger than diffMaxCells.
func middleDiff(a, b []string) []diffOp {
	ops := make([]diffOp, 0, len(a)+len(b))

	if (len(a)+1)*(len(b)+1) > diffMaxCells {
		for _, line := range a {
			ops = append(ops, diffOp{kind: '-', line: line})
		}

		for _, line := range b {
			ops = append(ops, diffOp{kind: '+', line: line})
		}

		return ops
	}

	// lcs[i][j] is the length of a longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}

	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	i, j := 0, 0

	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', line: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{kind: '-', line: a[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', line: b[j]})
			j++
		}
	}

	for ; i < len(a); i++ {
		ops = append(ops, diffOp{kind: '-', line: a[i]})
	}

	for ; j < len(b); j++ {
		ops = append(ops, diffOp{kind: '+', line: b[j]})
	}

	return ops
}
