package forward

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/polltest"
	ft "phenix/web/forward/forwardtypes"
)

// gatedMM is a minimega whose tunnel commands wait on a gate, telling entered
// when one starts.
type gatedMM struct {
	mm.MM

	entered chan struct{}
	gate    chan struct{}
	release func()

	// what CreateTunnel returns once let through
	createErr error
	creates   atomic.Int32
	closes    atomic.Int32
}

func newGatedMM(t *testing.T) *gatedMM {
	t.Helper()

	m := &gatedMM{entered: make(chan struct{}, 64), gate: make(chan struct{})}
	m.release = sync.OnceFunc(func() { close(m.gate) })

	t.Cleanup(m.release)

	return m
}

func (m *gatedMM) wait() {
	m.entered <- struct{}{}
	<-m.gate
}

func (m *gatedMM) GetTunnels(...mm.Option) []map[string]string {
	m.wait()

	return nil
}

func (m *gatedMM) GetVMHost(...mm.Option) (string, error) { return "node1", nil }

func (m *gatedMM) CreateTunnel(...mm.Option) error {
	m.creates.Add(1)
	m.wait()

	return m.createErr
}

func (m *gatedMM) CloseTunnel(...mm.Option) error {
	m.closes.Add(1)

	return nil
}

// useGatedForwards is useForwards with a gated minimega.
func useGatedForwards(t *testing.T, running []string, forwarded ...ft.Listener) *gatedMM {
	t.Helper()

	useForwards(t, running, nil, forwarded...)

	fake := newGatedMM(t)
	mm.DefaultMM = fake //nolint:reassign // install test double; useForwards restores minimega

	return fake
}

// While the reaper waits on minimega for one experiment's tunnels, another
// experiment's forwards can be listed and its stop hook runs.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestReapingDoesNotHoldForwards(t *testing.T) {
	var (
		tunnelA = ft.Listener{Exp: "a", VM: "vm", DstHost: "127.0.0.1", DstPort: 22, Owner: "u"}
		qemuB   = ft.Listener{Exp: "b", VM: "vm", DstPort: 5900, Owner: "u", QEMU: true}
	)

	fake := useGatedForwards(t, []string{"a", "b"}, tunnelA, qemuB)

	reaped := make(chan struct{})

	go func() {
		defer close(reaped)

		reapForwards()
	}()

	polltest.Within(t, "the reaper asking minimega", func() { <-fake.entered })

	polltest.Within(t, "listing the other experiment's forwards", func() {
		if diff := cmp.Diff([]string{qemuB.ToKey()}, listForwards(t, "b", "vm")); diff != "" {
			t.Errorf("GET /experiments/b/vms/vm/forwards while reaping mismatch (-want +got):\n%s", diff)
		}
	})

	polltest.Within(t, "stopping the other experiment", func() { dropExperimentForwards("b") })

	fake.release()

	polltest.Within(t, "the reaper finishing", func() { <-reaped })

	// minimega listed no tunnels for a, so the reaper dropped its forward
	if got := reapAll(); len(got) != 0 {
		t.Errorf("reapForwards() left %v, want none", got)
	}
}

// A forward gives up after trying maxTunnelPortAttempts busy cluster ports.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestCreateTunnelGivesUpAfterBoundedPorts(t *testing.T) {
	fake := useGatedForwards(t, []string{"exp"})
	fake.createErr = errors.New("listen tcp :50001: bind: address already in use")
	fake.release()

	polltest.Within(t, "creating the forward", func() {
		if err := createPortForward(t.Context(), "exp", "vm", "0", "", "22", "u"); err == nil {
			t.Error("createPortForward(exp, vm, 0, \"\", 22, u) with every port busy = nil, want an error")
		}
	})

	if n := fake.creates.Load(); n != maxTunnelPortAttempts {
		t.Errorf("createPortForward with every port busy tried %d ports, want %d", n, maxTunnelPortAttempts)
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	if len(forwards) != 0 || len(pending) != 0 {
		t.Errorf("after a failed createPortForward, forwards = %v, pending = %v; want none", forwards, pending)
	}
}

// Stopping an experiment during a forward's creation drops it; closing its
// tunnel would recreate the namespace.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestStopDuringCreateDropsTheForward(t *testing.T) {
	fake := useGatedForwards(t, []string{"exp"})

	created := make(chan error, 1)

	go func() { created <- createPortForward(t.Context(), "exp", "vm", "0", "", "22", "u") }()

	polltest.Within(t, "creating the tunnel", func() { <-fake.entered })
	polltest.Within(t, "stopping the experiment", func() { dropExperimentForwards("exp") })

	fake.release()

	var err error

	polltest.Within(t, "the create returning", func() { err = <-created })

	if !errors.Is(err, experiment.ErrExperimentNotRunning) {
		t.Errorf("createPortForward(exp, ...) stopped midway = %v, want %v", err, experiment.ErrExperimentNotRunning)
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	if len(forwards) != 0 || len(pending) != 0 {
		t.Errorf("after createPortForward of a stopped experiment, forwards = %v, pending = %v; want none", forwards, pending)
	}

	if n := fake.closes.Load(); n != 0 {
		t.Errorf("createPortForward of a stopped experiment closed %d tunnels, want 0", n)
	}
}
