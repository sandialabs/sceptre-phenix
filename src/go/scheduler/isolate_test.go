package scheduler

import (
	"testing"

	"phenix/internal/mm"
	v1 "phenix/types/version/v1"

	"github.com/golang/mock/gomock"
)

func TestIsolateSchedulerManual(t *testing.T) {
	sched := map[string]string{
		"foo": "compute0",
	}

	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: sched,
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name:     "compute0",
				CPUs:     16,
				MemTotal: 49152,
			},
			{
				Name:     "compute1",
				CPUs:     16,
				MemTotal: 49152,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("isolate-experiment", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	for vm, host := range spec.SchedulesF {
		if host != "compute0" {
			t.Logf("expected %s -> compute0, got %s -> %s", vm, vm, host)
			t.FailNow()
		}
	}
}

func TestIsolateSchedulerFits(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: make(map[string]string),
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name:     "compute1",
				CPUs:     1,
				MemTotal: 1024,
			},
			{
				Name:     "compute2",
				CPUs:     16,
				MemTotal: 49152,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("isolate-experiment", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	for vm, host := range spec.SchedulesF {
		if host != "compute2" {
			t.Logf("expected %s -> compute2, got %s -> %s", vm, vm, host)
			t.FailNow()
		}
	}
}

func TestIsolateSchedulerUnoccupied(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: make(map[string]string),
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name:     "compute1",
				CPUs:     16,
				MemTotal: 49152,
				VMs:      1,
			},
			{
				Name:     "compute2",
				CPUs:     16,
				MemTotal: 49152,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("isolate-experiment", spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	for vm, host := range spec.SchedulesF {
		if host != "compute2" {
			t.Logf("expected %s -> compute2, got %s -> %s", vm, vm, host)
			t.FailNow()
		}
	}
}

func TestIsolateSchedulerAllOccupied(t *testing.T) {
	spec := &v1.ExperimentSpec{
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
		SchedulesF: make(map[string]string),
	}

	hosts := mm.Hosts(
		[]mm.Host{
			{
				Name:     "compute1",
				CPUs:     16,
				MemTotal: 49152,
				VMs:      1,
			},
			{
				Name:     "compute2",
				CPUs:     16,
				MemTotal: 49152,
				VMs:      1,
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := mm.NewMockMM(ctrl)
	m.EXPECT().GetClusterHosts(true).Return(hosts, nil)

	mm.DefaultMM = m

	if err := Schedule("isolate-experiment", spec); err == nil {
		t.Log("expected error")
		t.FailNow()
	}
}
