package experiment_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/shell"
	"phenix/util/testenv"
)

// cancelMM is a minimega of no hosts and no VMs, which a user app run reads.
// Any other mm.MM method panics.
type cancelMM struct {
	mm.MM
}

func (cancelMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	return nil, nil
}

func (cancelMM) GetVMInfo(...mm.Option) mm.VMs {
	return nil
}

// useBlockingApp puts the user app phenix-app-<name> first on PATH until the
// test ends. The app starts a child sleep, writes the process ID of the child
// to the file that useBlockingApp returns, and waits for the child.
func useBlockingApp(t *testing.T, name string) string {
	t.Helper()

	testenv.RequireCommands(t, "sh", "sleep", "mv")

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	script := "#!/bin/sh\nsleep 30 &\necho $! > '" + pidFile + ".tmp'\nmv '" + pidFile + ".tmp' '" + pidFile + "'\nwait\n"

	path := filepath.Join(dir, "phenix-app-"+name)

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("os.WriteFile(phenix-app-%s) error = %v", name, err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	original := mm.DefaultMM
	mm.DefaultMM = cancelMM{} //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	return pidFile
}

// childPID waits for the process ID in pidFile.
func childPID(pidFile string) (int, error) {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return 0, fmt.Errorf("reading the process ID of the child: %w", err)
		}

		return pid, nil
	}

	return 0, errors.New("the user app wrote no process ID of its child in 10s")
}

// processGone returns an error if pid still runs 5s from now. A zombie counts
// as running, so this also waits for the process to be reaped.
func processGone(pid int) error {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !shell.ProcessExists(pid) {
			return nil
		}
	}

	return fmt.Errorf("child %d of the user app still runs 5s later, want it stopped", pid)
}

// A canceled context stops the user app that Create, Stop or Reconfigure runs,
// with the child process of the app, and the error wraps [context.Canceled].
// Create stores nothing, and Stop leaves the experiment running.
// TestStopFinishesWhenCanceledAfterTheCleanupApps covers a cancel after the
// last cleanup app finished.
func TestCancelStopsTheUserApp(t *testing.T) { //nolint:paralleltest // replaces the store, minimega and PATH
	const app = "cancel-probe"

	scenario := map[string]any{"apps": []any{map[string]any{"name": app}}}

	tests := []struct {
		name string
		// the status of the stored experiment exp; nil stores the topology and scenario for a create
		status      map[string]any
		run         func(context.Context) error
		wantRunning bool
	}{
		{
			name: "create",
			run: func(ctx context.Context) error {
				return experiment.Create(
					ctx,
					experiment.CreateWithName("exp"),
					experiment.CreateWithTopology("annotated"),
					experiment.CreateWithScenario("scn"),
				)
			},
		},
		{
			name:        "stop",
			status:      map[string]any{"startTime": "2024-01-01T00:00:00Z"},
			run:         func(ctx context.Context) error { return experiment.Stop(ctx, "exp") },
			wantRunning: true,
		},
		{
			name:   "reconfigure",
			status: map[string]any{},
			run:    func(ctx context.Context) error { return experiment.Reconfigure(ctx, "exp") },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useTestStore(t)

			pidFile := useBlockingApp(t, app)

			configs := []*store.Config{annotationTestTopology(), {
				Version:  "phenix.sandia.gov/v2",
				Kind:     "Scenario",
				Metadata: store.ConfigMetadata{Name: "scn", Annotations: map[string]string{"topology": "annotated"}},
				Spec:     scenario,
			}}

			if tc.status != nil {
				configs = []*store.Config{{
					Version:  "phenix.sandia.gov/v1",
					Kind:     "Experiment",
					Metadata: store.ConfigMetadata{Name: "exp"},
					Spec: map[string]any{
						"experimentName": "exp", "baseDir": t.TempDir(),
						"topology": map[string]any{"nodes": []any{}}, "scenario": scenario,
					},
					Status: tc.status,
				}}
			}

			for _, c := range configs {
				if err := store.Create(c); err != nil {
					t.Fatalf("storing %s %s: %v", c.Kind, c.Metadata.Name, err)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)

			go func() { done <- tc.run(ctx) }()

			pid, err := childPID(pidFile)
			if err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

			cancel()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("%s error = %v, want one that wraps context.Canceled", tc.name, err)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s did not return 30s after cancel, want it to stop the user app", tc.name)
			}

			if err := processGone(pid); err != nil {
				t.Errorf("after %s returned: %v", tc.name, err)
			}

			exp, err := experiment.Get("exp")

			switch {
			case tc.status == nil && err == nil:
				t.Error("Get(exp) after a canceled Create succeeded, want no experiment")
			case tc.status != nil && err != nil:
				t.Fatalf("Get(exp) error = %v", err)
			case tc.status != nil && exp.Running() != tc.wantRunning:
				t.Errorf("experiment running after a canceled %s = %t, want %t", tc.name, exp.Running(), tc.wantRunning)
			}
		})
	}
}
