package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

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
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, resp.StatusCode, readBody(t, resp))
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
		t.Fatalf("decoding summary: %v", err)
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
		t.Fatalf("expected 200, got %d", code)
	}

	if len(sums) != 2 || sums[0].Name != "alpha" || sums[1].Name != "zeta" {
		t.Fatalf("expected alpha and zeta, sorted, got %+v", sums)
	}

	// only the running experiment the caller may get is asked about
	if n := fake.stateReads.Load(); n != 1 {
		t.Errorf("minimega asked %d times, want once", n)
	}

	alpha, zeta := sums[0], sums[1]

	if alpha.Configured || alpha.Running || alpha.Status != "stopped" || alpha.VMs.Total != 2 {
		t.Errorf("alpha = %+v", alpha)
	}

	if !zeta.Configured || !zeta.SOHInitialized || zeta.Status != "started" || zeta.LastRun != "2026-09-28T14:03:40Z" {
		t.Errorf("zeta = %+v", zeta)
	}

	wantVMs := soh.VMCounts{Known: true, Total: 2, Running: 1, Failing: 1, NotDeploy: 1}
	if zeta.VMs != wantVMs || len(zeta.DownVMs) != 1 || zeta.DownVMs[0] != "vm-b" {
		t.Errorf("zeta VMs = %+v, down %v", zeta.VMs, zeta.DownVMs)
	}

	if zeta.Checks.Failing != 1 || len(zeta.FailingChecks) != 1 || zeta.FailingChecks[0].Check != "network" {
		t.Errorf("zeta checks = %+v, failing %+v", zeta.Checks, zeta.FailingChecks)
	}
}

func TestGetSoHSummaryForbiddenWithoutList(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("alpha", false))

	code, _ := getSohSummary(t, testRole("experiments", "get", "*"))
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", code)
	}
}

func TestGetSoHSummaryReportsMinimegaErrors(t *testing.T) {
	useTestStore(t, sohSummaryExperiment("zeta", true, "soh"))
	useTestMM(t, &testMM{err: errors.New("minimega unreachable")})

	lastVMStates.Delete("zeta")

	_, sums := getSohSummary(t, sohSummaryRole("*"))
	if len(sums) != 1 {
		t.Fatalf("got %+v", sums)
	}

	if sums[0].VMs.Known || sums[0].VMsError == "" {
		t.Errorf("expected unknown VM states with an error, got %+v", sums[0])
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
		t.Fatal(err)
	}

	t.Cleanup(func() { cache.UnlockExperiment("starting") })

	fake.stateReads.Store(0)

	_, sums := getSohSummary(t, sohSummaryRole("*"))

	if n := fake.stateReads.Load(); n != 0 {
		t.Errorf("minimega asked %d times while busy", n)
	}

	if sums[0].Name != "starting" || sums[0].Status != "starting" {
		t.Errorf("starting = %+v", sums[0])
	}

	if !sums[1].VMs.Known || sums[1].VMs.Running != 2 {
		t.Errorf("expected zeta's last states, got %+v", sums[1].VMs)
	}
}
