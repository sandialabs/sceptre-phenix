//nolint:testpackage // testing internal asynchronous-start cleanup
package experiment

import (
	"context"
	"testing"
)

func TestStopAfterAsyncStartFailureSkipsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := stopAfterAsyncStartFailure(ctx, "not-in-store"); err != nil {
		t.Fatalf("stopAfterAsyncStartFailure() error = %v, want nil", err)
	}
}
