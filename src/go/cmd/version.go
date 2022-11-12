package cmd

import (
	"fmt"

	"phenix/version"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s (commit %s) %s\n", version.Tag, version.Commit, version.Date)
			return nil
		},
	}

	return cmd
}

func init() {
	rootCmd.AddCommand(newVersionCmd())
}
