package forward

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"phenix/api/experiment"
	"phenix/util/mm"
	ft "phenix/web/forward/forwardtypes"
)

// within fails the test if fn does not return within 2 seconds.
func within(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s took longer than 2s", what)
	}
}

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

	within(t, "the reaper asking minimega", func() { <-fake.entered })

	within(t, "listing the other experiment's forwards", func() {
		if got := listForwards(t, "b", "vm"); len(got) != 1 || got[0] != qemuB.ToKey() {
			t.Errorf("forwards = %v, want %s", got, qemuB.ToKey())
		}
	})

	within(t, "stopping the other experiment", func() { dropExperimentForwards("b") })

	fake.release()

	within(t, "the reaper finishing", func() { <-reaped })

	// minimega listed no tunnels for a, so the reaper dropped its forward
	if got := reapAll(); len(got) != 0 {
		t.Errorf("forwards left = %v, want none", got)
	}
}

// A forward gives up after trying maxTunnelPortAttempts busy cluster ports.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestCreateTunnelGivesUpAfterBoundedPorts(t *testing.T) {
	fake := useGatedForwards(t, []string{"exp"})
	fake.createErr = errors.New("listen tcp :50001: bind: address already in use")
	fake.release()

	within(t, "creating the forward", func() {
		if err := createPortForward("exp", "vm", "0", "", "22", "u"); err == nil {
			t.Error("created a forward with every port busy")
		}
	})

	if n := fake.creates.Load(); n != maxTunnelPortAttempts {
		t.Errorf("tried %d ports, want %d", n, maxTunnelPortAttempts)
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	if len(forwards) != 0 || len(pending) != 0 {
		t.Errorf("forwards = %v, pending = %v; want none", forwards, pending)
	}
}

// An experiment stopping while one of its forwards is being created drops
// the forward. Its tunnel went with the experiment's VMs, and closing it would
// recreate the experiment's minimega namespace.
//
//nolint:paralleltest // replaces the store, minimega and the forwards
func TestStopDuringCreateDropsTheForward(t *testing.T) {
	fake := useGatedForwards(t, []string{"exp"})

	created := make(chan error, 1)

	go func() { created <- createPortForward("exp", "vm", "0", "", "22", "u") }()

	within(t, "creating the tunnel", func() { <-fake.entered })
	within(t, "stopping the experiment", func() { dropExperimentForwards("exp") })

	fake.release()

	var err error

	within(t, "the create returning", func() { err = <-created })

	if !errors.Is(err, experiment.ErrExperimentNotRunning) {
		t.Errorf("create returned %v, want %v", err, experiment.ErrExperimentNotRunning)
	}

	forwardsMu.Lock()
	defer forwardsMu.Unlock()

	if len(forwards) != 0 || len(pending) != 0 {
		t.Errorf("forwards = %v, pending = %v; want none", forwards, pending)
	}

	if n := fake.closes.Load(); n != 0 {
		t.Errorf("closed %d tunnels of the stopped experiment", n)
	}
}
