package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"phenix/version"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stdout, "%s (commit %s) %s\n", version.Tag, version.Commit, version.Date)

			return nil
		},
	}

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	rootCmd.AddCommand(newVersionCmd())
}
