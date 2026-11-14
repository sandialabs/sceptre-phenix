package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// subcommandOf returns the command that names, such as "vm" and "capture",
// lead to from the root command. A file adds subcommands to a parent with it
// from its init function, which runs after the init function of the parent's
// file when its name sorts after that file's name. It panics when there is no
// such command, so a misplaced init fails each test of the package.
func subcommandOf(names ...string) *cobra.Command {
	cmd := rootCmd

	for _, name := range names {
		var next *cobra.Command

		for _, c := range cmd.Commands() {
			if c.Name() == name {
				next = c

				break
			}
		}

		if next == nil {
			panic(fmt.Sprintf("phenix %s: no such command", strings.Join(names, " ")))
		}

		cmd = next
	}

	return cmd
}
