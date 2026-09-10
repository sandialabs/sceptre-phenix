package web

import (
	"context"
	"slices"
	"testing"
)

// TestTakeCancelersIncludesTriggeredApps: stopping an experiment must also
// cancel app running stages triggered from the UI, which register under
// "<exp>/<app>" rather than under the experiment name.
func TestTakeCancelersIncludesTriggeredApps(t *testing.T) {
	var called []string

	record := func(key string) context.CancelFunc {
		return func() { called = append(called, key) }
	}

	commonMu.Lock()

	for _, key := range []string{"exp", "exp/soh", "exp/other", "exp2/soh", "exp-two"} {
		cancelers[key] = []context.CancelFunc{record(key)}
	}

	commonMu.Unlock()

	cancels, _ := takeCancelers("exp")

	for _, cancel := range cancels {
		cancel()
	}

	slices.Sort(called)

	if want := []string{"exp", "exp/other", "exp/soh"}; !slices.Equal(called, want) {
		t.Fatalf("cancelled %v, want %v", called, want)
	}

	commonMu.Lock()
	defer commonMu.Unlock()

	for _, key := range []string{"exp2/soh", "exp-two"} {
		if _, ok := cancelers[key]; !ok {
			t.Fatalf("canceler %q for another experiment was removed", key)
		}

		delete(cancelers, key)
	}

	for _, key := range []string{"exp", "exp/soh", "exp/other"} {
		if _, ok := cancelers[key]; ok {
			t.Fatalf("canceler %q was not removed", key)
		}
	}
}
