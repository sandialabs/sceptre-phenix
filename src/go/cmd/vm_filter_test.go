package cmd

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"pgregory.net/rapid"

	"phenix/util/mm"
)

// ---------------------------------------------------------------------------
// normalizeVMFilterValues
// ---------------------------------------------------------------------------

func TestNormalizeVMFilterValues_Empty(t *testing.T) {
	if got := normalizeVMFilterValues(nil); got != nil {
		t.Fatalf("expected nil slice for nil input, got %v", got)
	}
}

func TestNormalizeVMFilterValues_SingleItem(t *testing.T) {
	got := normalizeVMFilterValues([]string{"app-label"})
	if len(got) != 1 || got[0] != "app-label" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestNormalizeVMFilterValues_CommaSeparatedExpandsToMultiple(t *testing.T) {
	got := normalizeVMFilterValues([]string{"label-a,label-b"})
	if len(got) != 2 || got[0] != "label-a" || got[1] != "label-b" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestNormalizeVMFilterValues_MultipleEntriesMerged(t *testing.T) {
	got := normalizeVMFilterValues([]string{"label-a", "label-b", "label-c"})
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(got), got)
	}
}

func TestNormalizeVMFilterValues_TrimsWhitespace(t *testing.T) {
	got := normalizeVMFilterValues([]string{" label-a , label-b "})
	if len(got) != 2 || got[0] != "label-a" || got[1] != "label-b" {
		t.Fatalf("unexpected result after trimming: %v", got)
	}
}

func TestNormalizeVMFilterValues_EmptyPiecesSkipped(t *testing.T) {
	// e.g. trailing comma or double comma
	got := normalizeVMFilterValues([]string{"label-a,,label-b"})
	if len(got) != 2 || got[0] != "label-a" || got[1] != "label-b" {
		t.Fatalf("expected empty pieces to be skipped, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// vmLabelMatchesLabel
// ---------------------------------------------------------------------------

func TestVMLabelMatchesLabel_ExactMatch(t *testing.T) {
	matched, err := vmLabelMatchesLabel("my-app", "my-app")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected exact match")
	}
}

func TestVMLabelMatchesLabel_CaseInsensitiveExact(t *testing.T) {
	matched, err := vmLabelMatchesLabel("My-App", "MY-APP")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected case-insensitive exact match")
	}
}

func TestVMLabelMatchesLabel_NoMatch(t *testing.T) {
	matched, err := vmLabelMatchesLabel("my-app", "other-label")
	if err != nil {
		t.Fatal(err)
	}

	if matched {
		t.Fatal("expected no match")
	}
}

func TestVMLabelMatchesLabel_GlobSuffixWildcard(t *testing.T) {
	matched, err := vmLabelMatchesLabel("app-controller", "app-*")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected suffix wildcard glob match")
	}
}

func TestVMLabelMatchesLabel_GlobPrefixWildcard(t *testing.T) {
	matched, err := vmLabelMatchesLabel("sceptre-app", "*-app")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected prefix wildcard glob match")
	}
}

func TestVMLabelMatchesLabel_GlobMiddleWildcard(t *testing.T) {
	matched, err := vmLabelMatchesLabel("sceptre-ot-app", "sceptre-*-app")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected middle wildcard glob match")
	}
}

func TestVMLabelMatchesLabel_GlobNoMatch(t *testing.T) {
	matched, err := vmLabelMatchesLabel("my-label", "app-*")
	if err != nil {
		t.Fatal(err)
	}

	if matched {
		t.Fatal("expected no glob match")
	}
}

func TestVMLabelMatchesLabel_GlobCaseInsensitive(t *testing.T) {
	matched, err := vmLabelMatchesLabel("App-Controller", "app-*")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected case-insensitive glob match")
	}
}

func TestVMLabelMatchesLabel_SingleCharWildcard(t *testing.T) {
	matched, err := vmLabelMatchesLabel("vm1", "vm?")
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected single-char wildcard match")
	}
}

func TestVMLabelMatchesLabel_InvalidGlobReturnsError(t *testing.T) {
	// path.Match returns an error for malformed bracket expressions
	_, err := vmLabelMatchesLabel("any-label", "[invalid")
	if err == nil {
		t.Fatal("expected error for invalid glob pattern")
	}
}

// ---------------------------------------------------------------------------
// vmMatchesAnyLabel
// ---------------------------------------------------------------------------

func makeVM(tags map[string]string) mm.VM {
	return mm.VM{
		Name: "vm-1",
		Tags: tags,
	}
}

func TestVMMatchesAnyLabel_EmptyFilters(t *testing.T) {
	v := makeVM(map[string]string{"app": "true"})

	matched, err := vmMatchesAnyLabel(v, nil)
	if err != nil {
		t.Fatal(err)
	}

	if matched {
		t.Fatal("expected no match for empty filters")
	}
}

func TestVMMatchesAnyLabel_AllKeyword(t *testing.T) {
	v := makeVM(map[string]string{"app": "true"})

	matched, err := vmMatchesAnyLabel(v, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected 'all' to match any VM")
	}
}

func TestVMMatchesAnyLabel_AllKeywordCaseInsensitive(t *testing.T) {
	v := makeVM(map[string]string{})

	matched, err := vmMatchesAnyLabel(v, []string{"ALL"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected 'ALL' to match any VM")
	}
}

func TestVMMatchesAnyLabel_AllKeywordMatchesVMWithNoLabels(t *testing.T) {
	v := makeVM(map[string]string{})

	matched, err := vmMatchesAnyLabel(v, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected 'all' to match VM even with no labels")
	}
}

func TestVMMatchesAnyLabel_ExactLabelMatch(t *testing.T) {
	v := makeVM(map[string]string{"sceptre-app": "true"})

	matched, err := vmMatchesAnyLabel(v, []string{"sceptre-app"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected exact label to match")
	}
}

func TestVMMatchesAnyLabel_NoMatchingLabel(t *testing.T) {
	v := makeVM(map[string]string{"sceptre-app": "true"})

	matched, err := vmMatchesAnyLabel(v, []string{"other-label"})
	if err != nil {
		t.Fatal(err)
	}

	if matched {
		t.Fatal("expected no match when label is absent")
	}
}

func TestVMMatchesAnyLabel_MultipleFiltersOrSemantics(t *testing.T) {
	v := makeVM(map[string]string{"label-b": "true"})

	// VM has label-b; filter includes both label-a and label-b, should match.
	matched, err := vmMatchesAnyLabel(v, []string{"label-a", "label-b"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected OR-semantics match when any filter matches")
	}
}

func TestVMMatchesAnyLabel_NoLabelsOnVM(t *testing.T) {
	v := makeVM(nil)

	matched, err := vmMatchesAnyLabel(v, []string{"some-label"})
	if err != nil {
		t.Fatal(err)
	}

	if matched {
		t.Fatal("expected no match for VM with no labels")
	}
}

func TestVMMatchesAnyLabel_GlobMatchesLabel(t *testing.T) {
	v := makeVM(map[string]string{"sceptre-ot-sim": "true"})

	matched, err := vmMatchesAnyLabel(v, []string{"sceptre-*"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected glob to match label")
	}
}

func TestVMMatchesAnyLabel_GlobMatchesOneOfMultipleLabels(t *testing.T) {
	v := makeVM(map[string]string{
		"unrelated":   "true",
		"sceptre-app": "true",
	})

	matched, err := vmMatchesAnyLabel(v, []string{"sceptre-*"})
	if err != nil {
		t.Fatal(err)
	}

	if !matched {
		t.Fatal("expected glob to match when at least one label matches")
	}
}

// genVMFilterLabelArgs draws --label values whose pieces are short and whose
// commas and blanks fall anywhere.
func genVMFilterLabelArgs() *rapid.Generator[[]string] {
	return rapid.SliceOfN(rapid.StringOfN(rapid.RuneFrom([]rune("ab, \t")), 0, 8, -1), 0, 4)
}

func TestNormalizeVMFilterValuesProperties(t *testing.T) {
	t.Parallel()

	dropBlanks := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ',' || r == ' ' || r == '\t' {
				return -1
			}

			return r
		}, s)
	}

	rapid.Check(t, func(t *rapid.T) {
		in := genVMFilterLabelArgs().Draw(t, "in")
		got := normalizeVMFilterValues(in)

		for _, piece := range got {
			if piece == "" || strings.Contains(piece, ",") || strings.TrimSpace(piece) != piece {
				t.Fatalf("normalizeVMFilterValues(%q) = %q, has the piece %q", in, got, piece)
			}
		}

		// no label text is lost: only commas and the blanks around pieces go
		if want, kept := dropBlanks(strings.Join(in, "")), dropBlanks(strings.Join(got, "")); kept != want {
			t.Fatalf("normalizeVMFilterValues(%q) = %q, keeps %q, want %q", in, got, kept, want)
		}

		joined := []string{strings.Join(in, ",")}
		if diff := cmp.Diff(got, normalizeVMFilterValues(joined)); diff != "" {
			t.Fatalf("normalizeVMFilterValues(%q) and normalizeVMFilterValues(%q) mismatch (-separate +joined):\n%s", in, joined, diff)
		}

		if again := normalizeVMFilterValues(got); !slices.Equal(again, got) {
			t.Fatalf("normalizeVMFilterValues(%q) = %q, want %q", got, again, got)
		}
	})
}

// vmFilterGlobModel is a case-insensitive shell glob of *, ? and literals as a
// regular expression, the model vmLabelMatchesLabel is checked against.
func vmFilterGlobModel(pattern string) *regexp.Regexp {
	var expr strings.Builder

	for _, r := range pattern {
		switch r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		default:
			expr.WriteString(regexp.QuoteMeta(string(r)))
		}
	}

	return regexp.MustCompile("(?is)^" + expr.String() + "$")
}

func TestVMLabelMatchesLabelProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		label := rapid.StringOfN(rapid.RuneFrom([]rune("aAbB-.")), 0, 5, -1).Draw(t, "label")
		pattern := rapid.StringOfN(rapid.RuneFrom([]rune("aAbB-.*?")), 0, 5, -1).Draw(t, "pattern")

		got, err := vmLabelMatchesLabel(label, pattern)
		if want := vmFilterGlobModel(pattern).MatchString(label); err != nil || got != want {
			t.Fatalf("vmLabelMatchesLabel(%q, %q) = %t, %v; want %t, nil", label, pattern, got, err, want)
		}
	})
}

// genVMFilterVM draws a VM whose labels come from a small set.
func genVMFilterVM() *rapid.Generator[mm.VM] {
	return rapid.Custom(func(t *rapid.T) mm.VM {
		keys := rapid.SliceOfDistinct(rapid.SampledFrom([]string{"a", "B", "ab", "web-1"}), rapid.ID).Draw(t, "labels")
		tags := make(map[string]string, len(keys))

		for _, key := range keys {
			tags[key] = "true"
		}

		return makeVM(tags)
	})
}

func TestVMMatchesAnyLabelProperties(t *testing.T) {
	t.Parallel()

	genLabels := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "A", "b", "AB", "a*", "?", "web-?", "all", "ALL"}), 0, 3)

	rapid.Check(t, func(t *rapid.T) {
		vmInfo := genVMFilterVM().Draw(t, "vm")
		first := genLabels.Draw(t, "first")
		second := genLabels.Draw(t, "second")

		matches := func(labels []string) bool {
			matched, err := vmMatchesAnyLabel(vmInfo, labels)
			if err != nil {
				t.Fatalf("vmMatchesAnyLabel(%v, %q) error = %v", vmInfo.Tags, labels, err)
			}

			return matched
		}

		// --label filters are ORed: the filters of two flags match as either does
		both := slices.Concat(first, second)
		if got, want := matches(both), matches(first) || matches(second); got != want {
			t.Fatalf("vmMatchesAnyLabel(%v, %q) = %t, want %t", vmInfo.Tags, both, got, want)
		}

		if len(first) == 1 && !strings.ContainsAny(first[0], "*?") {
			want := strings.EqualFold(first[0], "all") ||
				slices.ContainsFunc(slices.Collect(maps.Keys(vmInfo.Tags)), func(k string) bool { return strings.EqualFold(k, first[0]) })
			if got := matches(first); got != want {
				t.Fatalf("vmMatchesAnyLabel(%v, %q) = %t, want %t", vmInfo.Tags, first, got, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// vmTargetNamesForCommand
// ---------------------------------------------------------------------------

func newTestRestartCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "restart",
	}
	addVMLabelFlag(c)

	return c
}

func TestVMTargetNamesForCommand_MissingExperimentName(t *testing.T) {
	c := newTestRestartCmd()

	_, _, err := vmTargetNamesForCommand(c, nil)
	if err == nil {
		t.Fatal("expected error when no args provided")
	}
}

func TestVMTargetNamesForCommand_SingleVMByPositionalArg(t *testing.T) {
	c := newTestRestartCmd()

	expName, names, err := vmTargetNamesForCommand(c, []string{"my-exp", "vm-1"})
	if err != nil {
		t.Fatal(err)
	}

	if expName != "my-exp" {
		t.Fatalf("expected experiment 'my-exp', got %q", expName)
	}

	if len(names) != 1 || names[0] != "vm-1" {
		t.Fatalf("expected [vm-1], got %v", names)
	}
}

func TestVMTargetNamesForCommand_MissingVMNameWithoutFlag(t *testing.T) {
	c := newTestRestartCmd()

	// Only experiment name, no vm name, no --label flag → error
	_, _, err := vmTargetNamesForCommand(c, []string{"my-exp"})
	if err == nil {
		t.Fatal("expected error when VM name is missing and --label not set")
	}
}

func TestVMTargetNamesForCommand_EmptyLabelFlagReturnsError(t *testing.T) {
	c := newTestRestartCmd()
	// Set flag but provide no value items (empty string that normalizes away)
	if err := c.Flags().Set("label", ""); err != nil {
		t.Fatal(err)
	}

	_, _, err := vmTargetNamesForCommand(c, []string{"my-exp"})
	if err == nil {
		t.Fatal("expected error when --label flag set but produces no filters after normalization")
	}
}
