package app

import (
	"context"
	"fmt"
	"sync"

	"phenix/types"
	"phenix/util/mm"
)

type stageRuntimeKey struct{}

// stageRuntime is the cluster host and VM details handed to the external apps
// of one ApplyApps run. It is read from minimega for the first of them and
// reused for the rest, rather than costing a `host` and a `vm info` on every
// cluster node per app.
type stageRuntime struct {
	mu    sync.Mutex
	read  bool
	hosts mm.Hosts
	vms   mm.VMs
}

// PopulateRuntime adds current minimega host and VM details to an experiment.
func PopulateRuntime(exp *types.Experiment) error {
	hosts, vms, err := readRuntime(exp)
	if err != nil {
		return err
	}

	exp.Hosts = hosts
	exp.VMs = vms

	return nil
}

// withStageRuntime returns a context whose external apps share one reading of
// the runtime details (see populateStageRuntime).
func withStageRuntime(ctx context.Context) context.Context {
	return context.WithValue(ctx, stageRuntimeKey{}, new(stageRuntime))
}

// populateStageRuntime is PopulateRuntime, reusing the details already read
// for an earlier app of the same ApplyApps run, if any.
func populateStageRuntime(ctx context.Context, exp *types.Experiment) error {
	rt, ok := ctx.Value(stageRuntimeKey{}).(*stageRuntime)
	if !ok {
		return PopulateRuntime(exp)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	if !rt.read {
		hosts, vms, err := readRuntime(exp)
		if err != nil {
			return err
		}

		rt.hosts, rt.vms, rt.read = hosts, vms, true
	}

	exp.Hosts = rt.hosts
	exp.VMs = rt.vms

	return nil
}

func readRuntime(exp *types.Experiment) (mm.Hosts, mm.VMs, error) {
	cluster, err := mm.GetClusterHosts(true)
	if err != nil {
		return nil, nil, fmt.Errorf("getting cluster hosts: %w", err)
	}

	return cluster, mm.GetVMInfo(mm.NS(exp.Spec.ExperimentName())), nil
}
