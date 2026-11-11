package audit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/web/audit"
	"phenix/web/middleware"
)

// Events logged from many goroutines at once each reach the log whole, as one
// JSON record.
func TestEventsConcurrent(t *testing.T) {
	const (
		goroutines = 16
		calls      = 50
	)

	events := capture(t)

	var wg sync.WaitGroup

	for g := range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range calls {
				r := httptest.NewRequest(http.MethodDelete, "/api/v1/experiments/exp", nil)
				audit.Success(r, audit.ExperimentDelete, "deleted experiment", audit.Fields{
					Experiment: fmt.Sprintf("exp-%d-%d", g, i),
				})
			}
		}()
	}

	wg.Wait()

	seen := make(map[string]int)

	for _, record := range decoded(t, events()) {
		name, _ := record["phenix.experiment.name"].(string)
		seen[name]++
	}

	want := make(map[string]int, goroutines*calls)

	for g := range goroutines {
		for i := range calls {
			want[fmt.Sprintf("exp-%d-%d", g, i)] = 1
		}
	}

	if diff := cmp.Diff(want, seen); diff != "" {
		t.Errorf("concurrent audit records mismatch (-want +got):\n%s", diff)
	}
}

// Refusals from many goroutines at once are audited once a window for each
// source, and the next audited refusal counts every one skipped.
func TestRefusalsConcurrent(t *testing.T) {
	const (
		sources    = 8
		goroutines = 32
		calls      = 100
	)

	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRefusals(time.Minute, 2*sources, clock.Now)
		wg     sync.WaitGroup
	)

	for g := range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range calls {
				ip := fmt.Sprintf("192.0.2.%d", (g+i)%sources+1)
				limit.Refused(refusedRequest(ip), verified(middleware.RefusedExpired, ""))
			}
		}()
	}

	wg.Wait()

	if n := len(events()); n != sources {
		t.Fatalf("%d refusals from %d sources in one window audited %d times, want %d", goroutines*calls, sources, n, sources)
	}

	clock.advance(time.Minute)

	for s := range sources {
		limit.Refused(refusedRequest(fmt.Sprintf("192.0.2.%d", s+1)), verified(middleware.RefusedExpired, ""))
	}

	var suppressed float64

	for _, record := range decoded(t, events())[sources:] {
		n, _ := record["phenix.suppressed"].(float64)
		suppressed += n
	}

	if want := float64(goroutines*calls - sources); suppressed != want {
		t.Errorf("phenix.suppressed totals %v after the window, want %v", suppressed, want)
	}
}

// Attaches from many goroutines at once are audited once a window for each
// user and VM, and the next audited attach counts every one skipped.
func TestRepeatsConcurrent(t *testing.T) {
	const (
		vms        = 8
		goroutines = 32
		calls      = 100
	)

	var (
		events = capture(t)
		clock  = &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		limit  = audit.NewRepeats(5*time.Minute, time.Minute, 2*vms, clock.Now)
		wg     sync.WaitGroup
		attach = func(vm int) {
			limit.Success(attachRequest("alice"), audit.VNCAttach, "vnc attached", audit.Fields{VM: fmt.Sprintf("vm%d", vm)})
		}
	)

	for g := range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range calls {
				attach((g + i) % vms)
			}
		}()
	}

	wg.Wait()

	if n := len(events()); n != vms {
		t.Fatalf("%d attaches to %d VMs in one window audited %d times, want %d", goroutines*calls, vms, n, vms)
	}

	clock.advance(5 * time.Minute)

	for vm := range vms {
		attach(vm)
	}

	var suppressed float64

	for _, record := range decoded(t, events())[vms:] {
		n, _ := record["phenix.suppressed"].(float64)
		suppressed += n
	}

	if want := float64(goroutines*calls - vms); suppressed != want {
		t.Errorf("phenix.suppressed totals %v after the window, want %v", suppressed, want)
	}
}
