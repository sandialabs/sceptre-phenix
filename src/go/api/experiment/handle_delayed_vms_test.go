//nolint:testpackage // testing internals
package experiment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-multierror"

	"phenix/util/mm"
)

// countingMM is a test double for mm.MM that only tracks calls to
// ClearNamespace. Embedding the (nil) mm.MM interface means any other method
// call will panic, which is fine since these tests never exercise them.
type countingMM struct {
	mm.MM

	mu                  sync.Mutex
	clearNamespaceCalls int
}

func (m *countingMM) ClearNamespace(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clearNamespaceCalls++

	return nil
}

func (m *countingMM) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.clearNamespaceCalls
}

// TestHandleDelayedVMsSkipsClearNamespaceOnContextCancellation verifies the
// fix for the race where stopping an experiment (which cancels the start
// context) while delayed VMs were still pending would cause
// handleDelayedVMs to call mm.ClearNamespace itself. That raced with the
// namespace teardown already being performed by the in-progress Stop call,
// which could delete resources (e.g. taps created by the tap app) out from
// under that cleanup. When the context is canceled, handleDelayedVMs should
// return the error without touching the namespace.
func TestHandleDelayedVMsSkipsClearNamespaceOnContextCancellation(t *testing.T) {
	fake := new(countingMM)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use a delay long enough that, were the context-cancellation check not
	// hit first, the test would hang rather than silently pass.
	delays := map[string]time.Duration{"host01": time.Minute}

	err := handleDelayedVMs(ctx, "test-ns", delays, nil)
	if err == nil {
		t.Fatal("expected an error when the context is canceled")
	}

	merr, ok := err.(*multierror.Error) //nolint:errorlint // need access to wrapped Errors slice
	if !ok {
		t.Fatalf("expected *multierror.Error, got %T", err)
	}

	var foundCanceled bool

	for _, e := range merr.Errors {
		if errors.Is(e, context.Canceled) {
			foundCanceled = true

			break
		}
	}

	if !foundCanceled {
		t.Fatalf("expected context.Canceled among returned errors, got: %v", merr.Errors)
	}

	if calls := fake.calls(); calls != 0 {
		t.Fatalf("expected ClearNamespace not to be called on context cancellation, called %d time(s)", calls)
	}
}

// TestHandleDelayedVMsNoDelaysOrC2sReturnsNilWithoutClearingNamespace is a
// sanity check that the early-return path for experiments with no delayed
// VMs never touches the namespace either.
func TestHandleDelayedVMsNoDelaysOrC2sReturnsNilWithoutClearingNamespace(t *testing.T) {
	fake := new(countingMM)

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double

	if err := handleDelayedVMs(context.Background(), "test-ns", nil, nil); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if calls := fake.calls(); calls != 0 {
		t.Fatalf("expected ClearNamespace not to be called, called %d time(s)", calls)
	}
}
