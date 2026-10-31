package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newTestRootWithGlobalFlag builds a standalone root command with a
// persistent ("global") flag, mimicking rootCmd's global flags without
// touching the package-level rootCmd used by the rest of the CLI.
func newTestRootWithGlobalFlag() *cobra.Command {
	root := &cobra.Command{
		Use:          "phenix",
		SilenceUsage: true,
	}

	root.PersistentFlags().String("log.level", "info", "level to log messages at")

	return root
}

func runHelp(cmd *cobra.Command, args []string) string {
	var output bytes.Buffer

	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(append(args, "--help"))

	_ = cmd.Execute()

	return output.String()
}

func TestHideInheritedHelpHidesGlobalFlags(t *testing.T) {
	root := newTestRootWithGlobalFlag()

	child := &cobra.Command{
		Use: "child",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	hideInheritedHelp(child)
	root.AddCommand(child)

	got := runHelp(root, []string{"child"})

	if strings.Contains(got, "Global Flags") {
		t.Fatalf("expected 'Global Flags' section to be hidden, got:\n%s", got)
	}

	if strings.Contains(got, "log.level") {
		t.Fatalf("expected inherited --log.level flag to be hidden, got:\n%s", got)
	}
}

func TestHideInheritedHelpKeepsGlobalFlagsWhenNotHidden(t *testing.T) {
	root := newTestRootWithGlobalFlag()

	child := &cobra.Command{
		Use: "child",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	// No hideInheritedHelp call: global flags should still show up.
	root.AddCommand(child)

	got := runHelp(root, []string{"child"})

	if !strings.Contains(got, "Global Flags") {
		t.Fatalf("expected 'Global Flags' section to be present, got:\n%s", got)
	}

	if !strings.Contains(got, "log.level") {
		t.Fatalf("expected inherited --log.level flag to be listed, got:\n%s", got)
	}
}

func TestHideInheritedHelpAppliesToNestedSubcommands(t *testing.T) {
	root := newTestRootWithGlobalFlag()

	child := &cobra.Command{Use: "child"}
	hideInheritedHelp(child)

	grandchild := &cobra.Command{
		Use: "grandchild",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	// grandchild does not set its own help func, so it should inherit the
	// hiding behavior from its parent via cobra's HelpFunc() chain.
	child.AddCommand(grandchild)
	root.AddCommand(child)

	got := runHelp(root, []string{"child", "grandchild"})

	if strings.Contains(got, "Global Flags") {
		t.Fatalf("expected 'Global Flags' section to be hidden on nested subcommand, got:\n%s", got)
	}
}

// renderHelp generates cmd's help without executing it, so the root
// command's PersistentPreRunE does not run.
func renderHelp(t *testing.T, cmd *cobra.Command) string {
	t.Helper()

	var output bytes.Buffer

	cmd.SetOut(&output)
	defer cmd.SetOut(nil)

	cmd.HelpFunc()(cmd, nil)

	return output.String()
}

// descendants returns every command below cmd.
func descendants(cmd *cobra.Command) []*cobra.Command {
	all := make([]*cobra.Command, 0, len(cmd.Commands()))

	for _, child := range cmd.Commands() {
		all = append(all, child)
		all = append(all, descendants(child)...)
	}

	return all
}

// TestRootCommandsHideGlobalFlagsInHelp pins which commands list the root
// command's global flags in their help: only ui, which needs them to be
// discoverable. Every other command, and every subcommand, hides them so
// help shows just the flags that command takes.
func TestRootCommandsHideGlobalFlagsInHelp(t *testing.T) {
	showsGlobalFlags := map[string]bool{"ui": true}

	for _, top := range rootCmd.Commands() {
		// cobra generates the help command, so it does not go through
		// addCommandToRoot.
		if top.Name() == "help" {
			continue
		}

		want := showsGlobalFlags[top.Name()]

		for _, cmd := range append([]*cobra.Command{top}, descendants(top)...) {
			t.Run(cmd.CommandPath(), func(t *testing.T) {
				if got := strings.Contains(renderHelp(t, cmd), "Global Flags"); got != want {
					t.Errorf("%q help lists Global Flags = %t, want %t", cmd.CommandPath(), got, want)
				}
			})
		}
	}
}

// TestHideInheritedHelpRestoresGlobalFlags checks that hiding is scoped to the
// help it was generated for. The inherited flags belong to the parent, so
// leaving them hidden would hide them everywhere else too.
func TestHideInheritedHelpRestoresGlobalFlags(t *testing.T) {
	root := newTestRootWithGlobalFlag()

	hiding := &cobra.Command{
		Use: "hiding",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	hideInheritedHelp(hiding)

	showing := &cobra.Command{
		Use: "showing",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	root.AddCommand(hiding, showing)

	_ = runHelp(root, []string{"hiding"})

	if got := runHelp(root, []string{"showing"}); !strings.Contains(got, "log.level") {
		t.Errorf("sibling help lost the inherited --log.level flag, got:\n%s", got)
	}

	if got := runHelp(root, nil); !strings.Contains(got, "log.level") {
		t.Errorf("root help lost its own --log.level flag, got:\n%s", got)
	}
}

func TestHideInheritedHelpDoesNotBreakFlagParsing(t *testing.T) {
	root := newTestRootWithGlobalFlag()

	var seen string

	child := &cobra.Command{
		Use: "child",
		RunE: func(cmd *cobra.Command, args []string) error {
			seen, _ = cmd.Flags().GetString("log.level")
			return nil
		},
	}

	hideInheritedHelp(child)
	root.AddCommand(child)

	root.SetArgs([]string{"child", "--log.level", "debug"})

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error executing command: %v", err)
	}

	if seen != "debug" {
		t.Fatalf("expected hidden --log.level flag to still be parsed, got %q", seen)
	}
}
