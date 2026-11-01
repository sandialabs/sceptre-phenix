package scorch

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

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
