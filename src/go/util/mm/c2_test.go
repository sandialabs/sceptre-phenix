package mm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingMM records how many C2 commands are in flight at once.
type countingMM struct {
	MM

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

func (m *countingMM) ExecC2Command(...C2Option) (string, error) {
	m.mu.Lock()
	m.inFlight++
	m.maxInFlight = max(m.maxInFlight, m.inFlight)
	m.mu.Unlock()

	time.Sleep(20 * time.Millisecond)

	m.mu.Lock()
	m.inFlight--
	m.mu.Unlock()

	return "1", nil
}

func (m *countingMM) GetC2Response(...C2Option) (string, error) { return "ok", nil }

func useFakeMM(t *testing.T, fake MM) {
	t.Helper()

	original := DefaultMM
	t.Cleanup(func() { DefaultMM = original })

	DefaultMM = fake
}

func TestC2LimiterBoundsParallelCommands(t *testing.T) {
	const (
		limit    = 2
		commands = 6
	)

	fake := new(countingMM)
	useFakeMM(t, fake)

	var (
		limiter = NewC2Limiter(limit)
		wg      = new(StateGroup)
	)

	for range commands {
		ScheduleC2ParallelCommand(context.Background(), &C2ParallelCommand{
			Wait:     wg,
			Limiter:  limiter,
			Expected: func(string) error { return nil },
		})
	}

	wg.Wait()

	if wg.ErrCount != 0 {
		t.Fatalf("%d commands failed: %v", wg.ErrCount, wg.States)
	}

	if fake.maxInFlight > limit {
		t.Fatalf("%d commands in flight at once, limit %d", fake.maxInFlight, limit)
	}
}

func TestC2LimiterHonoursCancellation(t *testing.T) {
	useFakeMM(t, new(countingMM))

	limiter := NewC2Limiter(1)

	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	defer limiter.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	wg := new(StateGroup)

	ScheduleC2ParallelCommand(ctx, &C2ParallelCommand{Wait: wg, Limiter: limiter})
	wg.Wait()

	if len(wg.States) != 1 || !errors.Is(wg.States[0].Err, context.Canceled) {
		t.Fatalf("states = %v, want one context.Canceled error", wg.States)
	}

	// A nil limiter imposes no bound.
	var unbounded *C2Limiter

	if err := unbounded.Acquire(ctx); err != nil {
		t.Fatalf("nil limiter Acquire: %v", err)
	}

	unbounded.Release()
}
