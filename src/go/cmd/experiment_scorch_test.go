package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
)

// TestExpCmdScorchRun checks that experiment scorch runs the Scorch run that
// --run names in a running experiment, logs that it ran and leaves Scorch
// stopped, and refuses a run that the configuration does not have.
func TestExpCmdScorchRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		run       string
		humanized string // "" for success
	}{
		{run: "1"},
		{run: "2", humanized: "Unable to start Scorch run 2 for exp experiment"},
	}

	for _, tt := range tests {
		t.Run("run "+tt.run, func(t *testing.T) {
			cfg := vmCmdExperiment(t, "exp", true)
			cfg.Spec["scenario"] = map[string]any{"apps": []any{map[string]any{
				"name":     "scorch",
				"metadata": map[string]any{"components": []any{}, "runs": []any{map[string]any{}, map[string]any{}}},
			}}}

			expCmdUseStore(t, cfg)
			// a run records minimega's version, which minimega always gives; util/mm keeps it for the process
			mm.ForgetCluster()
			t.Cleanup(mm.ForgetCluster)
			mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == "version" {
					return []*minicli.Response{mmtest.Text("head", "minimega 3.4.0 abc 2026-10-01")}
				}

				return nil
			})
			expCmdNoServer(t)

			logs := plogtest.Capture(t)
			err := expCmdRun("scorch", "exp", "--run", tt.run)

			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Fatalf("phenix experiment scorch exp --run %s %s", tt.run, msg)
			}

			exp, err := experiment.Get("exp")
			if err != nil {
				t.Fatalf("experiment.Get(exp) error = %v", err)
			}

			if _, ok := exp.Status.AppStatus()["scorch"]; ok || exp.Status.AppRunning()["scorch"] {
				t.Errorf("Scorch after the run has status %t, running %t; want neither", ok, exp.Status.AppRunning()["scorch"])
			}

			const fallback = "phenix ui is not running, so this process executes the Scorch run, and the web UI does not show it"
			if got := len(logs.Records(t, plogtest.Message(fallback))); got != 1 {
				t.Errorf("phenix experiment scorch exp --run %s logged %d records that the run is not visible, want 1", tt.run, got)
			}

			records := logs.Records(t, plogtest.Message("scorch run triggered"))
			if want := map[bool]int{true: 1, false: 0}[tt.humanized == ""]; len(records) != want {
				t.Errorf(
					"phenix experiment scorch exp --run %s logged %d scorch run triggered records, want %d",
					tt.run,
					len(records),
					want,
				)
			}
		})
	}
}

// scorchFake stands in for the SCORCH routes of phenix ui. Before a start,
// the run's pipeline holds the statuses of an earlier run. After it, the
// first poll finds the run executing and the second finds it ended with
// final in the stage that it waits for.
type scorchFake struct {
	mu            sync.Mutex
	started       bool
	cleanupOnly   bool
	polls         int
	final         string
	omitExecuting bool
	onStart       func()
}

// pipelines is the answer to GET /experiments/exp/scorch/pipelines.
func (f *scorchFake) pipelines() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()

	node := func(name, stage, status string) map[string]any {
		return map[string]any{"name": name, "stage": stage, "status": status}
	}

	nodes := []map[string]any{
		node("configure", "", "success"), node("start", "", "success"), node("ping", "start", "success"),
		node("cleanup", "", "success"), node("done", "", "success"),
	}
	executing := []int{}

	if f.started {
		f.polls++

		switch {
		case f.polls == 1:
			nodes = []map[string]any{
				node("configure", "", "running"), node("start", "", "unknown"), node("ping", "start", "unknown"),
				node("cleanup", "", "unknown"), node("done", "", "unknown"),
			}
			executing = []int{0}
		case f.cleanupOnly:
			nodes = []map[string]any{
				node("configure", "", "unknown"), node("start", "", "unknown"), node("ping", "start", "unknown"),
				node("cleanup", "", f.final), node("done", "", "unknown"),
			}
		default:
			nodes = []map[string]any{
				node("configure", "", "success"), node("start", "", "success"), node("ping", "start", f.final),
				node("cleanup", "", "success"), node("done", "", f.final),
			}
		}
	}

	body := map[string]any{
		"pipelines": []any{map[string]any{"name": "baseline", "hasCleanup": true, "pipeline": nodes}},
		"running":   -1,
	}

	if !f.omitExecuting {
		body["executing"] = executing
	}

	return body
}

// handle answers the SCORCH and options routes.
func (f *scorchFake) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/options":
		writeJSONBody(w, map[string]any{})
	case r.URL.Path == "/api/v1/experiments/exp/scorch/pipelines" && r.Method == http.MethodGet:
		writeJSONBody(w, f.pipelines())
	case strings.HasPrefix(r.URL.Path, "/api/v1/experiments/exp/scorch/pipelines/0") && r.Method == http.MethodPost:
		if !strings.HasSuffix(r.URL.Path, "/clear") {
			f.mu.Lock()
			f.started = true
			f.cleanupOnly = strings.HasSuffix(r.URL.Path, "/cleanup")
			f.mu.Unlock()

			if f.onStart != nil {
				f.onStart()
			}
		}

		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		weberrorBody(w, http.StatusNotFound, "Scorch run not found")
	}
}

// weberrorBody writes the phenix server's JSON error envelope.
func weberrorBody(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// experiment scorch starts the run on phenix ui, prints its progress and
// waits until it ends, and fails when the run fails.
func TestExpCmdScorchOnServer(t *testing.T) { //nolint:paralleltest // replaces the socket
	tests := []struct {
		name    string
		args    []string
		final   string
		path    string
		wantOut []string
		wantErr string
	}{
		{
			name:  "success",
			final: "success",
			path:  "/api/v1/experiments/exp/scorch/pipelines/0",
			wantOut: []string{
				"Scorch run 0 of experiment exp started on the phenix server",
				"Scorch run 0: configure running",
				"Scorch run 0: configure success",
				"Scorch run 0: start success",
				"Scorch run 0: start/ping success",
				"Scorch run 0: cleanup success",
				"Scorch run 0: done success",
				"Scorch run 0 of experiment exp succeeded",
			},
		},
		{
			name:    "failure",
			final:   "failure",
			path:    "/api/v1/experiments/exp/scorch/pipelines/0",
			wantErr: `Scorch run 0 of experiment exp ended with done status "failure"`,
		},
		{
			name:  "cleanup",
			args:  []string{"--cleanup"},
			final: "success",
			path:  "/api/v1/experiments/exp/scorch/pipelines/0/cleanup",
			wantOut: []string{
				"Cleanup stage of Scorch run 0 of experiment exp started on the phenix server",
				"Scorch run 0: configure running",
				"Scorch run 0: cleanup success",
				"Cleanup stage of Scorch run 0 of experiment exp succeeded",
			},
		},
		{
			name:    "failed cleanup",
			args:    []string{"--cleanup"},
			final:   "failure",
			path:    "/api/v1/experiments/exp/scorch/pipelines/0/cleanup",
			wantErr: `the cleanup stage of Scorch run 0 of experiment exp ended with cleanup status "failure"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &scorchFake{final: tt.final}
			server := newFakeServer(t, fake.handle)
			expCmdUseSocket(t, server.socket)

			out, err := expCmdOutput(append([]string{"scorch", "exp"}, tt.args...)...)

			if tt.wantErr != "" {
				if !errors.Is(err, errScorchRunFailed) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("experiment scorch exp %q error = %v, want a failed run with %q", tt.args, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("experiment scorch exp %q error = %v, want nil", tt.args, err)
				}

				if diff := cmp.Diff(tt.wantOut, strings.Split(strings.TrimSpace(out), "\n")); diff != "" {
					t.Errorf("experiment scorch exp %q output mismatch (-want +got):\n%s", tt.args, diff)
				}
			}

			pipelines := "/api/v1/experiments/exp/scorch/pipelines"
			want := []recordedRequest{
				{method: http.MethodGet, path: "/api/v1/options"},
				{method: http.MethodGet, path: pipelines},
				{method: http.MethodPost, path: tt.path},
				{method: http.MethodGet, path: pipelines},
				{method: http.MethodGet, path: pipelines},
			}
			if diff := cmp.Diff(want, server.recorded(), requestFields); diff != "" {
				t.Errorf("experiment scorch exp %q requests mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// --status, --cancel and --clear send one request each to phenix ui for the
// run that --run selects, and print what happened.
func TestExpCmdScorchServerModes(t *testing.T) { //nolint:paralleltest // replaces the socket
	tests := []struct {
		args    []string
		want    recordedRequest
		wantOut string
		wantErr string
	}{
		{
			args: []string{"--status"},
			want: recordedRequest{method: http.MethodGet, path: "/api/v1/experiments/exp/scorch/pipelines"},
			wantOut: "Scorch run 0 (baseline) of experiment exp: not executing\n" +
				"  configure   success\n  start       success\n  start/ping  success\n  cleanup     success\n  done        success\n",
		},
		{
			args:    []string{"--status", "--run", "1"},
			want:    recordedRequest{method: http.MethodGet, path: "/api/v1/experiments/exp/scorch/pipelines"},
			wantErr: "experiment exp has no Scorch run 1",
		},
		{
			args:    []string{"--cancel", "-r", "0"},
			want:    recordedRequest{method: http.MethodDelete, path: "/api/v1/experiments/exp/scorch/pipelines/0"},
			wantOut: "Cancel requested for Scorch run 0 of experiment exp\n",
		},
		{
			args:    []string{"--clear"},
			want:    recordedRequest{method: http.MethodPost, path: "/api/v1/experiments/exp/scorch/pipelines/0/clear"},
			wantOut: "Cleared the status of Scorch run 0 of experiment exp\n",
		},
		{
			args:    []string{"--clear", "--run", "2"},
			want:    recordedRequest{method: http.MethodPost, path: "/api/v1/experiments/exp/scorch/pipelines/2/clear"},
			wantErr: "phenix server returned 404 Not Found: Scorch run not found",
		},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			fake := &scorchFake{}
			server := newFakeServer(t, fake.handle)
			expCmdUseSocket(t, server.socket)

			out, err := expCmdOutput(append([]string{"scorch", "exp"}, tt.args...)...)

			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("experiment scorch exp %q error = %v, want one that contains %q", tt.args, err, tt.wantErr)
				}
			case err != nil:
				t.Errorf("experiment scorch exp %q error = %v, want nil", tt.args, err)
			case out != tt.wantOut:
				t.Errorf("experiment scorch exp %q output = %q, want %q", tt.args, out, tt.wantOut)
			}

			if diff := cmp.Diff([]recordedRequest{tt.want}, server.recorded(), requestFields); diff != "" {
				t.Errorf("experiment scorch exp %q requests mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// experiment scorch takes one experiment name and at most one of the flags
// that need phenix ui, which say so when it is not running.
func TestExpCmdScorchArgs(t *testing.T) { //nolint:paralleltest // replaces the socket
	expCmdUseStore(t)
	expCmdNoServer(t)

	tests := []struct {
		args    []string
		wantErr string
	}{
		{args: []string{"scorch"}, wantErr: "accepts 1 arg(s), received 0"},
		{args: []string{"scorch", "a", "b"}, wantErr: "accepts 1 arg(s), received 2"},
		{args: []string{"scorch", "exp", "--status", "--cancel"}, wantErr: "were all set"},
		{args: []string{"scorch", "exp", "--cleanup", "--clear"}, wantErr: "were all set"},
		{args: []string{"scorch", "exp", "--status"}, wantErr: "phenix ui is not running"},
		{args: []string{"scorch", "exp", "--cancel"}, wantErr: "phenix ui is not running"},
		{args: []string{"scorch", "exp", "--cleanup"}, wantErr: "phenix ui is not running"},
		{args: []string{"scorch", "exp", "--clear"}, wantErr: "phenix ui is not running"},
	}

	for _, tt := range tests {
		err := expCmdRun(tt.args...)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("experiment %q error = %v, want one that contains %q", tt.args, err, tt.wantErr)
		}
	}
}

// A server that does not report the runs that it executes cannot be waited
// for, and experiment scorch does not start the run on it.
func TestExpCmdScorchOnOlderServer(t *testing.T) { //nolint:paralleltest // replaces the socket
	fake := &scorchFake{omitExecuting: true}
	server := newFakeServer(t, fake.handle)
	expCmdUseSocket(t, server.socket)

	err := expCmdRun("scorch", "exp")
	if err == nil || !strings.Contains(err.Error(), "does not report the Scorch runs that it executes") {
		t.Fatalf("experiment scorch exp error = %v, want one that says the server does not report its runs", err)
	}

	for _, r := range server.recorded() {
		if r.method == http.MethodPost {
			t.Errorf("experiment scorch exp sent %s %s, want no start", r.method, r.path)
		}
	}
}

// An interrupt while experiment scorch waits cancels the run on the server.
func TestScorchWaitCancelsOnInterrupt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fake := &scorchFake{final: "success", onStart: cancel}
	server := newFakeServer(t, fake.handle)

	var out bytes.Buffer

	wait := scorchWait{
		client: newExperimentClient(server.socket), exp: "exp", run: 0, interval: time.Millisecond, out: &out,
	}

	if err := wait.startAndWait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("startAndWait() after an interrupt = %v, want %v", err, context.Canceled)
	}

	got := server.recorded()
	if last := got[len(got)-1]; last.method != http.MethodDelete || last.path != "/api/v1/experiments/exp/scorch/pipelines/0" {
		t.Errorf("startAndWait() after an interrupt last sent %s %s, want DELETE of run 0", last.method, last.path)
	}

	if want := "Canceled Scorch run 0 of experiment exp on the phenix server\n"; !strings.HasSuffix(out.String(), want) {
		t.Errorf("startAndWait() after an interrupt wrote %q, want it to end with %q", out.String(), want)
	}
}
