package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// completionCmdOutput runs phenix completion with args and returns what it
// writes to the command writer.
func completionCmdOutput(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newCompletionCmd())
	root.SetArgs(append([]string{"completion"}, args...))

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return output.String(), err
}

// TestCompletionCmd checks that phenix completion writes the script of each
// shell to the command writer, and refuses other shells.
func TestCompletionCmd(t *testing.T) {
	t.Parallel()

	for shell, header := range map[string]string{
		"bash":       "# bash completion V2 for phenix",
		"zsh":        "#compdef phenix",
		"fish":       "# fish completion for phenix",
		"powershell": "# powershell completion for phenix",
	} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			output, err := completionCmdOutput(shell)
			if err != nil {
				t.Fatalf("phenix completion %s error = %v", shell, err)
			}

			if !strings.HasPrefix(output, header) {
				t.Errorf("phenix completion %s = %.60q..., want a script that starts with %q", shell, output, header)
			}
		})
	}

	for name, args := range map[string][]string{
		"unknown shell":     {"tcsh"},
		"no shell":          nil,
		"two shells":        {"bash", "zsh"},
		"shell and unknown": {"bash", "tcsh"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if output, err := completionCmdOutput(args...); err == nil || output != "" {
				t.Errorf("phenix completion %q = %q, %v, want no output and an error", args, output, err)
			}
		})
	}
}
