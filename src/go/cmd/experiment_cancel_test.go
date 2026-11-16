package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"phenix/store"
	"phenix/util/mm"
	"phenix/util/shell"
	"phenix/util/testenv"
)

// sigintMM is a minimega of no hosts and no VMs, which a user app run reads.
// Any other mm.MM method panics.
type sigintMM struct {
	mm.MM
}

func (sigintMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	return nil, nil
}

func (sigintMM) GetVMInfo(...mm.Option) mm.VMs {
	return nil
}

// sigintApp puts the user app phenix-app-<name> first on PATH until the test
// ends. The app starts a child sleep, writes the process ID of the child to
// the file that sigintApp returns, and waits for the child.
func sigintApp(t *testing.T, name string) string {
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
	mm.DefaultMM = sigintMM{} //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	return pidFile
}

// sigintChild waits for the process ID in pidFile.
func sigintChild(pidFile string) (int, error) {
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

// sigintConfigs are the topology topo, the scenario scn of topo with the app,
// and, when running is set, the running experiment exp of both.
func sigintConfigs(t *testing.T, app string, running bool) []store.Config {
	t.Helper()

	scenario := map[string]any{"apps": []any{map[string]any{"name": app}}}

	if running {
		return []store.Config{{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: map[string]any{
				"experimentName": "exp", "baseDir": t.TempDir(),
				"topology": map[string]any{"nodes": []any{}}, "scenario": scenario,
			},
			Status: map[string]any{"startTime": "2024-01-01T00:00:00Z"},
		}}
	}

	node := map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": "web"},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "ubuntu.qc2"}}},
	}

	return []store.Config{
		{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Topology",
			Metadata: store.ConfigMetadata{Name: "topo"},
			Spec:     map[string]any{"nodes": []any{node}},
		},
		{
			Version:  "phenix.sandia.gov/v2",
			Kind:     "Scenario",
			Metadata: store.ConfigMetadata{Name: "scn", Annotations: map[string]string{"topology": "topo"}},
			Spec:     scenario,
		},
	}
}

// A SIGINT, as Ctrl+C at a terminal sends, stops the user app that experiment
// create or stop runs, with the child of the app, and the command says that
// it was canceled. The user app is in a process group of its own, so only
// phenix gets the signal.
func TestExpCmdSIGINTStopsTheUserApp(t *testing.T) { //nolint:paralleltest // replaces the store, minimega and PATH, and signals the test
	const app = "sigint-probe"

	tests := []struct {
		name      string
		args      []string
		running   bool
		humanized string
		wantState string
	}{
		{
			name:      "create",
			args:      []string{"create", "exp", "-t", "topo", "-s", "scn"},
			humanized: "Unable to create the exp experiment: the command was canceled",
			wantState: "deleted",
		},
		{
			name:      "stop",
			args:      []string{"stop", "exp"},
			running:   true,
			humanized: "Problem encountered while stopping the exp experiment: the command was canceled",
			wantState: "running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdUseStore(t, sigintConfigs(t, app, tt.running)...)

			pidFile := sigintApp(t, app)
			done := make(chan error, 1)

			go func() { done <- expCmdRun(tt.args...) }()

			pid, err := sigintChild(pidFile)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				t.Fatalf("sending SIGINT to the test: %v", err)
			}

			select {
			case err := <-done:
				if !vmCmdHumanized(err, tt.humanized) {
					t.Errorf("experiment %q error = %v, want %q", tt.args, err, tt.humanized)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("experiment %q did not return 30s after SIGINT, want it to stop the user app", tt.args)
			}

			// a zombie counts as running, so wait for the child to be reaped
			for deadline := time.Now().Add(5 * time.Second); shell.ProcessExists(pid) && time.Now().Before(deadline); {
				time.Sleep(20 * time.Millisecond)
			}

			if shell.ProcessExists(pid) {
				t.Errorf("child %d of the user app runs 5s after experiment %q returned, want it stopped", pid, tt.args)
			}

			if got := expCmdState(t, "exp"); got != tt.wantState {
				t.Errorf("experiment exp after a canceled experiment %q = %s, want %s", tt.args, got, tt.wantState)
			}
		})
	}
}
