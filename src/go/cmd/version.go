package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"phenix/version"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (commit %s) %s\n", version.Label(), version.Commit, version.Date)

			return nil
		},
	}

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newVersionCmd(), true)
}
