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

	for _, multi := range []bool{false, true} {
		if err := configKindArgsValidator(multi)(nil, []string{"workflow/wf"}); err == nil {
			t.Errorf("configKindArgsValidator(%t) accepted workflow/wf", multi)
		}
	}

	if err := configKindValidator()(nil, []string{"workflow"}); err == nil {
		t.Error("configKindValidator() accepted workflow")
	}
}
