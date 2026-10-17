package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/soh"
	"phenix/store"
	"phenix/web/cache"
	"phenix/web/rbac"
)

func TestGetExperimentSoHNeedsExperimentGet(t *testing.T) {
	useTestStore(t) // holds no experiments

	tests := map[string]struct {
		role     rbac.Role
		wantCode int
	}{
		// allowed through to the lookup, which fails
		"experiment get": {
			role:     testRole("experiments", "get", "test-experiment"),
			wantCode: http.StatusInternalServerError,
		},
		"other experiment": {
			role:     testRole("experiments", "get", "other-experiment"),
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
	return combinedRole(testRole("experiments", "list", "*"), testRole("experiments", "get", getNames...))
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
	if got := resp.Header.Get("X-Phenix-Stale"); got != "true" {
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
