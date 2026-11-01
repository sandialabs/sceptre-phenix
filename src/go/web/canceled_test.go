package web

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	bt "phenix/web/broker/brokertypes"
)

// holdMinimega points the real mm package at fake minimega and holds the shared connection with a "hold" command
// until the test ends, so every other command waits; it returns the commands minimega received.
func holdMinimega(t *testing.T) func() []mmtest.Command {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	release := make(chan struct{})

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-release
		}

		return nil
	})

	// look up minimega's files directory before the hold, as whoever needs it first does so whatever its context;
	// answered with nothing, it falls back to <PhenixBase>/images
	_ = mm.GetMMFullPath("")

	held := make(chan error, 1)

	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() { <-held })
	t.Cleanup(sync.OnceFunc(func() { close(release) }))

	mmtest.Await(t, "hold")

	return received
}

// recordVMBroadcasts records the actions of the VM broadcasts sent for the
// rest of the test.
func recordVMBroadcasts(t *testing.T) func() []string {
	t.Helper()

	var (
		mu      sync.Mutex
		actions []string
	)

	original := vmBroadcast
	t.Cleanup(func() { vmBroadcast = original })

	vmBroadcast = func(_ *bt.RequestPolicy, resource *bt.Resource, _ json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()

		actions = append(actions, resource.Action)
	}

	return func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(actions)
	}
}

// A VM action whose minimega commands gave up because its request or job
// ended reports no failure to other clients, and withdraws the transition it
// announced.
func TestVMActionGivingUpWithdrawsItsTransition(t *testing.T) {
	for action, want := range map[string][]string{
		"start":    {"starting", "canceled"},
		"restart":  {"restarting", "canceled"},
		"shutdown": nil,
	} {
		t.Run(action, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
			holdMinimega(t)

			var (
				broadcasts  = recordVMBroadcasts(t)
				a           = vmActions[action]
				ctx, cancel = context.WithCancel(t.Context())
				done        = make(chan error, 1)
			)

			go func() {
				_, err := runVMAction(vmRequest{
					ctx: ctx, role: testRole(a.resource, a.verb, "exp/a"), user: "alice", exp: "exp", name: "a", jobID: "",
				}, a)
				done <- err
			}()

			mmtest.Await(t, "hold")
			polltest.Until(t, "the action waits for minimega", func() bool {
				return len(broadcasts()) == min(len(want), 1)
			})

			cancel()

			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("runVMAction(%s) with its context canceled = %v, want %v", action, err, context.Canceled)
			}

			if diff := cmp.Diff(want, broadcasts(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("runVMAction(%s) broadcasts mismatch (-want +got):\n%s", action, diff)
			}
		})
	}
}
