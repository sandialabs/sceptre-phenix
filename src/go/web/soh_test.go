package web

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/soh"
	"phenix/store"
	"phenix/types"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// sohRole may get the experiments with names and list VMs, as the state of
// health of an experiment needs.
func sohRole(names ...string) rbac.Role {
	return combinedRole(testRole("experiments", "get", names...), testRole("vms", "list", "*"))
}

func TestGetExperimentSoHNeedsExperimentGetAndVMsList(t *testing.T) {
	useTestStore(t) // holds no experiments

	tests := map[string]struct {
		role     rbac.Role
		wantCode int
	}{
		// allowed through to the lookup, which fails
		"experiment get and vms list": {
			role:     sohRole("test-experiment"),
			wantCode: http.StatusNotFound,
		},
		"other experiment": {
			role:     sohRole("other-experiment"),
			wantCode: http.StatusForbidden,
		},
		"experiment get only": {
			role:     testRole("experiments", "get", "test-experiment"),
			wantCode: http.StatusForbidden,
		},
		"vms list only": {
			role:     testRole("vms", "list", "*"),
			wantCode: http.StatusForbidden,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, "alice", tc.role).URL

			resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/soh", "")
			if resp.StatusCode != tc.wantCode {
				t.Errorf("GET /api/v1/experiments/test-experiment/soh = %d, want %d: %s", resp.StatusCode, tc.wantCode, readBody(t, resp))
			}
		})
	}
}

func sohSummaryExperiment(name string, running bool, apps ...string) store.Config {
	scenarioApps := make([]map[string]any, 0, len(apps))
	for _, a := range apps {
		scenarioApps = append(scenarioApps, map[string]any{"name": a})
	}

	status := map[string]any{}
	if running {
		status["startTime"] = "2026-09-28T14:00:00Z"
		status["apps"] = map[string]any{
			"soh": map[string]any{
				"initialized": true,
				"lastRun":     "2026-09-28T14:03:40Z",
				"hosts": []any{
					map[string]any{
						"hostname": "vm-a",
						"networking": []any{
							map[string]any{"timestamp": "2026-09-28T14:03:00Z", "error": "C2 not active"},
						},
					},
				},
			},
		}
	}

	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name, Annotations: map[string]string{"scenario": "scn"}},
		Spec: map[string]any{
			"experimentName": name,
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type":     "VirtualMachine",
						"general":  map[string]any{"hostname": "vm-a"},
						"hardware": map[string]any{"os_type": "linux"},
					},
					{
						"type":     "VirtualMachine",
						"general":  map[string]any{"hostname": "vm-b"},
						"hardware": map[string]any{"os_type": "linux"},
					},
				},
			},
			"scenario": map[string]any{"apps": scenarioApps},
		},
		Status: status,
	}
}

func getSohSummary(t *testing.T, role rbac.Role) (int, []soh.Summary) {
	t.Helper()

	resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+"/api/v1/soh", "")
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}

	var body struct {
		Experiments []soh.Summary `json:"experiments"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding GET /api/v1/soh: %v", err)
	}

	return resp.StatusCode, body.Experiments
}

func sohSummaryRole(getNames ...string) rbac.Role {
	return combinedRole(
		testRole("experiments", "list", "*"), testRole("experiments", "get", getNames...), testRole("vms", "list", "*"),
	)
}

// The summaries name down VMs, so a role that may not list VMs gets none.
func TestGetSoHSummaryNeedsVMsList(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("zeta", true, "soh"))
	useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING"}})

	role := combinedRole(testRole("experiments", "list", "*"), testRole("experiments", "get", "*"))
	if code, sums := getSohSummary(t, role); code != http.StatusForbidden || len(sums) != 0 {
		t.Errorf("GET /api/v1/soh without vms list = %d with %d summaries, want %d with none",
			code, len(sums), http.StatusForbidden)
	}
}

func TestGetSoHSummaryFiltersByExperimentGet(t *testing.T) {
	useTestStore(t,
		sohSummaryExperiment("zeta", true, "soh"),
		sohSummaryExperiment("alpha", false),
		sohSummaryExperiment("hidden", true, "soh"),
	)

	fake := useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING"}})

	code, sums := getSohSummary(t, sohSummaryRole("zeta", "alpha"))
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/soh = %d, want %d", code, http.StatusOK)
	}

	// only the running experiment the caller may get is asked about
	if n := fake.stateReads.Load(); n != 1 {
		t.Errorf("GET /api/v1/soh read minimega VM states %d times, want 1", n)
	}

	want := []soh.Summary{
		{Name: "alpha", Scenario: "scn", Status: "stopped", VMs: soh.VMCounts{Total: 2}},
		{
			Name: "zeta", Scenario: "scn", Status: "started", Running: true,
			Configured: true, ExpStarted: true, SOHInitialized: true,
			VMs:     soh.VMCounts{Known: true, Total: 2, Running: 1, Failing: 1, NotDeploy: 1},
			DownVMs: []string{"vm-b"},
			Hosts:   1, HostsWithErrors: 1, Checks: soh.Tally{Total: 1, Failing: 1},
			LastRun: "2026-09-28T14:03:40Z",
			FailingChecks: []soh.FailingCheck{
				{Host: "vm-a", Check: "network", Error: "C2 not active", Time: "2026-09-28T14:03:00Z"},
			},
		},
	}

	opts := cmp.Options{cmpopts.EquateEmpty(), cmpopts.IgnoreFields(soh.Summary{}, "VMsObservedAt")}
	if diff := cmp.Diff(want, sums, opts); diff != "" {
		t.Errorf("GET /api/v1/soh mismatch (-want +got):\n%s", diff)
	}
}

func TestGetSoHSummaryReportsMinimegaErrors(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("zeta", true, "soh"))
	useTestMM(t, &testMM{err: errors.New("minimega unreachable")})

	lastVMStates.Delete("zeta")

	_, sums := getSohSummary(t, sohSummaryRole("*"))
	if len(sums) != 1 {
		t.Fatalf("GET /api/v1/soh with minimega failing = %+v, want one summary", sums)
	}

	if sums[0].VMs.Known {
		t.Errorf("GET /api/v1/soh with minimega failing VM counts = %+v, want unknown", sums[0].VMs)
	}

	if sums[0].VMsError == "" {
		t.Error("GET /api/v1/soh with minimega failing has no vms_error, want minimega's error")
	}
}

// A truncated reply or one host's error leaves a running experiment's VM counts
// unknown with the error, rather than counting only the VMs listed.
func TestGetSoHSummaryPartialMinimegaReplies(t *testing.T) {
	header := []string{"name", "state"}

	tests := map[string]struct {
		reply   []*minicli.Response
		wantErr string
	}{
		"truncated": {
			reply:   mmtest.Truncated(mmtest.Tabular("head", header, []string{"vm-a", "RUNNING"})),
			wantErr: "server disconnected",
		},
		"host error": {
			reply: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"vm-a", "RUNNING"}),
				mmtest.Error("compute1", "namespace zeta not found"),
			},
			wantErr: "namespace zeta not found",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, sohSummaryExperiment("zeta", true, "soh"))
			useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "vm info summary" {
					return tc.reply
				}

				return nil
			})

			lastVMStates.Delete("zeta")
			t.Cleanup(func() { lastVMStates.Delete("zeta") })

			_, sums := getSohSummary(t, sohSummaryRole("*"))
			if len(sums) != 1 {
				t.Fatalf("GET /api/v1/soh = %+v, want one summary", sums)
			}

			if sums[0].VMs.Known {
				t.Errorf("GET /api/v1/soh VM counts = %+v, want unknown", sums[0].VMs)
			}

			if !strings.Contains(sums[0].VMsError, tc.wantErr) {
				t.Errorf("GET /api/v1/soh vms_error = %q, want one containing %q", sums[0].VMsError, tc.wantErr)
			}
		})
	}
}

func TestGetSoHSummaryReusesStatesWhileBusy(t *testing.T) {
	useTestStore(t,
		sohSummaryExperiment("zeta", true, "soh"),
		sohSummaryExperiment("starting", false, "soh"),
	)

	fake := useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING", "vm-b": "RUNNING"}})

	// read once while minimega is free
	_, _ = getSohSummary(t, sohSummaryRole("*"))

	if err := cache.LockExperimentForStarting("starting"); err != nil {
		t.Fatalf("cache.LockExperimentForStarting(starting) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockExperiment("starting") })

	fake.stateReads.Store(0)

	_, sums := getSohSummary(t, sohSummaryRole("*"))

	if n := fake.stateReads.Load(); n != 0 {
		t.Errorf("GET /api/v1/soh while minimega is busy read VM states %d times, want 0", n)
	}

	if len(sums) != 2 {
		t.Fatalf("GET /api/v1/soh while minimega is busy = %+v, want two summaries", sums)
	}

	if got := sums[0]; got.Name != "starting" || got.Status != "starting" {
		t.Errorf("GET /api/v1/soh first summary = %s with status %q, want starting with status starting", got.Name, got.Status)
	}

	wantVMs := soh.VMCounts{Known: true, Total: 2, Running: 2, Failing: 1}
	if diff := cmp.Diff(wantVMs, sums[1].VMs); diff != "" {
		t.Errorf("GET /api/v1/soh while busy zeta VM counts mismatch (-want +got):\n%s", diff)
	}

	// they are reported as earlier states, with when they were read
	if !sums[1].VMsStale {
		t.Error("GET /api/v1/soh while busy zeta vms_stale = false, want true")
	}

	if sums[1].VMsObservedAt == "" {
		t.Error("GET /api/v1/soh while busy zeta has no vms_observed_at, want when its states were read")
	}

	resp := do(t, http.MethodGet, serveAs(t, "alice", sohSummaryRole("*")).URL+"/api/v1/soh", "")
	if got := resp.Header.Get("X-Phenix-Stale"); got != flagTrue {
		t.Errorf("GET /api/v1/soh while busy X-Phenix-Stale = %q, want true", got)
	}

	if got, want := resp.Header.Get("X-Phenix-Observed-At"), sums[1].VMsObservedAt; got != want {
		t.Errorf("GET /api/v1/soh while busy X-Phenix-Observed-At = %q, want zeta's %q", got, want)
	}
}

// While minimega is busy and nothing was read yet, a running experiment's VM
// counts are unknown, not zero.
func TestGetSoHSummaryUnknownWhileBusyWithoutStates(t *testing.T) {
	useTestStore(t,
		sohSummaryExperiment("zeta", true, "soh"),
		sohSummaryExperiment("starting", false, "soh"),
	)
	useTestMM(t, &testMM{})

	lastVMStates.Delete("zeta")

	if err := cache.LockExperimentForStarting("starting"); err != nil {
		t.Fatalf("cache.LockExperimentForStarting(starting) error = %v", err)
	}

	t.Cleanup(func() { cache.UnlockExperiment("starting") })

	_, sums := getSohSummary(t, sohSummaryRole("*"))
	if len(sums) != 2 {
		t.Fatalf("GET /api/v1/soh while busy = %+v, want two summaries", sums)
	}

	if sums[1].VMs.Known {
		t.Errorf("GET /api/v1/soh while busy with no states read zeta VM counts = %+v, want unknown", sums[1].VMs)
	}

	if got := sums[1].VMsObservedAt; got != "" {
		t.Errorf("GET /api/v1/soh while busy with no states read zeta vms_observed_at = %q, want none", got)
	}
}

// sohBrokenApp is the running experiment name whose soh app status is not an object, as no soh app writes it.
func sohBrokenApp(name string) store.Config {
	exp := sohSummaryExperiment(name, true, "soh")
	exp.Status["apps"] = map[string]any{"soh": "broken"}

	return exp
}

// sohUnencodable is the running experiment name whose soh result for vm-a holds a number JSON cannot.
func sohUnencodable(name string) store.Config {
	exp := sohSummaryExperiment(name, true, "soh")
	exp.Status["apps"] = map[string]any{"soh": map[string]any{"hosts": []any{map[string]any{
		"hostname":   "vm-a",
		"networking": []any{map[string]any{"metadata": map[string]any{"rtt": math.NaN()}}},
	}}}}

	return exp
}

// Both soh responses report a soh app that the scenario disables and that never ran.
func TestSoHReportsDisabledApp(t *testing.T) {
	exp := sohSummaryExperiment("exp", true, "soh")
	exp.Spec["scenario"] = map[string]any{"apps": []map[string]any{{"name": "soh", "disabled": true}}}
	exp.Status["apps"] = map[string]any{}

	useTestStore(t, exp)
	useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING"}})

	resp := do(t, http.MethodGet, serveAs(t, "alice", sohRole("exp")).URL+"/api/v1/experiments/exp/soh", "")
	body := readBody(t, resp)

	var network soh.Network
	if err := json.Unmarshal([]byte(body), &network); err != nil {
		t.Fatalf("GET /api/v1/experiments/exp/soh = %d %s, not a network: %v", resp.StatusCode, body, err)
	}

	if !network.SOHDisabled || network.SOHInitialized || network.SOHRunning {
		t.Errorf("GET /api/v1/experiments/exp/soh soh_disabled, soh_initialized, soh_running = %t, %t, %t; want true, false, false",
			network.SOHDisabled, network.SOHInitialized, network.SOHRunning)
	}

	_, sums := getSohSummary(t, sohSummaryRole("*"))
	if len(sums) != 1 || !sums[0].SOHDisabled || sums[0].SOHInitialized {
		t.Errorf("GET /api/v1/soh = %+v, want one summary with soh_disabled and not soh_initialized", sums)
	}
}

// GET /experiments/{name}/soh answers the experiment's network, or a 500 for a status it cannot read.
func TestGetExperimentSoH(t *testing.T) {
	tests := map[string]struct {
		exp         store.Config
		wantCode    int
		wantStarted bool
		wantInit    bool
		wantNodes   []string // each node's label and status
		wantMessage string
	}{
		"running": {
			exp: sohSummaryExperiment("exp", true, "soh"), wantCode: http.StatusOK, wantStarted: true, wantInit: true,
			wantNodes: []string{"vm-a running"}, // minimega lacks vm-b, so it was not deployed
		},
		"stopped": {
			exp: sohSummaryExperiment("exp", false, "soh"), wantCode: http.StatusOK,
			wantNodes: []string{"vm-a notdeploy", "vm-b notdeploy"},
		},
		"an undecodable soh status": {
			exp: sohBrokenApp("exp"), wantCode: http.StatusInternalServerError,
			wantMessage: "unable to decode state of health details",
		},
		"a result JSON cannot hold": {
			exp: sohUnencodable("exp"), wantCode: http.StatusInternalServerError, wantMessage: "unsupported value: NaN",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestStore(t, tc.exp)
			useTestMM(t, &testMM{
				vms:    mm.VMs{{Name: "vm-a", State: "RUNNING", Running: true}},
				states: map[string]string{"vm-a": "RUNNING"},
			})

			resp := do(t, http.MethodGet, serveAs(t, "alice", sohRole("exp")).URL+"/api/v1/experiments/exp/soh", "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantCode {
				t.Fatalf("GET /api/v1/experiments/exp/soh = %d, want %d: %s", resp.StatusCode, tc.wantCode, body)
			}

			if tc.wantCode != http.StatusOK {
				if got := errorMessage(t, resp.StatusCode, body); !strings.Contains(got, tc.wantMessage) {
					t.Errorf("GET /api/v1/experiments/exp/soh message = %q, want it to contain %q", got, tc.wantMessage)
				}

				return
			}

			if got := resp.Header.Get("Content-Type"); got != mimeJSON {
				t.Errorf("GET /api/v1/experiments/exp/soh Content-Type = %q, want %q", got, mimeJSON)
			}

			var network soh.Network
			if err := json.Unmarshal([]byte(body), &network); err != nil {
				t.Fatalf("GET /api/v1/experiments/exp/soh = %s, not a network: %v", body, err)
			}

			if network.ExpStarted != tc.wantStarted || network.SOHInitialized != tc.wantInit {
				t.Errorf("GET /api/v1/experiments/exp/soh started, soh_initialized = %t, %t; want %t, %t",
					network.ExpStarted, network.SOHInitialized, tc.wantStarted, tc.wantInit)
			}

			var nodes []string
			for _, n := range network.Nodes {
				nodes = append(nodes, n.Label+" "+n.Status)
			}

			if diff := cmp.Diff(tc.wantNodes, nodes); diff != "" {
				t.Errorf("GET /api/v1/experiments/exp/soh nodes mismatch (-want +got):\n%s", diff)
			}

			if network.Nodes == nil || network.Edges == nil || network.Hosts == nil || network.HostFlows == nil {
				t.Errorf("GET /api/v1/experiments/exp/soh = %s, want lists, not null", body)
			}
		})
	}
}

// The summary of an experiment whose soh status cannot be read carries the error beside the others' summaries.
func TestGetSoHSummaryReportsSummaryErrors(t *testing.T) {
	useTestStore(t, sohBrokenApp("broken"), sohSummaryExperiment("fine", false))
	useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING"}})

	t.Cleanup(func() { lastVMStates.Delete("broken") })

	code, sums := getSohSummary(t, sohSummaryRole("*"))
	if code != http.StatusOK || len(sums) != 2 {
		t.Fatalf("GET /api/v1/soh = %d %+v, want %d and two summaries", code, sums, http.StatusOK)
	}

	if got := sums[0]; got.Name != "broken" || !strings.Contains(got.Error, "unexpected type string") {
		t.Errorf("GET /api/v1/soh first summary = %s with error %q, want broken with the soh status's", got.Name, got.Error)
	}

	if got := sums[1]; got.Name != "fine" || got.Error != "" {
		t.Errorf("GET /api/v1/soh second summary = %s with error %q, want fine with none", got.Name, got.Error)
	}
}

// A store that holds an undecodable experiment still gives the summaries of the others, and logs the error.
func TestGetSoHSummaryServesWhatTheStoreDecodes(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("fine", false))

	server := serveAs(t, "alice", sohSummaryRole("*")).URL

	s, _ := store.DefaultStore.(*testStore)
	store.DefaultStore = undecodableStore{testStore: s} //nolint:reassign // useTestStore restores it

	records := captureRecords(t)

	resp := do(t, http.MethodGet, server+"/api/v1/soh", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/soh with an undecodable experiment = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Experiments []soh.Summary `json:"experiments"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding GET /api/v1/soh: %v", err)
	}

	sums := body.Experiments

	names := make([]string, 0, len(sums))
	for _, sum := range sums {
		names = append(names, sum.Name)
	}

	if diff := cmp.Diff([]string{"fine"}, names); diff != "" {
		t.Errorf("GET /api/v1/soh with an undecodable experiment summaries mismatch (-want +got):\n%s", diff)
	}

	if !slices.ContainsFunc(records(), func(r string) bool { return strings.Contains(r, `"msg":"getting experiments"`) }) {
		t.Error("GET /api/v1/soh with an undecodable experiment logged no error getting experiments")
	}
}

// A summary whose client leaves while its minimega read waits for the shared connection gives the read up and
// writes nothing.
func TestGetSoHSummaryGivesUpWhenTheClientLeaves(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("exp", true, "soh"))

	received := holdMinimega(t)

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), middleware.ContextKeyRole, sohSummaryRole("*")))

	var (
		rec  = httptest.NewRecorder()
		done = make(chan struct{})
	)

	go func() {
		defer close(done)

		weberror.ErrorHandler(GetSoHSummary).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/soh", nil))
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GET /soh kept waiting for minimega after its client left")
	}

	if rec.Body.Len() != 0 {
		t.Errorf("GET /soh wrote %q (status %d) to a client that left, want nothing", rec.Body.String(), rec.Code)
	}

	got := slices.DeleteFunc(mmtest.Bases(received()), func(base string) bool { return base == "args" })
	if diff := cmp.Diff([]string{"hold"}, got); diff != "" {
		t.Errorf("GET /soh minimega commands mismatch (-want +got):\n%s", diff)
	}
}

// sohVMStates asks minimega nothing about an experiment that is stopped or a dry run, and forgets its last states.
func TestSohVMStatesOfExperimentsNotRunning(t *testing.T) {
	tests := map[string]string{"stopped": "", "dry run": "2026-09-28T14:00:00Z-DRYRUN"}

	for name, started := range tests {
		t.Run(name, func(t *testing.T) {
			config := sohSummaryExperiment("soh-not-running", started != "")
			if started != "" {
				config.Status["startTime"] = started
			}

			exp, err := types.DecodeExperimentFromConfig(config)
			if err != nil {
				t.Fatalf("types.DecodeExperimentFromConfig(%s) error = %v", name, err)
			}

			fake := useTestMM(t, &testMM{states: map[string]string{"vm-a": "RUNNING"}})

			lastVMStates.Store("soh-not-running", vmStatesRead{states: map[string]string{"vm-a": "RUNNING"}, at: time.Now()})
			t.Cleanup(func() { lastVMStates.Delete("soh-not-running") })

			for _, busy := range []bool{false, true} {
				states, obs, err := sohVMStates(t.Context(), *exp, busy)
				if states != nil || obs != freshness.Unobserved() || err != nil {
					t.Errorf("sohVMStates(%s, busy %t) = %v, %+v, %v; want nil, unobserved, nil", name, busy, states, obs, err)
				}
			}

			if _, ok := lastVMStates.Load("soh-not-running"); ok {
				t.Errorf("sohVMStates(%s) kept the experiment's last VM states, want them forgotten", name)
			}

			if n := fake.stateReads.Load(); n != 0 {
				t.Errorf("sohVMStates(%s) read minimega VM states %d times, want none", name, n)
			}
		})
	}
}

// A summary JSON cannot hold, as of a start whose progress is not a number, answers 500 and is logged.
func TestGetSoHSummaryCannotEncode(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("starting", false, "soh"))
	useTestMM(t, &testMM{})

	if err := cache.LockExperimentForStarting("starting"); err != nil {
		t.Fatalf("cache.LockExperimentForStarting(starting) error = %v", err)
	}

	startProgress.Store("starting", math.NaN())

	t.Cleanup(func() {
		startProgress.Delete("starting")
		cache.UnlockExperiment("starting")
	})

	records := captureRecords(t)

	code, _ := getSohSummary(t, sohSummaryRole("*"))
	if code != http.StatusInternalServerError {
		t.Errorf("GET /api/v1/soh with a start at NaN%% = %d, want %d", code, http.StatusInternalServerError)
	}

	if !slices.ContainsFunc(records(), func(r string) bool {
		return strings.Contains(r, `"msg":"marshaling state of health summary"`)
	}) {
		t.Error("GET /api/v1/soh with a summary JSON cannot hold logged no marshaling error")
	}
}
