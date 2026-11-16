package scorch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/polltest"
	"phenix/util/testenv"
	"phenix/web/lifecycle"
)

// KillTerminals ends every terminal's process, and a terminal ended twice,
// by its context and by KillTerminals, is ended once.
//
//nolint:paralleltest // uses the package's terminals
func TestKillTerminalsEndsEveryTerminal(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		testenv.Skipf(t, "sleep is not installed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make([]chan struct{}, 0, 2)

	for i := range 2 {
		d, err := CreateWebTerminal(ctx, "exp", i, 0, "start", "break", t.TempDir(), sleep, []string{"60"})
		if err != nil {
			t.Fatalf("CreateWebTerminal(sleep 60) error = %v", err)
		}

		done = append(done, d)
	}

	// the context ending kills them too, at the same time
	cancel()

	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()

	if left := KillTerminals(wait); left != 0 {
		t.Errorf("KillTerminals() = %d terminals still running, want 0", left)
	}

	for i, d := range done {
		select {
		case <-d:
		default:
			t.Errorf("terminal %d not done after KillTerminals(), want done", i)
		}
	}

	if terms, _ := GetExperimentTerminals("exp", -1); len(terms) != 0 {
		t.Errorf("GetExperimentTerminals(exp) after KillTerminals() = %d terminals, want none", len(terms))
	}
}

// Once the server is shutting down, no terminal starts.
//
//nolint:paralleltest // stops the server's lifecycle
func TestCreateWebTerminalRefusedWhileStopping(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		testenv.Skipf(t, "sleep is not installed")
	}

	lifecycle.Start()
	t.Cleanup(lifecycle.Start)

	lifecycle.Stop()

	_, err = CreateWebTerminal(context.Background(), "exp", 0, 0, "start", "break", t.TempDir(), sleep, []string{"60"})
	if !errors.Is(err, ErrServerStopping) {
		t.Errorf("CreateWebTerminal() while stopping error = %v, want %v", err, ErrServerStopping)
	}

	if terms, _ := GetExperimentTerminals("exp", -1); len(terms) != 0 {
		t.Errorf("GetExperimentTerminals(exp) after a refused start = %d terminals, want none", len(terms))
	}
}

// A terminal whose command cannot start is refused and not kept.
//
//nolint:paralleltest // uses the package's terminals
func TestCreateWebTerminalOfAMissingCommand(t *testing.T) {
	const missing = "phenix-scorch-no-such-command"

	done, err := CreateWebTerminal(context.Background(), "exp", 0, 0, "start", "brk", t.TempDir(), missing, nil)
	if err == nil || !strings.HasPrefix(err.Error(), missing+" terminal failed: ") || done != nil {
		t.Errorf("CreateWebTerminal(%s) = %v, %v; want no channel and an error naming the command", missing, done, err)
	}

	if terms, _ := GetExperimentTerminals("exp", -1); len(terms) != 0 {
		t.Errorf("GetExperimentTerminals(exp) after a failed start = %d terminals, want none", len(terms))
	}
}

// A terminal is found by its PID and its component until it is killed, which
// ends it once: killing it again, or one never kept, does nothing.
//
//nolint:paralleltest // uses the package's terminals
func TestKillTerminal(t *testing.T) {
	testenv.RequireCommands(t, "sleep")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done, err := CreateWebTerminal(ctx, "exp", 1, 2, "start", "brk", t.TempDir(), "sleep", []string{"60"})
	if err != nil {
		t.Fatalf("CreateWebTerminal(sleep 60) error = %v", err)
	}

	byKey, err := GetTerminalByExperiment("exp|1|2|start|brk")
	if err != nil {
		t.Fatalf("GetTerminalByExperiment(exp|1|2|start|brk) error = %v", err)
	}

	byPID, err := GetTerminalByPID(byKey.Pid)
	if err != nil || byPID.Done != done || byPID.Pty == nil {
		t.Fatalf("GetTerminalByPID(%d) = %+v, %v; want the terminal started", byKey.Pid, byPID, err)
	}

	want := WebTerm{Exp: "exp", Run: 1, Loop: 2, Stage: "start", Name: "brk"}
	opts := cmp.Options{cmpopts.IgnoreFields(WebTerm{}, "Pid", "Pty", "Done"), cmpopts.IgnoreUnexported(WebTerm{})}

	if diff := cmp.Diff(want, byPID, opts); diff != "" {
		t.Errorf("GetTerminalByPID(%d) mismatch (-want +got):\n%s", byKey.Pid, diff)
	}

	for i := range 2 {
		if err := KillTerminal(byPID); err != nil {
			t.Errorf("KillTerminal() call %d error = %v, want nil", i+1, err)
		}
	}

	select {
	case <-done:
	default:
		t.Error("terminal not done after KillTerminal(), want done")
	}

	if _, err := GetTerminalByPID(byPID.Pid); !errors.Is(err, ErrTerminalNotFound) {
		t.Errorf("GetTerminalByPID(%d) after KillTerminal() error = %v, want %v", byPID.Pid, err, ErrTerminalNotFound)
	}

	if _, err := GetTerminalByExperiment(byPID.key); !errors.Is(err, ErrTerminalNotFound) {
		t.Errorf("GetTerminalByExperiment(%s) after KillTerminal() error = %v, want %v", byPID.key, err, ErrTerminalNotFound)
	}

	never := newWebTerm("exp", 9, 0, "start", "never")
	if err := KillTerminal(never); err != nil {
		t.Errorf("KillTerminal() of a terminal never kept error = %v, want nil", err)
	}

	select {
	case <-never.Done:
		t.Error("KillTerminal() of a terminal never kept closed its Done, want it open")
	default:
	}
}

// KillTerminals with a context already done returns at once, and the
// terminals it did not wait for still end.
//
//nolint:paralleltest // uses the package's terminals
func TestKillTerminalsStopsWaitingWithItsContext(t *testing.T) {
	testenv.RequireCommands(t, "sleep")

	const terminals = 3

	done := make([]chan struct{}, 0, terminals)

	for i := range terminals {
		d, err := CreateWebTerminal(context.Background(), "exp", i, 0, "start", "brk", t.TempDir(), "sleep", []string{"60"})
		if err != nil {
			t.Fatalf("CreateWebTerminal(sleep 60) error = %v", err)
		}

		done = append(done, d)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// its count depends on which kills end first
	polltest.Within(t, "KillTerminals(done context)", func() { _ = KillTerminals(ctx) })

	for i, d := range done {
		polltest.Within(t, fmt.Sprintf("terminal %d ending", i), func() { <-d })
	}

	polltest.Until(t, "every terminal being forgotten", func() bool {
		terms, _ := GetExperimentTerminals("", -1)

		return len(terms) == 0
	})
}

// GetExperimentTerminals lists every terminal for no experiment, or those of
// each listed experiment, of one run or of every run for a negative run.
//
//nolint:paralleltest // uses the package's terminals
func TestGetExperimentTerminals(t *testing.T) {
	terms := []WebTerm{
		newWebTerm("a", 0, 0, "start", "x"),
		newWebTerm("a", 1, 0, "start", "x"),
		newWebTerm("b", 0, 0, "start", "x"),
		newWebTerm("c", 1, 0, "start", "x"),
	}

	// known only by component, so nothing ends their PIDs
	webTermMu.Lock()
	for _, term := range terms {
		webTermsExp[term.key] = term
	}
	webTermMu.Unlock()

	t.Cleanup(func() {
		webTermMu.Lock()
		for _, term := range terms {
			delete(webTermsExp, term.key)
		}
		webTermMu.Unlock()
	})

	for name, tc := range map[string]struct {
		exp  string
		run  int
		want []string
	}{
		"every terminal":     {exp: "", run: 5, want: []string{"a|0", "a|1", "b|0", "c|1"}},
		"one experiment":     {exp: "a", run: -1, want: []string{"a|0", "a|1"}},
		"one run":            {exp: "a", run: 1, want: []string{"a|1"}},
		"listed experiments": {exp: "a,c", run: -1, want: []string{"a|0", "a|1", "c|1"}},
		"run of two":         {exp: "b,c", run: 0, want: []string{"b|0"}},
		"unknown experiment": {exp: "d", run: -1, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := GetExperimentTerminals(tc.exp, tc.run)
			if err != nil {
				t.Fatalf("GetExperimentTerminals(%q, %d) error = %v", tc.exp, tc.run, err)
			}

			keys := make([]string, 0, len(got))
			for _, term := range got {
				keys = append(keys, fmt.Sprintf("%s|%d", term.Exp, term.Run))
			}

			opts := cmp.Options{cmpopts.SortSlices(func(a, b string) bool { return a < b }), cmpopts.EquateEmpty()}
			if diff := cmp.Diff(tc.want, keys, opts); diff != "" {
				t.Errorf("GetExperimentTerminals(%q, %d) mismatch (-want +got):\n%s", tc.exp, tc.run, diff)
			}
		})
	}
}
