package util_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"phenix/util"
)

func TestSleepContext(t *testing.T) {
	t.Parallel()

	if err := util.SleepContext(context.Background(), time.Millisecond); err != nil {
		t.Errorf("SleepContext(background, 1ms) = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	if err := util.SleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("SleepContext(canceled, 1h) = %v, want %v", err, context.Canceled)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("SleepContext(canceled, 1h) returned after %s, want at once", elapsed)
	}
}
