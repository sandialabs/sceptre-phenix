package vm

import (
	"cmp"
	"context"
	"slices"

	"phenix/api/experiment"
	"phenix/util/mm"
)

// ExperimentCaptures returns the running packet captures of every VM of the experiment expName, sorted by VM name
// and interface index. A stopped experiment has none, and minimega is not asked: asking it would recreate the
// experiment's namespace. Once ctx is done, the minimega command gives up (see mm.Context).
func ExperimentCaptures(ctx context.Context, expName string) []mm.Capture {
	if !experiment.Running(expName) {
		return []mm.Capture{}
	}

	return sortedCaptures(mm.GetExperimentCaptures(mm.NS(expName), mm.Context(ctx)))
}

// Captures returns the running packet captures of the VM vmName of the experiment expName, sorted by interface index.
// A stopped experiment has none, as ExperimentCaptures says.
func Captures(ctx context.Context, expName, vmName string) []mm.Capture {
	if !experiment.Running(expName) {
		return []mm.Capture{}
	}

	return sortedCaptures(mm.GetVMCaptures(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx)))
}

// sortedCaptures sorts captures by VM name, then interface index, then file path, and returns them; never nil.
func sortedCaptures(captures []mm.Capture) []mm.Capture {
	if captures == nil {
		return []mm.Capture{}
	}

	captures = slices.Clone(captures)

	slices.SortFunc(captures, func(a, b mm.Capture) int {
		return cmp.Or(cmp.Compare(a.VM, b.VM), cmp.Compare(a.Interface, b.Interface), cmp.Compare(a.Filepath, b.Filepath))
	})

	return captures
}
