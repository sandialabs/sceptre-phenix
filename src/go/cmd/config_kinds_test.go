package cmd

import (
	"slices"
	"strings"
	"testing"
)

// TestConfigKindsExcludeWorkflow proves the Workflow schema did not become a
// config kind that the config commands accept, list or complete.
func TestConfigKindsExcludeWorkflow(t *testing.T) {
	isWorkflow := func(kind string) bool { return strings.EqualFold(kind, "workflow") }

	if slices.ContainsFunc(configKinds(), isWorkflow) {
		t.Errorf("configKinds() = %v, includes workflow", configKinds())
	}

	if slices.ContainsFunc(configListKinds(), isWorkflow) {
		t.Errorf("configListKinds() = %v, includes workflow", configListKinds())
	}

	// Loop variables keep unparam from seeing a constant argument.
	for _, opts := range []struct{ multi, allowAll bool }{{false, false}, {true, false}, {true, true}} {
		if err := configKindArgsValidator(opts.multi, opts.allowAll)(nil, []string{"workflow/wf"}); err == nil {
			t.Errorf("configKindArgsValidator(%t, %t) accepted workflow/wf", opts.multi, opts.allowAll)
		}
	}

	if err := configKindValidator()(nil, []string{"workflow"}); err == nil {
		t.Error("configKindValidator() accepted workflow")
	}
}
