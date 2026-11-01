package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A negative grace period is refused before the server starts.
//
//nolint:paralleltest // sets the server options
func TestStartRefusesNegativeGracePeriod(t *testing.T) {
	saved := o
	t.Cleanup(func() { o = saved })

	err := Start(t.Context(), ServeWithShutdownGracePeriod(-time.Second))
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("Start(grace period -1s) = %v, want an error that it must not be negative", err)
	}
}

// A console started once endAll has run is ended at once, so none outlives
// the shutdown.
//
//nolint:paralleltest // swaps the consoles
func TestConsoleStartAfterEndAllIsRefused(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if left := registry.endAll(ctx, consoleEndedShutdown); left != 0 {
		t.Errorf("endAll() with no consoles = %d left, want 0", left)
	}

	if _, err := registry.start(context.Background(), "alice"); !errors.Is(err, errServerStopping) {
		t.Errorf("start() after endAll() error = %v, want %v", err, errServerStopping)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	if n := len(registry.sessions); n != 0 {
		t.Errorf("consoles after start() following endAll() = %d, want none", n)
	}
}
