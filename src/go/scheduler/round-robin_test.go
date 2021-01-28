package scheduler

import (
	"testing"

	"phenix/internal/mm"
	v1 "phenix/types/version/v1"

	"github.com/golang/mock/gomock"
)

func TestRoundRobinSchedulerNoVMs(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: make(map[string]string),
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name: "compute0",
				VMs:  0,
			},
			{
				Name: "compute1",
				VMs:  0,
			},
			{
				Name: "compute2",
				VMs:  0,
			},
			{
				Name: "compute3",
				VMs:  0,
			},
			{
				Name: "compute4",
				VMs:  0,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("round-robin", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	expected := map[string]string{
		"foo":   "compute0",
		"bar":   "compute1",
		"sucka": "compute2",
		"fish":  "compute3",
	}

	if len(spec.SchedulesF) != len(expected) {
		t.Logf("expected %d VMs to be scheduled, got %d", len(expected), len(spec.SchedulesF))
		t.FailNow()
	}

	for vm, host := range expected {
		if spec.SchedulesF[vm] != host {
			t.Logf("expected %s -> %s, got %s -> %s", vm, host, vm, spec.SchedulesF[vm])
			t.FailNow()
		}
	}
}

func TestRoundRobinSchedulerSomeVMs(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: make(map[string]string),
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name: "compute0",
				VMs:  0,
			},
			{
				Name: "compute1",
				VMs:  3,
			},
			{
				Name: "compute2",
				VMs:  2,
			},
			{
				Name: "compute3",
				VMs:  0,
			},
			{
				Name: "compute4",
				VMs:  0,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("round-robin", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	expected := map[string]string{
		"foo":   "compute0",
		"bar":   "compute3",
		"sucka": "compute4",
		"fish":  "compute4",
	}

	if len(spec.SchedulesF) != len(expected) {
		t.Logf("expected %d VMs to be scheduled, got %d", len(expected), len(spec.SchedulesF))
		t.FailNow()
	}

	for vm, host := range expected {
		if spec.SchedulesF[vm] != host {
			t.Logf("expected %s -> %s, got %s -> %s", vm, host, vm, spec.SchedulesF[vm])
			t.FailNow()
		}
	}
}

func TestRoundRobinSchedulerSomePrescheduled(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: map[string]string{
			"sucka": "compute0",
		},
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name: "compute0",
				VMs:  0,
			},
			{
				Name: "compute1",
				VMs:  3,
			},
			{
				Name: "compute2",
				VMs:  2,
			},
			{
				Name: "compute3",
				VMs:  0,
			},
			{
				Name: "compute4",
				VMs:  0,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("round-robin", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	expected := map[string]string{
		"foo":   "compute3",
		"bar":   "compute4",
		"sucka": "compute0",
		"fish":  "compute4",
	}

	if len(spec.SchedulesF) != len(expected) {
		t.Logf("expected %d VMs to be scheduled, got %d", len(expected), len(spec.SchedulesF))
		t.FailNow()
	}

	for vm, host := range expected {
		if spec.SchedulesF[vm] != host {
			t.Logf("expected %s -> %s, got %s -> %s", vm, host, vm, spec.SchedulesF[vm])
			t.FailNow()
		}
	}
}
