package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"phenix/store"
	"phenix/util/mm/mmcli"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
	"phenix/web/proto"
)

// A read goes on after its caller stops waiting and after the caller's context
// ends, but it has a deadline liveReadTimeout away.
func TestLiveReadsBoundsTheDetachedRead(t *testing.T) {
	t.Parallel()

	reads := newLiveReads[int]()
	reads.setWait(time.Millisecond)

	var (
		got     = make(chan context.Context, 1)
		release = make(chan struct{})
	)

	ctx, cancel := context.WithCancel(t.Context())
	start := time.Now()

	if _, ok, err := reads.get(ctx, "exp", func(ctx context.Context) int {
		got <- ctx
		<-release

		return 1
	}); ok || err != nil {
		t.Errorf("get() of a read that has not ended = %t, %v; want false, nil", ok, err)
	}

	cancel()

	readCtx := <-got

	if err := readCtx.Err(); err != nil {
		t.Errorf("the read's context after the caller's ended has error %v, want none", err)
	}

	deadline, ok := readCtx.Deadline()
	if !ok || deadline.Before(start) || deadline.After(time.Now().Add(liveReadTimeout)) {
		t.Errorf("the read's context deadline = %v, %t; want one about %v away", deadline, ok, liveReadTimeout)
	}

	close(release)
	polltest.Until(t, "the read ends", func() bool { return reads.running() == 0 })
}

// A read that panics gives its callers errLiveReadPanic, is logged, and frees
// its key for the next read.
//
//nolint:paralleltest // captures the process-wide log
func TestLiveReadsRecoversAPanic(t *testing.T) {
	logs := plogtest.Capture(t)
	reads := newLiveReads[int]()

	_, ok, err := reads.get(t.Context(), "exp", func(context.Context) int { panic("boom") })
	if !ok || !errors.Is(err, errLiveReadPanic) {
		t.Errorf("get() of a read that panics = %t, %v; want true, %v", ok, err, errLiveReadPanic)
	}

	if n := reads.running(); n != 0 {
		t.Errorf("reads running after a panic = %d, want 0", n)
	}

	got, ok, err := reads.get(t.Context(), "exp", func(context.Context) int { return 2 })
	if got != 2 || !ok || err != nil {
		t.Errorf("get() after a read panicked = %d, %t, %v; want 2, true, nil", got, ok, err)
	}

	panics := logs.Records(t, func(record map[string]any) bool {
		return record["level"] == "ERROR" && record["panic"] == "boom" && record["key"] == "exp"
	})
	if len(panics) != 1 {
		t.Errorf("logged %d errors for the panic, want 1", len(panics))
	}
}

// stalledLists is how many running experiments the shared wait tests list:
// more than one batch of mmcli.MinReadOnlyLimit reads.
const stalledLists = 4 * mmcli.MinReadOnlyLimit

// stalledListWait is how long the shared wait tests let a list wait.
const stalledListWait = 300 * time.Millisecond

// useStalledLists stores stalledLists running experiments, makes minimega not
// answer about their VMs until the test ends, and lowers the read limit so the
// reads run in batches. It returns the experiments' names.
func useStalledLists(t *testing.T, config func(name string) store.Config) []string {
	t.Helper()

	names := make([]string, stalledLists)
	configs := make([]store.Config, stalledLists)

	for i := range names {
		names[i] = fmt.Sprintf("stalled-%02d", i)
		configs[i] = config(names[i])

		lastVMStates.forget(names[i])
	}

	useTestStore(t, configs...)

	// LIFO: the reads are let go before they are waited for
	fake := useTestMM(t, &testMM{})
	useLiveReadWait(t, stalledListWait)

	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })

	t.Cleanup(release)
	fake.vmGate.Store(&gate)
	fake.stateGate.Store(&gate)

	limit := mmcli.ReadOnlyLimit()
	mmcli.SetReadOnlyLimit(mmcli.MinReadOnlyLimit)
	t.Cleanup(func() { mmcli.SetReadOnlyLimit(limit) })

	return names
}

// With more stalled experiments than the read limit, the experiment list
// waits one wait in all, not one for each batch, and lists their VMs unknown.
func TestExperimentListWaitsOnceForStalledExperiments(t *testing.T) {
	names := useStalledLists(t, func(name string) store.Config {
		return namedExperiment(t, name, "2024-01-01T00:00:00Z", map[string]any{
			"type":     "VirtualMachine",
			"general":  map[string]any{"hostname": "vm-" + name},
			"hardware": map[string]any{"os_type": "linux"},
		})
	})

	server := serveAs(t, "alice", combinedRole(testRole("experiments", "list", "*"), testRole("vms", "list", "*"))).URL
	start := time.Now()

	var list proto.ExperimentList
	if code := getAPI(t, server+"/api/v1/experiments", &list); code != http.StatusOK {
		t.Fatalf("GET /api/v1/experiments = %d, want %d", code, http.StatusOK)
	}

	if took := time.Since(start); took > 2*stalledListWait {
		t.Errorf("GET /api/v1/experiments with %d stalled experiments took %v, want about %v",
			len(names), took, stalledListWait)
	}

	if n := len(list.GetExperiments()); n != len(names) {
		t.Fatalf("GET /api/v1/experiments listed %d experiments, want %d", n, len(names))
	}

	for _, exp := range list.GetExperiments() {
		if !exp.GetVmsUnknown() || len(exp.GetVms()) != 1 {
			t.Errorf("GET /api/v1/experiments %s: vms unknown %t, %d VMs; want unknown, 1 VM",
				exp.GetName(), exp.GetVmsUnknown(), len(exp.GetVms()))
		}
	}
}

// With more stalled experiments than the read limit, the state of health
// summary waits one wait in all, and gives their VM counts unknown, not an error.
func TestGetSoHSummaryWaitsOnceForStalledExperiments(t *testing.T) {
	names := useStalledLists(t, func(name string) store.Config { return sohSummaryExperiment(name, true, "soh") })

	start := time.Now()

	_, sums := getSohSummary(t, sohSummaryRole("*"))

	if took := time.Since(start); took > 2*stalledListWait {
		t.Errorf("GET /api/v1/soh with %d stalled experiments took %v, want about %v", len(names), took, stalledListWait)
	}

	if len(sums) != len(names) {
		t.Fatalf("GET /api/v1/soh gave %d summaries, want %d", len(sums), len(names))
	}

	for _, sum := range sums {
		if sum.VMs.Known || sum.VMsError != "" {
			t.Errorf("GET /api/v1/soh %s: VM counts %+v, error %q; want unknown, no error",
				sum.Name, sum.VMs, sum.VMsError)
		}
	}
}
