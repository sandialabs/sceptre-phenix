package vm_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/util/freshness"
	"phenix/util/mm"
)

// observingMM is a minimega that also reports how its VM info was read.
type observingMM struct {
	fakeMM

	obs freshness.Observation
}

func (m *observingMM) GetVMInfoObserved(...mm.Option) (mm.VMs, freshness.Observation) {
	return m.vms, m.obs
}

func vmNames(vms []mm.VM) []string {
	names := make([]string, len(vms))
	for i, v := range vms {
		names[i] = v.Name
	}

	return names
}

// When minimega fails to answer for a running experiment, its VMs are listed
// as configured, not dropped as undeployed, and the failure is reported.
//
//nolint:paralleltest // replaces the store and minimega
func TestListForObservedKeepsConfiguredVMsWhenUnknown(t *testing.T) {
	useExperiment(t, startTime, vmNode("a"), vmNode("b"))

	exp, err := experiment.Get(testExp)
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now()
	fake := &observingMM{
		fakeMM: fakeMM{vms: mm.VMs{runningVM("a")}},
		obs:    freshness.Observation{At: at, Known: false, Err: errors.New("compute1: timed out")},
	}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	vms, obs := vm.ListForObserved(t.Context(), exp)
	if obs.Known || !obs.At.Equal(at) {
		t.Errorf("observation = %+v, want minimega's unknown one", obs)
	}

	if names := vmNames(vms); !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("VMs = %v, want a and b", names)
	}

	if !vms[0].Running || vms[1].Running || vms[1].State != "" {
		t.Errorf("a = %+v, b = %+v; want a running and b with no state", vms[0], vms[1])
	}

	// a known answer drops the VM minimega does not run, as not deployed
	fake.obs = freshness.Observed(at)

	if vms, _ := vm.ListForObserved(t.Context(), exp); !slices.Equal(vmNames(vms), []string{"a"}) {
		t.Errorf("VMs with a known answer = %v, want only a", vmNames(vms))
	}
}
