package builder_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// caseTableUnicode is the Unicode version of the Go that builds phenix (Go
// 1.24 and 1.25). The web UI's copy of Go's case mapping is of this version.
const caseTableUnicode = "15.0.0"

// frontendCaseTable is the web UI's copy of Go's case mapping (goCase.js
// reads it). It must match [caseTableJSON].
var frontendCaseTable = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "js", "src", "builder", "schema", "go-case.json",
)

// foldCorpusFile holds texts and what Go's case functions make of them,
// which go-case.contract.test.js checks the web UI's copy against.
var foldCorpusFile = filepath.Join("testdata", "fold-corpus.json") //nolint:gochecknoglobals // test fixture path

// caseRun maps the runes first, first+stride, ... up to last to the rune
// delta after each: [first, last, delta, stride].
type caseRun [4]int

// caseRuns returns the runes that mapping changes, as runs in rune order.
func caseRuns(mapping func(rune) rune) []caseRun {
	var runs []caseRun

	for r := range rune(unicode.MaxRune + 1) {
		delta := int(mapping(r) - r)
		if delta == 0 {
			continue
		}

		if n := len(runs); n > 0 {
			last := &runs[n-1]
			step := int(r) - last[1]

			switch {
			case last[2] == delta && last[0] == last[1] && (step == 1 || step == 2):
				last[1], last[3] = int(r), step

				continue
			case last[2] == delta && step == last[3]:
				last[1] = int(r)

				continue
			}
		}

		runs = append(runs, caseRun{int(r), int(r), delta, 1})
	}

	return runs
}

// mapRun maps r by the runs, as goCase.js does.
func mapRun(runs []caseRun, r rune) rune {
	index, found := slices.BinarySearchFunc(runs, int(r), func(run caseRun, target int) int {
		switch {
		case run[1] < target:
			return -1
		case run[0] > target:
			return 1
		default:
			return 0
		}
	})

	if found && (int(r)-runs[index][0])%runs[index][3] == 0 {
		return r + rune(runs[index][2])
	}

	return r
}

// orbitFirst is the smallest rune of the case orbit of r: the runes that
// [strings.EqualFold] takes as r.
func orbitFirst(r rune) rune {
	first := r

	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		first = min(first, next)
	}

	return first
}

// caseTableJSON is the web UI's copy of Go's case mapping: lower holds the
// runs of [unicode.ToLower], and fold the runs that map each rune to the
// smallest rune of its case orbit. Each run is on its own line.
func caseTableJSON() []byte {
	var out bytes.Buffer

	writeRuns := func(name string, runs []caseRun, last bool) {
		fmt.Fprintf(&out, "  %q: [\n", name)

		for i, run := range runs {
			comma := ","
			if i == len(runs)-1 {
				comma = ""
			}

			fmt.Fprintf(&out, "    [%d, %d, %d, %d]%s\n", run[0], run[1], run[2], run[3], comma)
		}

		if last {
			out.WriteString("  ]\n")
		} else {
			out.WriteString("  ],\n")
		}
	}

	out.WriteString("{\n")
	fmt.Fprintf(&out, "  %q: %q,\n", "description",
		"Go's case mapping, from TestFrontendCaseTableMatchesUnicode in phenix/types/builder: "+
			"each run is [first, last, delta, stride]. lower is unicode.ToLower, and fold maps a rune to the smallest "+
			"rune that strings.EqualFold takes as it.")
	fmt.Fprintf(&out, "  %q: %q,\n", "unicode", unicode.Version)
	writeRuns("lower", caseRuns(unicode.ToLower), false)
	writeRuns("fold", caseRuns(orbitFirst), true)
	out.WriteString("}\n")

	return out.Bytes()
}

// skipOtherUnicode skips a test that compares a committed file with the case
// mapping of this Go, when this Go has another Unicode version than the Go
// that builds phenix.
func skipOtherUnicode(t *testing.T) {
	t.Helper()

	if unicode.Version != caseTableUnicode {
		t.Skipf("%s has Unicode %s, and the Go that builds phenix has Unicode %s: "+
			"run this test with Go 1.24 or 1.25, as CI does", runtime.Version(), unicode.Version, caseTableUnicode)
	}
}

// writeOrCompare writes want to path with -update-frontend-schema, then
// compares the file with it.
func writeOrCompare(t *testing.T, path string, want []byte) {
	t.Helper()

	if *updateFrontendSchema {
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s is out of date; regenerate it with make generate-builder-schema (or make generate) in src/go "+
			"(-want +got):\n%s", path, diff)
	}
}

func TestFrontendCaseTableMatchesUnicode(t *testing.T) {
	skipOtherUnicode(t)
	writeOrCompare(t, frontendCaseTable, caseTableJSON())
}

// The runs give [unicode.ToLower] of every rune, and runes have the same
// smallest rune of their orbit exactly when [strings.EqualFold] takes them as
// one. This holds for the Unicode version of any Go.
func TestCaseRunsReproduceUnicode(t *testing.T) {
	lower, fold := caseRuns(unicode.ToLower), caseRuns(orbitFirst)

	for r := range rune(unicode.MaxRune + 1) {
		if got, want := mapRun(lower, r), unicode.ToLower(r); got != want {
			t.Errorf("mapRun(lower, %U) = %U, want unicode.ToLower(%U) = %U", r, got, r, want)
		}

		first := mapRun(fold, r)

		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if got := mapRun(fold, next); got != first {
				t.Errorf("mapRun(fold, %U) = %U, want %U, as for %U of its orbit", next, got, first, r)
			}
		}

		if !strings.EqualFold(string(r), string(first)) {
			t.Errorf("strings.EqualFold(%q, %q) = false, want true for the smallest rune of the orbit", r, first)
		}
	}
}

// foldCase is a text, and what [strings.ToLower] and foldKey make of it.
type foldCase struct {
	In    string `json:"in"`
	Lower string `json:"lower"`
	Key   string `json:"key"`
}

// equalFoldCase is two texts, and whether [strings.EqualFold] takes them as
// one.
type equalFoldCase struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Equal bool   `json:"equal"`
}

// foldCorpus is testdata/fold-corpus.json.
type foldCorpus struct {
	Description string          `json:"description"`
	Unicode     string          `json:"unicode"`
	Fold        []foldCase      `json:"fold"`
	EqualFold   []equalFoldCase `json:"equalFold"`
}

// foldTexts are texts whose case Go and JavaScript map differently, and
// texts with white space around them that [strings.TrimSpace] does or does
// not remove.
//
//nolint:gochecknoglobals // test table
var foldTexts = []string{
	"", "ROUTER", "F\u0130rewall", "\u0130x", "\u0130STANBUL", "\u0131I", "\u03a3\u0391\u03a3",
	"\u039f\u0394\u039f\u03a3", "\u03a3\u0391\u03a3 \u03a3\u0391\u03a3", "\u03a3", "\u03c2",
	"\u13a0\u13f0\uab70", "\u1e9e", "\u01c5\u01c4\u01c6", "\u212a\u212b\u2126", "\u03f4\u0398",
	"\U00010400\U00010428", "\u216b\u24b6", "\ufb00", "\u0149",
	"\u3000 Host\u0085", "\u00a0eth0\u2029", "\ufeffeth0", "\u200beth0", "\u180eeth0",
	"\u1680x\u205f\u202f", "\x1cx\x1f", "\v\fx\r\n\t ", "\u2000\u200aEth0\u2028",
}

// equalFoldPairs are pairs of texts that [strings.EqualFold] takes as one or
// not, and that a lowercase comparison would get wrong.
//
//nolint:gochecknoglobals // test table
var equalFoldPairs = [][2]string{
	{"o\u017fpf", "OSPF"}, {"\u0130", "i"}, {"\u0131", "I"}, {"\u00df", "\u1e9e"}, {"\u212a", "k"},
	{"mgmt", "MGMT"}, {"\u03a3\u0391\u03a3", "\u03c3\u03b1\u03c2"}, {"\u01c5", "\u01c4"}, {"\u212b", "\u00e5"},
	{"\ufb00", "ff"}, {"eth0", "eth0 "}, {"\U00010400", "\U00010428"}, {"\u13a0", "\uab70"}, {"\u03f4", "\u03b8"},
	{"\u03d1", "\u0398"}, {"\u00b5", "\u039c"}, {"", ""}, {"a", ""},
}

// runSample is the first, middle and last rune of a run, as text.
func runSample(run caseRun) []rune {
	middle := run[0] + (run[1]-run[0])/run[3]/2*run[3]

	return []rune{rune(run[0]), rune(middle), rune(run[1])}
}

// foldCorpusJSON is testdata/fold-corpus.json: what Go makes of foldTexts and
// equalFoldPairs, and of the first, middle and last rune of each case run.
func foldCorpusJSON(t *testing.T) []byte {
	t.Helper()

	corpus := foldCorpus{
		Description: "Texts and what Go makes of them: lower is strings.ToLower, key is foldKey in spec.go " +
			"(strings.ToLower of strings.TrimSpace), and equal is strings.EqualFold. Written by TestFoldCorpus " +
			"in phenix/types/builder, and read by go-case.contract.test.js.",
		Unicode: unicode.Version, Fold: nil, EqualFold: nil,
	}

	texts := slices.Clone(foldTexts)
	for _, run := range caseRuns(unicode.ToLower) {
		texts = append(texts, string(runSample(run)))
	}

	for _, text := range texts {
		corpus.Fold = append(corpus.Fold, foldCase{In: text, Lower: strings.ToLower(text), Key: builder.FoldKey(text)})
	}

	pairs := slices.Clone(equalFoldPairs)

	for _, run := range caseRuns(orbitFirst) {
		sample := runSample(run)
		first := make([]rune, len(sample))

		for i, r := range sample {
			first[i] = orbitFirst(r)
		}

		pairs = append(pairs, [2]string{string(sample), string(first)}, [2]string{string(sample), strings.ToLower(string(sample))})
	}

	for _, pair := range pairs {
		corpus.EqualFold = append(corpus.EqualFold, equalFoldCase{
			A: pair[0], B: pair[1], Equal: strings.EqualFold(pair[0], pair[1]),
		})
	}

	var out bytes.Buffer

	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(corpus); err != nil {
		t.Fatalf("encoding the fold corpus: %v", err)
	}

	return asciiJSON(out.Bytes())
}

// asciiJSON escapes each rune of JSON text that is not ASCII, so the file
// shows which runes it holds.
func asciiJSON(data []byte) []byte {
	var out bytes.Buffer

	for _, r := range string(data) {
		switch {
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}

	return out.Bytes()
}

func TestFoldCorpus(t *testing.T) {
	skipOtherUnicode(t)
	writeOrCompare(t, foldCorpusFile, foldCorpusJSON(t))
}
