package mm

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

func TestGetVMStates(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"name", "state"}

	for name, tc := range map[string]struct {
		answer  []*minicli.Response // one per host in the mesh
		want    map[string]string
		wantErr string // substring of the error, "" for none
	}{
		"reads every host's VMs from one command": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"a", "RUNNING"}),
				mmtest.Tabular("compute1", header, []string{"b", "PAUSED"}, []string{"c", "BUILDING"}),
			},
			want: map[string]string{"a": "RUNNING", "b": "PAUSED", "c": "BUILDING"},
		},
		"reads an empty namespace": {
			answer: []*minicli.Response{mmtest.Tabular("head", header), mmtest.Tabular("compute1", header)},
			want:   map[string]string{},
		},
		"skips answers without a name and state": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", []string{"name"}, []string{"a"}),
				mmtest.Tabular("compute1", []string{"state"}, []string{"RUNNING"}),
				mmtest.Tabular("compute2", header, []string{"short"}, []string{"c", "BUILDING"}),
			},
			want: map[string]string{"c": "BUILDING"},
		},
		"names every host that cannot answer": {
			answer: []*minicli.Response{
				{Host: "head", Error: "head down"},
				{Host: "compute1", Error: "compute1 down"},
			},
			wantErr: "getting VM states in namespace exp: head down. compute1 down",
		},
		"fails when a host cannot answer": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"a", "RUNNING"}),
				{Host: "compute1", Error: "host unreachable"},
			},
			wantErr: "host unreachable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoSummaryCmd {
					return tc.answer
				}

				return nil
			})

			states, err := Minimega{}.GetVMStates(NS("exp"))

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("GetVMStates(exp) error = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("GetVMStates(exp) error = %v, want one containing %q", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, states); diff != "" {
				t.Errorf("GetVMStates(exp) mismatch (-want +got):\n%s", diff)
			}

			want := []mmtest.Command{{Namespace: "exp", Base: vmInfoSummaryCmd}}
			if diff := cmp.Diff(want, received(), ignoreRaw); diff != "" {
				t.Errorf("GetVMStates(exp) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Concurrent reads of a namespace's VM states (several pages polling the SoH summary)
// share one command, and each caller gets its own copy.
func TestGetVMStatesMergesConcurrentCalls(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const callers = 4

	release := make(chan struct{})

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case poolHold:
			<-release
		case vmInfoSummaryCmd:
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "state"}, []string{"a", "RUNNING"})}
		}

		return nil
	})

	var (
		wg      sync.WaitGroup
		results = make([]map[string]string, callers)
		errs    = make([]error, callers)
		held    = make(chan error, mmcli.ReadOnlyLimit())
	)

	// free the read-only connections even if the test fails first, so every caller finishes
	unhold := sync.OnceFunc(func() { close(release) })

	t.Cleanup(func() {
		unhold()
		wg.Wait()
	})

	// hold every read-only connection so the callers below queue up behind them
	for range mmcli.ReadOnlyLimit() {
		go func() {
			cmd := mmcli.NewCommand()
			cmd.Command = poolHold

			held <- mmcli.ErrorResponse(mmcli.RunReadOnly(context.Background(), mmcli.HeadRead, cmd))
		}()
	}

	polltest.Until(t, "every read-only connection is held", func() bool {
		return mmtest.Count(received(), poolHold) == mmcli.ReadOnlyLimit()
	})

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i], errs[i] = Minimega{}.GetVMStates(NS("exp"))
		}()
	}

	polltest.Until(t, "every caller waits for the run", func() bool { return vmStatesFlights.Waiting("exp") == callers })

	unhold()
	wg.Wait()

	for range mmcli.ReadOnlyLimit() {
		if err := <-held; err != nil {
			t.Errorf("holding command error = %v", err)
		}
	}

	if got := mmtest.Count(received(), vmInfoSummaryCmd); got != 1 {
		t.Errorf("%d concurrent GetVMStates(exp) calls sent %d vm info summary commands, want 1", callers, got)
	}

	for i := range callers {
		if errs[i] != nil {
			t.Errorf("concurrent GetVMStates(exp) error = %v, want nil", errs[i])
		}

		if diff := cmp.Diff(map[string]string{"a": "RUNNING"}, results[i]); diff != "" {
			t.Errorf("concurrent GetVMStates(exp) mismatch (-want +got):\n%s", diff)
		}
	}

	results[0]["a"] = "changed"

	if got := results[1]["a"]; got != "RUNNING" {
		t.Errorf("another caller's state of a after one changed its result = %q, want RUNNING", got)
	}
}
