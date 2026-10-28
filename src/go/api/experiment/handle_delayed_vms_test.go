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
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/go-multierror"
	"pgregory.net/rapid"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// delayedMM answers ActiveC2Clients from a set tests change, and counts ClearNamespace calls; other methods panic.
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

// useDelayedMM installs a delayedMM and a fake minimega failing vm start for the VMs in fail;
// returns the double and a lister of the commands sent.
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

	go func() { done <- handleDelayedVMs(context.Background(), "exp", nil, c2s, nil) }()

	// Nothing starts while no client is active, and each check asks about
	// every waited-on client once.
	polltest.Until(t, "the clients were checked twice", func() bool { return len(fake.checked()) >= 2 })

	if got := received(); len(got) != 0 {
		t.Errorf("handleDelayedVMs() sent %q before any client was active, want nothing", mmtest.Bases(got))
	}

	want := []mm.C2ClientRef{other, byName, byUUID, solo}
	if diff := cmp.Diff(want, fake.checked()[0]); diff != "" {
		t.Errorf("handleDelayedVMs() first ActiveC2Clients refs mismatch (-want +got):\n%s", diff)
	}

	fake.activate(solo, byName)

	mmtest.Await(t, "vm start client1")

	if n := mmtest.Count(received(), "vm start client2"); n != 0 {
		t.Errorf("handleDelayedVMs() sent vm start client2 %d times before all its clients were active, want 0", n)
	}

	checksBefore := len(fake.checked())

	fake.activate(other, byUUID)

	var err error

	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleDelayedVMs() still running 5s after every client was active")
	}

	// each delayed VM started once, in the experiment's namespace
	wantSent := []mmtest.Command{
		{Namespace: "exp", Base: "vm start client1"},
		{Namespace: "exp", Base: "vm start client2"},
		{Namespace: "exp", Base: "vm start late"},
	}
	if diff := cmp.Diff(wantSent, received(), cmpopts.IgnoreFields(mmtest.Command{}, "Raw")); diff != "" {
		t.Errorf("handleDelayedVMs() commands mismatch (-want +got):\n%s", diff)
	}

	// A VM that fails to start is reported, and the experiment's VMs are
	// killed.
	var (
		merr    *multierror.Error
		delayed DelayedVMError
	)

	if !errors.As(err, &merr) || len(merr.Errors) != 1 || !errors.As(merr.Errors[0], &delayed) || delayed.VM != "late" {
		t.Errorf("handleDelayedVMs() error = %v, want one %T for late", err, delayed)
	}

	if n := fake.clears(); n != 1 {
		t.Errorf("handleDelayedVMs() ClearNamespace calls = %d, want 1", n)
	}

	// Checks after client1 started leave out what only it waited on.
	checks := fake.checked()
	if len(checks) <= checksBefore {
		t.Fatalf("handleDelayedVMs() ActiveC2Clients calls = %d, want more than the %d before client1 started", len(checks), checksBefore)
	}

	if diff := cmp.Diff([]mm.C2ClientRef{other, byName, byUUID}, checks[len(checks)-1]); diff != "" {
		t.Errorf("handleDelayedVMs() last ActiveC2Clients refs mismatch (-want +got):\n%s", diff)
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
		nil,
	)

	// one error for each waiting VM
	var merr *multierror.Error
	if !errors.As(err, &merr) || len(merr.Errors) != 3 {
		t.Errorf("handleDelayedVMs(canceled) error = %v, want a %T of 3 errors", err, merr)
	} else {
		for _, err := range merr.Errors {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("handleDelayedVMs(canceled) VM error = %v, want %v", err, context.Canceled)
			}
		}
	}

	if got := received(); len(got) != 0 {
		t.Errorf("handleDelayedVMs(canceled) sent %q, want nothing", mmtest.Bases(got))
	}

	if n := fake.clears(); n != 0 {
		t.Errorf("handleDelayedVMs(canceled) ClearNamespace calls = %d, want 0", n)
	}
}

func TestHandleDelayedVMsWithNothingDelayed(t *testing.T) { //nolint:paralleltest // replaces package globals
	fake, received := useDelayedMM(t)

	if err := handleDelayedVMs(context.Background(), "exp", nil, nil, nil); err != nil {
		t.Errorf("handleDelayedVMs(nothing delayed) error = %v, want nil", err)
	}

	if got := received(); len(got) != 0 {
		t.Errorf("handleDelayedVMs(nothing delayed) sent %q, want nothing", mmtest.Bases(got))
	}

	if n := fake.clears(); n != 0 {
		t.Errorf("handleDelayedVMs(nothing delayed) ClearNamespace calls = %d, want 0", n)
	}
}

// genDelayedPending draws C2-delayed VMs, each waiting on clients that others often share.
func genDelayedPending() *rapid.Generator[map[string]map[string]bool] {
	vm := rapid.SampledFrom([]string{"a", "b", "c", "server", "solo"})

	return rapid.MapOfN(rapid.SampledFrom([]string{"d1", "d2", "d3", "d4"}), rapid.MapOfN(vm, rapid.Bool(), 0, 4), 0, 4)
}

// waitedOnC2Clients lists each client some pending VM waits on once, and no
// other, ordered by VM with the client named by VM name before the one by UUID.
func TestWaitedOnC2ClientsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		pending := genDelayedPending().Draw(t, "pending")
		got := waitedOnC2Clients(pending)

		want := make(map[mm.C2ClientRef]bool)
		for _, others := range pending {
			for vm, byUUID := range others {
				want[mm.C2ClientRef{VM: vm, ByUUID: byUUID}] = true
			}
		}

		for i, ref := range got {
			if !want[ref] {
				t.Fatalf("waitedOnC2Clients(%v) = %v, lists %v, which no VM waits on", pending, got, ref)
			}

			delete(want, ref)

			if i > 0 && (got[i-1].VM > ref.VM || (got[i-1].VM == ref.VM && !ref.ByUUID)) {
				t.Fatalf("waitedOnC2Clients(%v) = %v, %v out of order after %v", pending, got, ref, got[i-1])
			}
		}

		if len(want) != 0 {
			t.Fatalf("waitedOnC2Clients(%v) = %v, missing %v", pending, got, want)
		}
	})
}
