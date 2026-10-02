package vm_test

import (
	"errors"
	"fmt"
	"testing"

	"phenix/store"
	"phenix/util/mm"
)

const (
	testExp = "test-experiment"

	// startTime is the start time of a running experiment.
	startTime = "2026-08-26T00:00:00Z"
)

// errAny is the wanted error of a case that expects an error of any kind.
var errAny = errors.New("any error")

// checkErr fails the test unless err is want: nil for none, errAny for any
// error, or else an error that wraps want.
func checkErr(t *testing.T, err, want error) {
	t.Helper()

	switch {
	case want == nil:
		if err != nil {
			t.Fatalf("got error %v, want none", err)
		}
	case errors.Is(want, errAny):
		if err == nil {
			t.Fatal("got no error, want one")
		}
	case !errors.Is(err, want):
		t.Fatalf("got error %v, want %v", err, want)
	}
}

// fakeMM stands in for minimega. It reports the same VMs and captures
// whichever VM a call names, and any mm.MM method it does not define panics
// through the nil embedded interface.
type fakeMM struct {
	mm.MM

	vms      mm.VMs
	captures []mm.Capture

	startErr, stopErr error

	starts, stops, connects, tagUpdates, captureListings int
}

func (m *fakeMM) GetVMInfo(...mm.Option) mm.VMs {
	return m.vms
}

func (m *fakeMM) GetVMCaptures(...mm.Option) []mm.Capture {
	return m.captures
}

func (m *fakeMM) GetExperimentCaptures(...mm.Option) []mm.Capture {
	m.captureListings++

	return m.captures
}

func (m *fakeMM) StartVMCapture(...mm.Option) error {
	m.starts++

	return m.startErr
}

func (m *fakeMM) StopVMCapture(...mm.Option) error {
	m.stops++

	return m.stopErr
}

func (m *fakeMM) ConnectVMInterface(...mm.Option) error {
	m.connects++

	return nil
}

func (m *fakeMM) SetVMTags(...mm.Option) error {
	m.tagUpdates++

	return nil
}

// useMM makes fake answer for minimega for the rest of the test.
func useMM(t *testing.T, fake *fakeMM) *fakeMM {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// runningVM is what minimega reports for a VM running on compute1.
func runningVM(name string, networks ...string) mm.VM {
	return mm.VM{
		Name: name, Host: "compute1", State: "RUNNING", Running: true, Networks: networks,
	}
}

// fakeStore holds one experiment config. Any store.Store method it does not
// define panics through the nil embedded interface.
type fakeStore struct {
	store.Store

	experiment store.Config

	gets  int
	saved []map[string]any // the spec of each update, oldest first
}

func (s *fakeStore) Get(cfg *store.Config) error {
	s.gets++

	if cfg.Kind != s.experiment.Kind || cfg.Metadata.Name != s.experiment.Metadata.Name {
		return fmt.Errorf("%s %s not found", cfg.Kind, cfg.Metadata.Name)
	}

	*cfg = s.experiment

	return nil
}

func (s *fakeStore) Update(cfg *store.Config) error {
	s.saved = append(s.saved, cfg.Spec)

	return nil
}

// useExperiment makes the store hold only the experiment testExp, with the
// given topology nodes, running since started unless that is empty. A test may
// add to the returned store's experiment spec before reading it.
func useExperiment(t *testing.T, started string, nodes ...map[string]any) *fakeStore {
	t.Helper()

	fake := &fakeStore{experiment: store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: testExp},
		Spec: map[string]any{
			"experimentName": testExp,
			"baseDir":        t.TempDir(),
			"defaultBridge":  "phenix",
			"topology":       map[string]any{"nodes": nodes},
		},
	}}

	if started != "" {
		fake.experiment.Status = map[string]any{"startTime": started}
	}

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	store.DefaultStore = fake //nolint:reassign // install test double

	return fake
}

// vmNode is the topology node of a VM that boots /images/<hostname>.qc2 and
// has the given interfaces.
func vmNode(hostname string, interfaces ...map[string]any) map[string]any {
	return map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": hostname},
		"hardware": map[string]any{
			"vcpus":   2,
			"memory":  512,
			"os_type": "linux",
			"drives":  []map[string]any{{"image": "/images/" + hostname + ".qc2"}},
		},
		"network": map[string]any{"interfaces": interfaces},
	}
}

// vmIface is a topology interface on vlan, with a static address unless
// address is empty.
func vmIface(name, vlan, address string) map[string]any {
	return map[string]any{"name": name, "vlan": vlan, "address": address}
}
