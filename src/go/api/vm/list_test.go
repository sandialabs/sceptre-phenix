package vm_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
		t.Fatalf("experiment.Get(%q) error = %v", testExp, err)
	}

	at := time.Now()
	unknown := freshness.Observation{At: at, Known: false, Err: errors.New("compute1: timed out")}
	fake := &observingMM{fakeMM: fakeMM{vms: mm.VMs{runningVM("a")}}, obs: unknown}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	vms, obs := vm.ListForObserved(t.Context(), exp)
	if diff := cmp.Diff(unknown, obs, cmpopts.EquateErrors()); diff != "" {
		t.Errorf("ListForObserved(%s) observation mismatch (-want +got):\n%s", testExp, diff)
	}

	if diff := cmp.Diff([]string{"a", "b"}, vmNames(vms)); diff != "" {
		t.Fatalf("ListForObserved(%s) VM names mismatch (-want +got):\n%s", testExp, diff)
	}

	if !vms[0].Running {
		t.Errorf("ListForObserved(%s) a.Running = false, want true", testExp)
	}

	if vms[1].Running || vms[1].State != "" {
		t.Errorf("ListForObserved(%s) b Running, State = %t, %q; want false, \"\"", testExp, vms[1].Running, vms[1].State)
	}

	// a known answer drops the VM minimega does not run, as not deployed
	fake.obs = freshness.Observed(at)

	vms, _ = vm.ListForObserved(t.Context(), exp)
	if diff := cmp.Diff([]string{"a"}, vmNames(vms)); diff != "" {
		t.Errorf("ListForObserved(%s) with a known answer VM names mismatch (-want +got):\n%s", testExp, diff)
	}
}
