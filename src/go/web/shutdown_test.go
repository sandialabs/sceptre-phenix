package web

import (
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
