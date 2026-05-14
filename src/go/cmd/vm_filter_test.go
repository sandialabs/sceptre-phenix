package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"phenix/util/mm"
)

// ---------------------------------------------------------------------------
// normalizeVMLabels
// ---------------------------------------------------------------------------

func TestNormalizeVMLabels_Empty(t *testing.T) {
	if got := normalizeVMLabels(nil); got != nil {
		t.Fatalf("expected nil slice for nil input, got %v", got)
	}
}

func TestNormalizeVMLabels_SingleItem(t *testing.T) {
	got := normalizeVMLabels([]string{"app-label"})
	if len(got) != 1 || got[0] != "app-label" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestNormalizeVMLabels_CommaSeparatedExpandsToMultiple(t *testing.T) {
	got := normalizeVMLabels([]string{"label-a,label-b"})
	if len(got) != 2 || got[0] != "label-a" || got[1] != "label-b" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestNormalizeVMLabels_MultipleEntriesMerged(t *testing.T) {
	got := normalizeVMLabels([]string{"label-a", "label-b", "label-c"})
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(got), got)
	}
}

func TestNormalizeVMLabels_TrimsWhitespace(t *testing.T) {
	got := normalizeVMLabels([]string{" label-a , label-b "})
	if len(got) != 2 || got[0] != "label-a" || got[1] != "label-b" {
		t.Fatalf("unexpected result after trimming: %v", got)
	}
}

func TestNormalizeVMLabels_EmptyPiecesSkipped(t *testing.T) {
	// e.g. trailing comma or double comma
	got := normalizeVMLabels([]string{"label-a,,label-b"})
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
