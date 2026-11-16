package soh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/app"
	v1 "phenix/types/version/v1"
)

// Checks of an experiment whose checks already run in this process are
// refused with app.ErrAlreadyRunning, and send no C2 command; a finished run
// lets the next one start.
func TestRunningRefusesASecondRun(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{"hostFiles": map[string]any{"web": []any{"/etc/motd"}}}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
	})

	fake := useSOHMM(t, answerFrom(map[string]string{"exp/web: stat -c present -- /etc/motd": "present"}))
	ctx := app.SetContextMetadata(t.Context(), map[string]any{"checks": []string{"files"}})

	running.Store("exp", struct{}{})

	err := newSOH().Running(ctx, exp)
	if !errors.Is(err, app.ErrAlreadyRunning) {
		t.Errorf("Running(exp) while checks of exp run error = %v, want %v", err, app.ErrAlreadyRunning)
	}

	if got := fake.commands(); len(got) != 0 {
		t.Errorf("Running(exp) while checks of exp run sent C2 commands %q, want none", got)
	}

	running.Delete("exp")

	for i := range 2 {
		if err := newSOH().Running(ctx, exp); err != nil {
			t.Errorf("Running(exp) %d after the other run ended error = %v", i+1, err)
		}
	}

	if _, held := running.Load("exp"); held {
		t.Error("checks of exp held as running after their run ended")
	}
}

// PostStart stops waiting for the startup delay, and runs no check, when the
// context ends.
func TestPostStartStopsTheStartupDelayWhenTheContextEnds(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{"startupDelay": "1h", "skipInitialNetworkConfigTests": true}, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
	})

	fake := useSOHMM(t, answerFrom(nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)

	go func() { done <- newSOH().PostStart(ctx, exp) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("PostStart(exp) with a done context error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("PostStart(exp) with a done context still waits for its 1h startup delay after 30s")
	}

	if got := fake.commands(); len(got) != 0 {
		t.Errorf("PostStart(exp) with a done context sent C2 commands %q, want none", got)
	}
}

// The checks and c2Timeout of a run read the same from scorch and the CLI, as
// strings or lists, and from the UI, as URL query values.
func TestRunChecksReadsTheMetadataOfEachSource(t *testing.T) { //nolint:paralleltest // replaces package globals
	for name, md := range map[string]map[string]any{
		"scorch list":         {"checks": []string{"files", "services"}, "c2Timeout": "42s"},
		"cli string":          {"checks": "files, services", "c2Timeout": "42s"},
		"ui query values":     {"checks": []string{"files,services"}, "c2Timeout": []string{"42s"}},
		"ui repeated queries": {"checks": []string{"files", "services"}, "c2Timeout": []string{"42s", "1s"}},
	} {
		t.Run(name, func(t *testing.T) {
			exp := sohExperiment(t, map[string]any{
				"hostFiles":    map[string]any{"web": []any{"/etc/motd"}},
				"hostServices": map[string]any{"web": []any{"sshd"}},
				"hostProcesses": map[string]any{
					"web": []any{"sshd"},
				},
			}, []*v1.Node{sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", ""))})

			fake := useSOHMM(t, answerFrom(map[string]string{
				"exp/web: stat -c present -- /etc/motd": "present",
				"exp/web: systemctl is-active -- sshd":  "active",
			}))

			s := newSOH()
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			if err := s.runChecks(app.SetContextMetadata(t.Context(), md), exp); err != nil {
				t.Fatalf("runChecks(%v) error = %v", md, err)
			}

			want := []string{"exp/web: stat -c present -- /etc/motd", "exp/web: systemctl is-active -- sshd"}
			if diff := cmp.Diff(want, fake.commands()); diff != "" {
				t.Errorf("runChecks(%v) C2 commands mismatch (-want +got):\n%s", md, diff)
			}

			if s.md.c2Timeout != 42*time.Second {
				t.Errorf("runChecks(%v) C2 timeout = %v, want 42s", md, s.md.c2Timeout)
			}
		})
	}
}

// warned counts the warnings with message msg in the JSON log lines of logs.
func warned(t *testing.T, logs *bytes.Buffer, msg string) int {
	t.Helper()

	var n int

	dec := json.NewDecoder(logs)

	for dec.More() {
		var record map[string]any
		if err := dec.Decode(&record); err != nil {
			t.Fatalf("decoding soh log record: %v", err)
		}

		if record[slog.LevelKey] == slog.LevelWarn.String() && record[slog.MessageKey] == msg {
			n++
		}
	}

	return n
}

// runChecks warns once when a run asks for reachability checks without the
// network-config check, as it then skips them.
func TestRunChecksWarnsOfSkippedReachability(t *testing.T) { //nolint:paralleltest // replaces package globals
	for name, tc := range map[string]struct {
		checks []string
		want   int
	}{
		"reachability alone":        {checks: []string{"reachability"}, want: 1},
		"custom reachability alone": {checks: []string{"custom-reachability"}, want: 1},
		"with network-config":       {checks: []string{"network-config", "reachability"}},
		"no reachability":           {checks: []string{"files"}},
	} {
		t.Run(name, func(t *testing.T) {
			exp := sohExperiment(t, map[string]any{}, nil)
			useSOHMM(t, answerFrom(nil))

			ctx, logs := jsonLogs(t)
			ctx = app.SetContextMetadata(ctx, map[string]any{"checks": tc.checks})

			s := newSOH()
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			if err := s.runChecks(ctx, exp); err != nil {
				t.Fatalf("runChecks(%q) error = %v", tc.checks, err)
			}

			if got := warned(t, logs, skipReachabilityMsg); got != tc.want {
				t.Errorf("runChecks(%q) warned %q %d times, want %d", tc.checks, skipReachabilityMsg, got, tc.want)
			}
		})
	}
}
