package vm

import (
	"fmt"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
)

// ResetSettings restores the settings that each VM of vmNames had when phenix
// created the stopped experiment expName: CPUs, memory, the first drive's image
// and inject partition, snapshot, do not boot, and the cluster host of the
// schedule. It reads the experiment once and saves it once, with every VM it
// can reset, and saves nothing when it can reset none.
//
// It returns, by VM name, the error of each VM it cannot reset: one wrapping
// [experiment.ErrNoVMDefaults] for a VM that the experiment saved no settings
// for, or [mm.ErrVMNotFound] for a VM that the topology does not have. Its
// second error is for the experiment as a whole, which then changes no VM; it
// wraps [experiment.ErrExperimentRunning] for a running experiment.
func ResetSettings(expName string, vmNames []string) (map[string]error, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("unable to get experiment %s: %w", expName, err)
	}

	if exp.Running() {
		return nil, fmt.Errorf("resetting the VM settings of experiment %s: %w", expName, experiment.ErrExperimentRunning)
	}

	defaults, err := experiment.VMDefaultsOf(exp.Metadata.Annotations)
	if err != nil {
		return nil, fmt.Errorf("resetting the VM settings of experiment %s: %w", expName, err)
	}

	var (
		vmErrs    = make(map[string]error)
		schedules = exp.Spec.Schedules()
		changed   bool
	)

	for _, name := range vmNames {
		var node ifaces.NodeSpec
		if topo := exp.Spec.Topology(); topo != nil {
			node = topo.FindNodeByName(name)
		}

		if node == nil {
			vmErrs[name] = &mm.VMNotFoundError{Err: fmt.Errorf("unable to find VM %s in experiment %s", name, expName)}

			continue
		}

		saved, ok := defaults[name]
		if !ok {
			vmErrs[name] = experiment.ErrNoVMDefaults

			continue
		}

		resetNode(node, saved)
		changed = true

		if saved.Host == "" {
			delete(schedules, name)
		} else {
			schedules[name] = saved.Host
		}
	}

	if !changed {
		return vmErrs, nil
	}

	exp.Spec.SetSchedule(schedules)

	err = experiment.Save(experiment.SaveWithName(expName), experiment.SaveWithSpec(exp.Spec))
	if err != nil {
		return nil, fmt.Errorf("unable to save experiment with reset VMs: %w", err)
	}

	return vmErrs, nil
}

// resetNode gives the topology node of a VM its saved settings, except its host.
func resetNode(node ifaces.NodeSpec, saved experiment.VMDefaults) {
	node.Hardware().SetVCPU(saved.CPUs)
	node.Hardware().SetMemory(saved.RAM)

	if drives := node.Hardware().Drives(); len(drives) > 0 {
		if saved.Disk != "" {
			drives[0].SetImage(saved.Disk)
		}

		drives[0].SetInjectPartition(saved.InjectPartition)
	}

	node.General().SetSnapshot(saved.Snapshot)
	node.General().SetDoNotBoot(saved.DNB)
}
