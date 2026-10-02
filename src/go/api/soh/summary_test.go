package soh

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/activeshadow/structs"

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

	body, err := json.Marshal(hosts)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}

	return decoded
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
		t.Fatal(err)
	}

	if !sum.Configured || !sum.ExpStarted || !sum.SOHInitialized || sum.SOHRunning || sum.Scenario != "soh-full" {
		t.Errorf("unexpected flags: %+v", sum)
	}

	wantVMs := VMCounts{
		Known: true, Total: 7, Running: 2, Failing: 2, NotRunning: 1, NotBoot: 1,
		NotDeploy: 1, External: 1, Delayed: 1,
	}
	if sum.VMs != wantVMs {
		t.Errorf("VMs = %+v, want %+v", sum.VMs, wantVMs)
	}

	if !reflect.DeepEqual(sum.DownVMs, []string{"historian", "flushed"}) {
		t.Errorf("DownVMs = %v", sum.DownVMs)
	}

	if sum.Hosts != 3 || sum.HostsWithErrors != 2 {
		t.Errorf("hosts = %d, with errors = %d", sum.Hosts, sum.HostsWithErrors)
	}

	if sum.Checks != (Tally{Total: 7, Passing: 4, Failing: 3}) {
		t.Errorf("Checks = %+v", sum.Checks)
	}

	if sum.Reachability != (Tally{Total: 2, Passing: 1, Failing: 1}) {
		t.Errorf("Reachability = %+v", sum.Reachability)
	}

	if sum.LastRun != "2026-09-28T14:03:40Z" || sum.LastRunDuration != 12.5 {
		t.Errorf("last run = %q (%v)", sum.LastRun, sum.LastRunDuration)
	}

	wantHistory := []RunSummary{{Time: "2026-09-28T13:48:00Z", Duration: 11, Total: 7, Failing: 1, HostsFailing: 1}}
	if !reflect.DeepEqual(sum.History, wantHistory) {
		t.Errorf("History = %+v", sum.History)
	}

	// capped at 2, newest first
	wantFailing := []FailingCheck{
		{Host: "hmi", Check: "reachability", Target: "plc (10.20.1.12)", Error: "no ICMP reply", Time: "2026-09-28T14:03:22Z"},
		{Host: "plc", Check: "process", Target: "modbusd", Error: "process not found", Time: "2026-09-28T14:03:05Z"},
	}
	if !reflect.DeepEqual(sum.FailingChecks, wantFailing) {
		t.Errorf("FailingChecks = %+v", sum.FailingChecks)
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
		t.Fatal(err)
	}

	if sum.LastRun != "2026-09-28T14:02:00Z" || sum.LastRunDuration != 0 || len(sum.History) != 0 {
		t.Errorf("last run = %q (%v), history %v", sum.LastRun, sum.LastRunDuration, sum.History)
	}

	// minimega not asked: only the topology's total is known
	if sum.VMs != (VMCounts{Total: 7}) || len(sum.DownVMs) != 0 {
		t.Errorf("VMs = %+v, down %v", sum.VMs, sum.DownVMs)
	}

	if sum.SOHInitialized {
		t.Error("expected soh not initialized")
	}
}

func TestSummarizeStoppedAndUnconfigured(t *testing.T) {
	t.Parallel()

	exp := summaryExperiment(map[string]any{
		"initialized": true,
		"hosts":       storedHosts(t, HostState{Hostname: "hmi", Networking: []State{{Error: "old"}}}),
	})
	exp.Status = &v1.ExperimentStatus{AppsF: exp.Status.AppStatus()}

	sum, err := Summarize(exp, map[string]string{"hmi": "RUNNING"}, 10)
	if err != nil {
		t.Fatal(err)
	}

	// a stopped experiment's leftover results are not reported
	if sum.Running || sum.ExpStarted || sum.SOHInitialized || sum.Checks.Total != 0 || sum.VMs != (VMCounts{Total: 7}) {
		t.Errorf("stopped summary = %+v", sum)
	}

	if !sum.Configured {
		t.Error("expected soh configured")
	}

	exp.Spec.(*v1.ExperimentSpec).ScenarioF = nil

	if sum, _ = Summarize(exp, nil, 10); sum.Configured {
		t.Error("expected soh not configured without a scenario")
	}

	if sum.FailingChecks == nil || sum.DownVMs == nil || sum.History == nil {
		t.Error("expected empty lists, not null")
	}
}

func TestSummarizeReportsUndecodableResults(t *testing.T) {
	t.Parallel()

	for _, status := range []map[string]any{{"hosts": "nope"}, {"hosts": []any{}, "history": "nope"}} {
		if _, err := Summarize(summaryExperiment(status), nil, 10); err == nil {
			t.Errorf("Summarize of soh status %v succeeded, want an error", status)
		}
	}
}

func TestSummarizeDescribesWhatFailingChecksLookedAt(t *testing.T) {
	t.Parallel()

	failed := func(md map[string]any) []State {
		return []State{{Timestamp: "2026-09-28T14:03:00Z", Error: "failed", Metadata: md}}
	}

	tests := []struct {
		host  HostState
		check string
		want  string
	}{
		{HostState{Reachability: failed(map[string]any{"target": "rtu", "ip": "10.0.0.3"})}, "reachability", "rtu (10.0.0.3)"},
		{HostState{Reachability: failed(map[string]any{"target": "10.0.0.3", "ip": "10.0.0.3"})}, "reachability", "10.0.0.3"},
		{
			HostState{Reachability: failed(map[string]any{"ip": "10.0.0.3", "port": 502, "proto": "tcp"})},
			"reachability",
			"10.0.0.3 tcp/502",
		},
		{HostState{Files: failed(map[string]any{"host": "a", "path": "/etc/x"})}, "file", "/etc/x"},
		{HostState{Services: failed(map[string]any{"service": "PIArchiveSS"})}, "service", "PIArchiveSS"},
		{HostState{CustomTests: failed(map[string]any{"test": "ladder-logic-hash"})}, "custom test", "ladder-logic-hash"},
		{HostState{Networking: failed(nil)}, "network", ""},
	}

	for _, tc := range tests {
		tc.host.Hostname = "hmi"

		sum, err := Summarize(summaryExperiment(map[string]any{"hosts": storedHosts(t, tc.host)}), nil, 10)
		if err != nil || len(sum.FailingChecks) != 1 {
			t.Fatalf("Summarize = %+v, %v; want one failing check", sum.FailingChecks, err)
		}

		if got := sum.FailingChecks[0]; got.Check != tc.check || got.Target != tc.want {
			t.Errorf("failing %s check = %q on %q, want %q", tc.check, got.Check, got.Target, tc.want)
		}
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
		t.Fatal(err)
	}
}

// asStored returns v as it reads back from the store, which keeps JSON.
func asStored(t *testing.T, v any) any {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
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

			c, _ := store.NewConfig("experiment/exp")
			if err := store.Get(c); err != nil {
				t.Fatal(err)
			}

			stored, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				t.Fatal(err)
			}

			written, _ := stored.Status.AppStatus()["soh"].(map[string]any)

			if written["initialized"] != true || written["custom"] != "kept" || written["packetCapture"] == nil {
				t.Errorf("existing status not kept: %v", written)
			}

			history, _ := written["history"].([]any)
			if len(history) != len(tc.kept)+1 || (len(tc.kept) > 0 && !reflect.DeepEqual(history[:len(tc.kept)], asStored(t, tc.kept))) {
				t.Fatalf("history = %v, want %v and the new run", history, tc.kept)
			}

			sum, err := Summarize(stored, nil, 10)
			if err != nil {
				t.Fatal(err)
			}

			// the duration is kept to the millisecond
			ms := sum.LastRunDuration * 1000
			if _, err := time.Parse(time.RFC3339, sum.LastRun); err != nil || ms < 2000 || math.Abs(ms-math.Round(ms)) > 1e-6 {
				t.Errorf("last run = %q (%vs), want its time and at least 2s to the millisecond", sum.LastRun, sum.LastRunDuration)
			}

			want := RunSummary{
				Time: sum.LastRun, Duration: sum.LastRunDuration,
				Total: 6, Failing: 3, HostsFailing: 2, HostsDown: 1,
			}
			if got := sum.History[len(sum.History)-1]; got != want {
				t.Errorf("newest run = %+v, want %+v", got, want)
			}
		})
	}
}
