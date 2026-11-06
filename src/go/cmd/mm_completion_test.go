package cmd

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// mmArgsCompletion asks the minimega under base-dir.minimega for suggestions
// to complete the command line, and completes nothing for --attach.
func TestMMArgsCompletion(t *testing.T) { //nolint:paralleltest // sets PHENIX_BASE_DIR_MINIMEGA
	var (
		mu    sync.Mutex
		lines []string
	)

	base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
		mu.Lock()
		lines = append(lines, req.Suggest)
		mu.Unlock()

		return []miniclient.Response{{Suggest: []string{req.Suggest + "info ", "other "}}}
	})

	t.Setenv("PHENIX_BASE_DIR_MINIMEGA", base)

	if got := viper.GetString("base-dir.minimega"); got != base {
		t.Fatalf("viper.GetString(base-dir.minimega) = %q, want %q", got, base)
	}

	tests := []struct {
		name       string
		args       []string
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{name: "blank line", want: []string{"info", "other"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "after a word", args: []string{"vm"}, want: []string{"info"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{
			name:       "partial word",
			args:       []string{"vm"},
			toComplete: "in",
			want:       []string{"ininfo"},
			directive:  cobra.ShellCompDirectiveNoFileComp,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, directive := mmArgsCompletion(newMMCmd(), test.args, test.toComplete)

			if !slices.Equal(got, test.want) {
				t.Errorf("mmArgsCompletion(%q, %q) = %q, want %q", test.args, test.toComplete, got, test.want)
			}

			if directive != test.directive {
				t.Errorf("mmArgsCompletion(%q, %q) directive = %d, want %d", test.args, test.toComplete, directive, test.directive)
			}
		})
	}

	mu.Lock()
	sent := slices.Clone(lines)
	mu.Unlock()

	if want := []string{" ", "vm ", "vm in"}; !slices.Equal(sent, want) {
		t.Errorf("mmArgsCompletion sent suggestion requests %q, want %q", sent, want)
	}

	attach := newMMCmd()
	if err := attach.Flags().Set("attach", "true"); err != nil {
		t.Fatalf("setting --attach: %v", err)
	}

	if got, directive := mmArgsCompletion(attach, nil, ""); got != nil || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("mmArgsCompletion(--attach) = %q, %d, want nil, %d", got, directive, cobra.ShellCompDirectiveNoFileComp)
	}

	t.Setenv("PHENIX_BASE_DIR_MINIMEGA", filepath.Join(base, "missing"))

	if _, directive := mmArgsCompletion(newMMCmd(), nil, ""); directive != cobra.ShellCompDirectiveError {
		t.Errorf("mmArgsCompletion() with no minimega directive = %d, want %d", directive, cobra.ShellCompDirectiveError)
	}
}
