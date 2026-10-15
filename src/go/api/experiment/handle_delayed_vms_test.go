package experiment

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/hashicorp/go-multierror"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// delayedMM is a test double for mm.MM answering ActiveC2Clients from a set
// of active clients that tests change between checks, and counting
// ClearNamespace calls. Any other method panics through the nil embedded
// interface.
type delayedMM struct {
	mm.MM

	mu      sync.Mutex
	active  map[mm.C2ClientRef]bool
	checks  [][]mm.C2ClientRef
	cleared int
}

func (m *delayedMM) ActiveC2Clients(_ string, refs []mm.C2ClientRef) map[mm.C2ClientRef]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checks = append(m.checks, slices.Clone(refs))

	out := make(map[mm.C2ClientRef]bool, len(refs))

	for _, ref := range refs {
		out[ref] = m.active[ref]
	}

	return out
}

func (m *delayedMM) ClearNamespace(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleared++

	return nil
}

func (m *delayedMM) activate(refs ...mm.C2ClientRef) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ref := range refs {
		m.active[ref] = true
	}
}

func (m *delayedMM) checked() [][]mm.C2ClientRef {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.checks)
}

func (m *delayedMM) clears() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.cleared
}

// useDelayedMM installs a delayedMM, with no client active, and a fake
// minimega that fails `vm start` for the VMs in fail. It returns the double
// and a function listing the commands minimega was sent.
func useDelayedMM(t *testing.T, fail ...string) (*delayedMM, func() []mmtest.Command) {
	t.Helper()

	fake := &delayedMM{active: make(map[mm.C2ClientRef]bool)}

	originalMM, originalInterval := mm.DefaultMM, c2CheckInterval

	mm.DefaultMM = fake //nolint:reassign // install test double
	c2CheckInterval = 10 * time.Millisecond

	t.Cleanup(func() {
		mm.DefaultMM = originalMM //nolint:reassign // restore default
		c2CheckInterval = originalInterval
	})

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if slices.Contains(fail, strings.TrimPrefix(cmd.Base, "vm start ")) {
			return []*minicli.Response{{Host: "head", Error: "unable to start: boom"}}
		}

		return nil
	})

	return fake, received
}

func TestHandleDelayedVMsStartsC2DelayedVMsOnceTheirClientsAreActive(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake, received := useDelayedMM(t, "late")

	var (
		byName = mm.C2ClientRef{VM: "server", ByUUID: false}
		byUUID = mm.C2ClientRef{VM: "server", ByUUID: true}
		other  = mm.C2ClientRef{VM: "other", ByUUID: false}
		solo   = mm.C2ClientRef{VM: "solo", ByUUID: false}
	)

	c2s := map[string]map[string]bool{
		"client1": {"solo": false},
		"client2": {"server": false, "other": false},
		"late":    {"server": true},
	}

	done := make(chan error, 1)

	go func() { done <- handleDelayedVMs(context.Background(), "exp", nil, c2s) }()

	// Nothing starts while no client is active, and each check asks about
	// every waited-on client once.
	polltest.Until(t, "the clients were checked twice", func() bool { return len(fake.checked()) >= 2 })

	if got := received(); len(got) != 0 {
		t.Fatalf("sent %q before any client was active", mmtest.Bases(got))
	}

	want := []mm.C2ClientRef{other, byName, byUUID, solo}
	if got := fake.checked()[0]; !slices.Equal(got, want) {
		t.Fatalf("checked %v, want %v", got, want)
	}

	fake.activate(solo, byName)

	mmtest.Await(t, "vm start client1")

	if mmtest.Count(received(), "vm start client2") != 0 {
		t.Fatal("client2 started before all the clients it waits on were active")
	}

	checksBefore := len(fake.checked())

	fake.activate(other, byUUID)

	var err error

	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleDelayedVMs did not return once every client was active")
	}

	sent := received()

	if got := mmtest.Bases(sent); !slices.Equal(got, []string{"vm start client1", "vm start client2", "vm start late"}) {
		t.Fatalf("sent %q, want each delayed VM started once", got)
	}

	for _, cmd := range sent {
		if cmd.Namespace != "exp" {
			t.Errorf("%q sent in namespace %q, want exp", cmd.Base, cmd.Namespace)
		}
	}

	// A VM that fails to start is reported, and the experiment's VMs are
	// killed.
	var (
		merr    *multierror.Error
		delayed DelayedVMError
	)

	if !errors.As(err, &merr) || len(merr.Errors) != 1 || !errors.As(merr.Errors[0], &delayed) || delayed.VM != "late" {
		t.Fatalf("error = %v, want late's start error", err)
	}

	if n := fake.clears(); n != 1 {
		t.Fatalf("cleared the namespace %d times, want once", n)
	}

	// Checks after client1 started leave out what only it waited on.
	checks := fake.checked()
	if last := checks[len(checks)-1]; len(checks) <= checksBefore || !slices.Equal(last, []mm.C2ClientRef{other, byName, byUUID}) {
		t.Fatalf("last check asked about %v, want solo left out", last)
	}
}

// Stopping an experiment cancels its start, and the stop tears down the
// namespace, so the delayed VMs' wait must leave it alone.
func TestHandleDelayedVMsLeavesNamespaceWhenCanceled(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake, received := useDelayedMM(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A delay this long hangs the test rather than passing if the
	// cancellation were missed.
	err := handleDelayedVMs(
		ctx,
		"exp",
		map[string]time.Duration{"timed": time.Minute},
		map[string]map[string]bool{"a": {"server": false}, "b": {"server": true}},
	)

	var merr *multierror.Error
	if !errors.As(err, &merr) || len(merr.Errors) != 3 {
		t.Fatalf("error = %v, want one for each waiting VM", err)
	}

	for _, err := range merr.Errors {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want context.Canceled", err)
		}
	}

	if got := received(); len(got) != 0 {
		t.Fatalf("sent %q after the start was canceled", mmtest.Bases(got))
	}

	if n := fake.clears(); n != 0 {
		t.Fatalf("cleared the namespace %d times, want none", n)
	}
}

func TestHandleDelayedVMsWithNothingDelayed(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake, received := useDelayedMM(t)

	if err := handleDelayedVMs(context.Background(), "exp", nil, nil); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if len(received()) != 0 || fake.clears() != 0 {
		t.Fatalf("sent %q and cleared the namespace %d times, want nothing", mmtest.Bases(received()), fake.clears())
	}
}
