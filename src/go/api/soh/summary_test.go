package soh

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

func summaryNode(name string, dnb bool, delay string, external bool) *v1.Node {
	node := &v1.Node{
		TypeF:     "VirtualMachine",
		GeneralF:  &v1.General{HostnameF: name, DoNotBootF: &dnb},
		HardwareF: &v1.Hardware{},
		NetworkF:  &v1.Network{},
	}

	if delay != "" {
		node.DelayF = &v1.Delay{TimerF: delay}
	}

	if external {
		ext := true
		node.ExternalF = &ext
	}

	return node
}

// summaryExperiment is a running experiment with the soh app, whose status
// holds the given app status for soh.
func summaryExperiment(sohStatus map[string]any) *types.Experiment {
	return &types.Experiment{
		Metadata: store.ConfigMetadata{
			Name:        "exp",
			Annotations: map[string]string{"scenario": "soh-full"},
		},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "exp",
			TopologyF: &v1.TopologySpec{
				NodesF: []*v1.Node{
					summaryNode("hmi", false, "", false),
					summaryNode("plc", false, "", false),
					summaryNode("historian", false, "", false),
					summaryNode("flushed", false, "", false),
					summaryNode("spare", true, "", false),
					summaryNode("late", false, "5m", false),
					summaryNode("rtu", false, "", true),
				},
			},
			ScenarioF: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "soh"}}},
		},
		Status: &v1.ExperimentStatus{
			StartTimeF: "2026-09-28T14:00:00Z",
			AppsF:      map[string]any{"soh": sohStatus},
		},
	}
}

// storedHosts returns results as the soh app stores them, after a round trip
// through the store.
func storedHosts(t *testing.T, hosts ...HostState) []any {
	t.Helper()

	stored, _ := asStored(t, hosts).([]any)

	return stored
}

func TestSummarizeRunningExperiment(t *testing.T) {
	t.Parallel()

	status := map[string]any{
		"initialized": true,
		"hosts": storedHosts(t,
			HostState{
				Hostname:   "hmi",
				Networking: []State{{Timestamp: "2026-09-28T14:03:00Z", Success: "ok"}},
				Reachability: []State{
					{
						Timestamp: "2026-09-28T14:03:22Z",
						Error:     "no ICMP reply",
						Metadata:  map[string]any{"host": "hmi", "target": "plc", "ip": "10.20.1.12"},
					},
					{Timestamp: "2026-09-28T14:03:21Z", Success: "reachable", Metadata: map[string]any{"target": "historian"}},
				},
			},
			HostState{
				Hostname:   "plc",
				Networking: []State{{Timestamp: "2026-09-28T14:02:50Z", Success: "ok"}},
				Processes: []State{
					{Timestamp: "2026-09-28T14:03:05Z", Error: "process not found", Metadata: map[string]any{"proc": "modbusd"}},
				},
				Listeners: []State{
					{Timestamp: "2026-09-28T14:02:58Z", Error: "nothing listening", Metadata: map[string]any{"port": "tcp/502"}},
				},
			},
			HostState{
				Hostname:   "historian",
				Networking: []State{{Timestamp: "2026-09-28T14:02:40Z", Success: "ok"}},
			},
		),
		"lastRun":         "2026-09-28T14:03:40Z",
		"lastRunDuration": 12.5,
		"history": []any{
			map[string]any{
				"time":         "2026-09-28T13:48:00Z",
				"duration":     11.0,
				"total":        7.0,
				"failing":      1.0,
				"hostsFailing": 1.0,
				"hostsDown":    0.0,
			},
		},
		"packetCapture": map[string]any{"hosts": []any{}},
	}

	vmStates := map[string]string{
		"hmi":       "RUNNING",
		"plc":       "RUNNING",
		"historian": "PAUSED",
		"late":      "BUILDING",
	}

	sum, err := Summarize(summaryExperiment(status), vmStates, 2)
	if err != nil {
		t.Fatalf("Summarize(running exp, %v, 2) error = %v", vmStates, err)
	}

	want := Summary{
		Name: "exp", Scenario: "soh-full", Running: true,
		Configured: true, ExpStarted: true, SOHInitialized: true,
		VMs: VMCounts{
			Known: true, Total: 7, Running: 2, Failing: 2, NotRunning: 1, NotBoot: 1,
			NotDeploy: 1, External: 1, Delayed: 1,
		},
		DownVMs:         []string{"historian", "flushed"},
		Hosts:           3,
		HostsWithErrors: 2,
		Checks:          Tally{Total: 7, Passing: 4, Failing: 3},
		Reachability:    Tally{Total: 2, Passing: 1, Failing: 1},
		LastRun:         "2026-09-28T14:03:40Z",
		LastRunDuration: 12.5,
		History:         []RunSummary{{Time: "2026-09-28T13:48:00Z", Duration: 11, Total: 7, Failing: 1, HostsFailing: 1}},
		// capped at 2, newest first
		FailingChecks: []FailingCheck{
			{Host: "hmi", Check: "reachability", Target: "plc (10.20.1.12)", Error: "no ICMP reply", Time: "2026-09-28T14:03:22Z"},
			{Host: "plc", Check: "process", Target: "modbusd", Error: "process not found", Time: "2026-09-28T14:03:05Z"},
		},
	}
	if diff := cmp.Diff(want, sum); diff != "" {
		t.Errorf("Summarize(running exp, %v, 2) mismatch (-want +got):\n%s", vmStates, diff)
	}
}

func TestSummarizeWithoutRunTimeUsesNewestCheck(t *testing.T) {
	t.Parallel()

	status := map[string]any{
		"hosts": storedHosts(t, HostState{
			Hostname:   "hmi",
			Networking: []State{{Timestamp: "2026-09-28T14:01:00Z"}, {Timestamp: "2026-09-28T14:02:00Z"}},
		}),
	}

	sum, err := Summarize(summaryExperiment(status), nil, 10)
	if err != nil {
		t.Fatalf("Summarize(exp without lastRun, nil, 10) error = %v", err)
	}

	if want := "2026-09-28T14:02:00Z"; sum.LastRun != want {
		t.Errorf("Summarize(exp without lastRun, nil, 10).LastRun = %q, want %q", sum.LastRun, want)
	}

	if sum.LastRunDuration != 0 {
		t.Errorf("Summarize(exp without lastRun, nil, 10).LastRunDuration = %v, want 0", sum.LastRunDuration)
	}

	if len(sum.History) != 0 {
		t.Errorf("Summarize(exp without lastRun, nil, 10).History = %+v, want empty", sum.History)
	}

	// minimega not asked: only the topology's total is known
	if diff := cmp.Diff(VMCounts{Total: 7}, sum.VMs); diff != "" {
		t.Errorf("Summarize(exp without lastRun, nil, 10).VMs mismatch (-want +got):\n%s", diff)
	}

	if len(sum.DownVMs) != 0 {
		t.Errorf("Summarize(exp without lastRun, nil, 10).DownVMs = %q, want empty", sum.DownVMs)
	}

	if sum.SOHInitialized {
		t.Error("Summarize(exp without initialized, nil, 10).SOHInitialized = true, want false")
	}
}

func TestSummarizeCountsDelayedVMAsDownOnceStarted(t *testing.T) {
	t.Parallel()

	vmStates := map[string]string{
		"hmi": "RUNNING", "plc": "RUNNING", "historian": "RUNNING", "flushed": "RUNNING", "late": "PAUSED",
	}

	sum, err := Summarize(summaryExperiment(map[string]any{}), vmStates, 10)
	if err != nil {
		t.Fatalf("Summarize(exp, %v, 10) error = %v", vmStates, err)
	}

	// the delayed VM counts as not running, not as delayed
	wantVMs := VMCounts{Known: true, Total: 7, Running: 4, NotRunning: 1, NotBoot: 1, External: 1}
	if diff := cmp.Diff(wantVMs, sum.VMs); diff != "" {
		t.Errorf("Summarize(exp, %v, 10).VMs mismatch (-want +got):\n%s", vmStates, diff)
	}

	if diff := cmp.Diff([]string{"late"}, sum.DownVMs); diff != "" {
		t.Errorf("Summarize(exp, %v, 10).DownVMs mismatch (-want +got):\n%s", vmStates, diff)
	}
}

func TestSummarizeStoppedAndUnconfigured(t *testing.T) {
	t.Parallel()

	exp := summaryExperiment(map[string]any{
		"initialized": true,
		"hosts":       storedHosts(t, HostState{Hostname: "hmi", Networking: []State{{Error: "old"}}}),
	})
	exp.Status = &v1.ExperimentStatus{AppsF: exp.Status.AppStatus()}

	vmStates := map[string]string{"hmi": "RUNNING"}

	sum, err := Summarize(exp, vmStates, 10)
	if err != nil {
		t.Fatalf("Summarize(stopped exp, %v, 10) error = %v", vmStates, err)
	}

	// a stopped experiment's leftover results are not reported, and its lists are empty, not null
	want := Summary{
		Name: "exp", Scenario: "soh-full", Configured: true, VMs: VMCounts{Total: 7},
		DownVMs: []string{}, History: []RunSummary{}, FailingChecks: []FailingCheck{},
	}
	if diff := cmp.Diff(want, sum); diff != "" {
		t.Errorf("Summarize(stopped exp, %v, 10) mismatch (-want +got):\n%s", vmStates, diff)
	}

	exp.Spec.(*v1.ExperimentSpec).ScenarioF = nil

	sum, err = Summarize(exp, nil, 10)
	if err != nil {
		t.Fatalf("Summarize(stopped exp without a scenario, nil, 10) error = %v", err)
	}

	want.Configured = false
	if diff := cmp.Diff(want, sum); diff != "" {
		t.Errorf("Summarize(stopped exp without a scenario, nil, 10) mismatch (-want +got):\n%s", diff)
	}
}

func TestSummarizeReportsUndecodableResults(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]any{
		"hosts":   {"hosts": "nope"},
		"history": {"hosts": []any{}, "history": "nope"},
	}

	for name, status := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Summarize(summaryExperiment(status), nil, 10); err == nil {
				t.Errorf("Summarize(exp with soh status %v, nil, 10) error = nil, want an error", status)
			}
		})
	}
}

func TestSummarizeDescribesWhatFailingChecksLookedAt(t *testing.T) {
	t.Parallel()

	failed := func(md map[string]any) []State {
		return []State{{Timestamp: "2026-09-28T14:03:00Z", Error: "failed", Metadata: md}}
	}

	tests := []struct {
		name  string
		host  HostState
		check string
		want  string
	}{
		{
			name:  "reachability of a named target",
			host:  HostState{Reachability: failed(map[string]any{"target": "rtu", "ip": "10.0.0.3"})},
			check: "reachability", want: "rtu (10.0.0.3)",
		},
		{
			name:  "reachability of a target named by its address",
			host:  HostState{Reachability: failed(map[string]any{"target": "10.0.0.3", "ip": "10.0.0.3"})},
			check: "reachability", want: "10.0.0.3",
		},
		{
			name:  "reachability of a port",
			host:  HostState{Reachability: failed(map[string]any{"ip": "10.0.0.3", "port": 502, "proto": "tcp"})},
			check: "reachability", want: "10.0.0.3 tcp/502",
		},
		{name: "file", host: HostState{Files: failed(map[string]any{"host": "a", "path": "/etc/x"})}, check: "file", want: "/etc/x"},
		{
			name:  "service",
			host:  HostState{Services: failed(map[string]any{"service": "PIArchiveSS"})},
			check: "service", want: "PIArchiveSS",
		},
		{
			name:  "custom test",
			host:  HostState{CustomTests: failed(map[string]any{"test": "ladder-logic-hash"})},
			check: "custom test", want: "ladder-logic-hash",
		},
		{name: "network", host: HostState{Networking: failed(nil)}, check: "network", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.host.Hostname = "hmi"

			sum, err := Summarize(summaryExperiment(map[string]any{"hosts": storedHosts(t, tc.host)}), nil, 10)
			if err != nil {
				t.Fatalf("Summarize(exp with a failing %s check, nil, 10) error = %v", tc.check, err)
			}

			want := []FailingCheck{{Host: "hmi", Check: tc.check, Target: tc.want, Error: "failed", Time: "2026-09-28T14:03:00Z"}}
			if diff := cmp.Diff(want, sum.FailingChecks); diff != "" {
				t.Errorf("Summarize(exp with a failing %s check, nil, 10).FailingChecks mismatch (-want +got):\n%s", tc.check, diff)
			}
		})
	}
}

// useStoredExperiment points the store at a new BoltDB file holding exp.
func useStoredExperiment(t *testing.T, exp *types.Experiment) {
	t.Helper()

	storetest.Use(t)

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: exp.Metadata,
		Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	})
	if err != nil {
		t.Fatalf("storing experiment %s: %v", exp.Metadata.Name, err)
	}
}

// asStored returns v as it reads back from the store, which keeps JSON.
func asStored(t *testing.T, v any) any {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%v) error = %v", v, err)
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
	}

	return decoded
}

func TestWriteRunFinishedRecordsTheRunForTheSummary(t *testing.T) { //nolint:paralleltest // replaces the default store
	full := make([]any, 0, MaxRunHistory)

	for i := range MaxRunHistory {
		full = append(full, map[string]any{"time": strconv.Itoa(i), "extra": i})
	}

	tests := map[string]struct {
		history any   // as the app status holds it
		kept    []any // the entries kept before the new one
	}{
		"the first run": {history: nil, kept: nil},
		"after earlier runs, keeping fields this version does not know": {
			history: []any{map[string]any{"time": "earlier", "extra": "kept"}},
			kept:    []any{map[string]any{"time": "earlier", "extra": "kept"}},
		},
		"after a run earlier in the same process": {
			history: []map[string]any{{"time": "earlier"}},
			kept:    []any{map[string]any{"time": "earlier"}},
		},
		"a full history, dropping the oldest": {history: full, kept: full[1:]},
		"an unreadable history, replaced":     {history: "nope", kept: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status := map[string]any{
				"hosts":         []any{},
				"packetCapture": map[string]any{"hosts": []any{"10.0.0.1"}},
				"custom":        "kept",
			}

			if tc.history != nil {
				status["history"] = tc.history
			}

			exp := summaryExperiment(status)
			useStoredExperiment(t, exp)

			s := newSOH()
			s.status = map[string]HostState{
				"a": {Hostname: "a", Networking: []State{{Error: "no C2"}}, Processes: []State{{Error: "missing"}}},
				"b": {Hostname: "b", Networking: []State{{Success: "ok"}}, Files: []State{{Error: "missing"}, {Success: "ok"}}},
				"c": {Hostname: "c", Networking: []State{{Success: "ok"}}},
			}

			s.writeRunFinished(context.Background(), exp, time.Now().Add(-2*time.Second))

			c, err := store.NewConfig("experiment/exp")
			if err != nil {
				t.Fatalf("store.NewConfig(experiment/exp) error = %v", err)
			}

			if err := store.Get(c); err != nil {
				t.Fatalf("store.Get(experiment/exp) error = %v", err)
			}

			stored, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				t.Fatalf("types.DecodeExperimentFromConfig(experiment/exp) error = %v", err)
			}

			written, _ := stored.Status.AppStatus()["soh"].(map[string]any)

			// the existing status is kept, and the app marked initialized
			gotKept := map[string]any{
				"initialized":   written["initialized"],
				"custom":        written["custom"],
				"packetCapture": written["packetCapture"],
			}
			wantKept := map[string]any{
				"initialized":   true,
				"custom":        "kept",
				"packetCapture": map[string]any{"hosts": []any{"10.0.0.1"}},
			}
			if diff := cmp.Diff(wantKept, gotKept); diff != "" {
				t.Errorf("writeRunFinished() soh status mismatch (-want +got):\n%s", diff)
			}

			history, _ := written["history"].([]any)
			if len(history) != len(tc.kept)+1 {
				t.Fatalf("writeRunFinished() history length = %d, want %d", len(history), len(tc.kept)+1)
			}

			var wantHistory []any
			if tc.kept != nil {
				wantHistory, _ = asStored(t, tc.kept).([]any)
			}

			if diff := cmp.Diff(wantHistory, history[:len(tc.kept)], cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("writeRunFinished() history before the new run mismatch (-want +got):\n%s", diff)
			}

			sum, err := Summarize(stored, nil, 10)
			if err != nil {
				t.Fatalf("Summarize(stored exp, nil, 10) error = %v", err)
			}

			if _, err := time.Parse(time.RFC3339, sum.LastRun); err != nil {
				t.Errorf("Summarize(stored exp, nil, 10).LastRun = %q, want an RFC 3339 time: %v", sum.LastRun, err)
			}

			// the duration is kept to the millisecond
			if ms := sum.LastRunDuration * 1000; ms < 2000 || math.Abs(ms-math.Round(ms)) > 1e-6 {
				t.Errorf("Summarize(stored exp, nil, 10).LastRunDuration = %vs, want at least 2s to the millisecond", sum.LastRunDuration)
			}

			if len(sum.History) == 0 {
				t.Fatal("Summarize(stored exp, nil, 10).History is empty, want the new run")
			}

			want := RunSummary{
				Time: sum.LastRun, Duration: sum.LastRunDuration,
				Total: 6, Failing: 3, HostsFailing: 2, HostsDown: 1,
			}
			if diff := cmp.Diff(want, sum.History[len(sum.History)-1]); diff != "" {
				t.Errorf("Summarize(stored exp, nil, 10) newest run mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
