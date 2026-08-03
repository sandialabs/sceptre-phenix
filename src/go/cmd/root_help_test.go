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
