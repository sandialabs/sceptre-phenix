package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/job"
	"phenix/util/common"
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

// useDiskCheck sets the experiment.disk-check mode for the rest of the test.
func useDiskCheck(t *testing.T, mode common.DiskCheck) {
	t.Helper()

	original := common.CurrentDiskCheck()
	t.Cleanup(func() { _ = common.SetDiskCheck(string(original)) })

	if err := common.SetDiskCheck(string(mode)); err != nil {
		t.Fatalf("SetDiskCheck(%s) = %v", mode, err)
	}
}

// A bulk action job canceled while its VM's minimega command waits for the
// shared connection gives the command up, unsent, and reports the VM canceled.
func TestVMActionsJobGivesUpWhenCanceled(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))
	useDiskCheck(t, common.DiskCheckOff) // the disk check reads the VM states first

	var (
		received = holdMinimega(t)
		registry = useJobs(t)
		server   = serveAs(t, "alice", testRole("vms/start", "update", "exp/a")).URL
	)

	resp := postVMActions(t, server, "start", "a")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/v1/experiments/exp/vms/actions start = %d, want %d: %s",
			resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding the started job: %v", err)
	}

	polltest.Until(t, "the VM's action starts", func() bool {
		j, ok := registry.Get(started.ID)

		return ok && j.Items[0].Status == job.ItemRunning
	})

	if _, err := registry.Cancel(started.ID); err != nil {
		t.Fatalf("Cancel(%s) = %v", started.ID, err)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled {
		t.Errorf("canceled job status = %s, want %s", j.Status, job.StatusCanceled)
	}

	if got := j.Items[0].Status; got != job.ItemCanceled {
		t.Errorf("canceled job item a status = %s, want %s", got, job.ItemCanceled)
	}

	if n := mmtest.Count(received(), "vm start"); n != 0 {
		t.Errorf("canceled job sent %d vm start commands, want none", n)
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
				t.Fatalf("runVMAction(%s) with its context canceled = %v, want %v", action, err, context.Canceled)
			}

			if diff := cmp.Diff(want, broadcasts(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("runVMAction(%s) broadcasts mismatch (-want +got):\n%s", action, diff)
			}
		})
	}
}

// A bulk start canceled while its disk check waits for the VM states on the
// shared connection gives up, sends no vm start, and reports the VM canceled.
func TestVMActionsJobGivesUpWhenCanceledInDiskCheck(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

	var (
		received = holdMinimega(t)
		registry = useJobs(t)
		server   = serveAs(t, "alice", testRole("vms/start", "update", "exp/a")).URL
	)

	resp := postVMActions(t, server, "start", "a")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/v1/experiments/exp/vms/actions start = %d, want %d: %s",
			resp.StatusCode, http.StatusAccepted, readBody(t, resp))
	}

	var started job.Job
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decoding the started job: %v", err)
	}

	polltest.Until(t, "the job runs", func() bool {
		j, ok := registry.Get(started.ID)

		return ok && j.Status == job.StatusRunning
	})

	if _, err := registry.Cancel(started.ID); err != nil {
		t.Fatalf("Cancel(%s) = %v", started.ID, err)
	}

	j := waitJob(t, registry, started.ID)
	if j.Status != job.StatusCanceled || j.Items[0].Status != job.ItemCanceled {
		t.Errorf("canceled job status, item a status = %s, %s, want %s, %s",
			j.Status, j.Items[0].Status, job.StatusCanceled, job.ItemCanceled)
	}

	if n := mmtest.Count(received(), "vm start"); n != 0 {
		t.Errorf("canceled job sent %d vm start commands, want none", n)
	}
}
