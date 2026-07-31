package app

import (
	"encoding/json"
	"testing"

	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
)

type runtimeMM struct {
	mm.MM

	hosts mm.Hosts
	vms   mm.VMs
}

func (m runtimeMM) GetClusterHosts(bool) (mm.Hosts, error) {
	return m.hosts, nil
}

func (m runtimeMM) GetVMInfo(...mm.Option) mm.VMs {
	return m.vms
}

func TestPopulateRuntimeIncludesVMTapsInExperimentJSON(t *testing.T) {
	originalMM := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = originalMM }) //nolint:reassign // restore test double

	hosts := mm.Hosts{{Name: "compute-0"}}
	vms := mm.VMs{{
		Name: "router",
		Host: "compute-0",
		Taps: []string{"mega_tap101", "mega_tap102"},
	}}
	mm.DefaultMM = runtimeMM{hosts: hosts, vms: vms} //nolint:reassign // install test double

	exp := types.NewExperiment(store.ConfigMetadata{Name: "tap-test"})
	exp.Spec.SetExperimentName("tap-test")

	if err := PopulateRuntime(exp); err != nil {
		t.Fatalf("populating experiment runtime details: %v", err)
	}

	if len(exp.Hosts) != 1 || exp.Hosts[0].Name != hosts[0].Name {
		t.Fatalf("unexpected runtime hosts: %#v", exp.Hosts)
	}

	if len(exp.VMs) != 1 || exp.VMs[0].Name != vms[0].Name {
		t.Fatalf("unexpected runtime VMs: %#v", exp.VMs)
	}

	data, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("marshaling experiment: %v", err)
	}

	var payload struct {
		VMs []struct {
			Taps []string `json:"taps"`
		} `json:"vms"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshaling experiment JSON: %v", err)
	}

	if len(payload.VMs) != 1 || len(payload.VMs[0].Taps) != 2 {
		t.Fatalf("VM taps missing from experiment JSON: %s", data)
	}
}
