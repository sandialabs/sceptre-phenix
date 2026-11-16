package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	dryRunFlagName  = "dry-run"
	dryRunFlagUsage = "Show what the command would change, and change nothing"
)

// dryRun is the --dry-run flag of a command that deletes or changes things.
//
// A command adds it with [addDryRunFlag]. Before each change, the command
// calls [dryRun.skip]. In a dry run, skip writes one line, such as "would
// delete experiment foo", and the command goes to the next item without the
// change. The command still reads and checks what it would change, so a dry
// run gives the same errors as a real run, and exits 0 when there are none.
type dryRun struct {
	cmd *cobra.Command
}

// addDryRunFlag adds --dry-run to cmd. Do not use it for a command that has
// a --dry-run flag with another meaning, such as phenix experiment start,
// phenix image build and phenix workflow apply (-n/--dry-run).
func addDryRunFlag(cmd *cobra.Command) dryRun {
	cmd.Flags().Bool(dryRunFlagName, false, dryRunFlagUsage)

	return dryRun{cmd: cmd}
}

// enabled reports whether the command line has --dry-run.
func (d dryRun) enabled() bool {
	return mustGet(d.cmd.Flags().GetBool, dryRunFlagName)
}

// skip reports whether the command must not make the change: action, such as
// "delete", on item, such as "experiment foo". In a dry run, it also writes
// "would <action> <item>" to the command's output.
func (d dryRun) skip(action, item string) bool {
	if !d.enabled() {
		return false
	}

	fmt.Fprintf(d.cmd.OutOrStdout(), "would %s %s\n", action, item)

	return true
}
