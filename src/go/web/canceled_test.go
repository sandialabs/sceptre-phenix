package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/gorilla/mux"

	"phenix/api/job"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
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

// A request whose client leaves while its minimega commands wait for the
// shared connection gives them up, unsent, and writes nothing.
func TestReadsGiveUpWhenTheClientLeaves(t *testing.T) {
	tests := map[string]struct {
		handler http.HandlerFunc
		path    string
		vars    map[string]string
		role    rbac.Role
	}{
		"vm": {
			handler: GetVM, path: "/experiments/exp/vms/a",
			vars: map[string]string{"exp": "exp", "name": "a"}, role: testRole("vms", "get", "exp/a"),
		},
		"vm snapshots": {
			handler: GetVMSnapshots, path: "/experiments/exp/vms/a/snapshots",
			vars: map[string]string{"exp": "exp", "name": "a"}, role: testRole("vms/snapshots", "list", "exp/a"),
		},
		"state of health": {
			handler: GetExperimentSoH, path: "/experiments/exp/soh",
			vars: map[string]string{"name": "exp"}, role: testRole("experiments", "get", "exp"),
		},
		"topology": {
			handler: GetExperimentTopology, path: "/experiments/exp/topology",
			vars: map[string]string{"name": "exp"}, role: testRole("experiments/topology", "get", "exp"),
		},
		"cluster hosts": {
			handler: GetClusterHosts, path: "/hosts", role: testRole("hosts", "list", "*"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

			received := holdMinimega(t)

			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), middleware.ContextKeyRole, tc.role))

			req := httptest.NewRequestWithContext(ctx, http.MethodGet, tc.path, nil)
			req = mux.SetURLVars(req, tc.vars)

			var (
				rec  = httptest.NewRecorder()
				done = make(chan struct{})
			)

			go func() {
				defer close(done)

				tc.handler(rec, req)
			}()

			cancel()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the handler kept waiting for minimega after its client left")
			}

			if rec.Body.Len() != 0 {
				t.Errorf("the handler wrote %q (status %d) to a client that left", rec.Body.String(), rec.Code)
			}

			// leaving out the files directory lookup, if this test made it
			got := slices.DeleteFunc(mmtest.Bases(received()), func(base string) bool { return base == "args" })
			if !slices.Equal(got, []string{"hold"}) {
				t.Errorf("minimega received %q, want only the hold", got)
			}
		})
	}
}

// A bulk action job canceled while its VM's minimega command waits for the
// shared connection gives the command up, unsent, and reports the VM canceled.
func TestVMActionsJobGivesUpWhenCanceled(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

	var (
		received = holdMinimega(t)
		registry = useJobs(t)
		server   = serveAs(t, "alice", testRole("vms/start", "update", "exp/a")).URL
	)

	resp := postVMActions(t, server, "start", "a")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	polltest.Until(t, "the VM's action starts", func() bool {
		j, ok := registry.Get(started.ID)

		return ok && j.Items[0].Status == job.ItemRunning
	})

	if _, err := registry.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled || j.Items[0].Status != job.ItemCanceled {
		t.Errorf("job %s with item %s, want both canceled", j.Status, j.Items[0].Status)
	}

	if n := mmtest.Count(received(), "vm start"); n != 0 {
		t.Errorf("minimega received %d vm start commands, want none", n)
	}
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
				t.Fatalf("runVMAction = %v, want %v", err, context.Canceled)
			}

			if got := broadcasts(); !slices.Equal(got, want) {
				t.Errorf("broadcast %q, want %q", got, want)
			}
		})
	}
}
