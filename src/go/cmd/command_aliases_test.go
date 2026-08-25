package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandAliases(t *testing.T) {
	tests := []struct {
		name        string
		parent      *cobra.Command
		command     *cobra.Command
		alias       string
		commandName string
	}{
		{
			name:        "image delete",
			parent:      newImageCmd(),
			command:     newImageDeleteCmd(),
			alias:       deleteAlias,
			commandName: "delete",
		},
		{
			name:        "config delete",
			parent:      newConfigCmd(),
			command:     newConfigDeleteCmd(),
			alias:       deleteAlias,
			commandName: "delete",
		},
		{
			name:        "vm restart",
			parent:      newVMCmd(),
			command:     newVMRestartCmd(),
			alias:       restartAlias,
			commandName: "restart",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := &cobra.Command{Use: "phenix"}
			test.parent.AddCommand(test.command)
			root.AddCommand(test.parent)

			command, _, err := root.Find([]string{test.parent.Name(), test.alias})
			if err != nil {
				t.Fatalf("expected alias to resolve: %v", err)
			}
			if command.Name() != test.commandName {
				t.Fatalf("expected alias to resolve to %q, got %q", test.commandName, command.Name())
			}
		})
	}
}
