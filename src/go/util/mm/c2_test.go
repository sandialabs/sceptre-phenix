package mm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// parallelMM answers each C2 command with the next item of answers for that
// command. GetC2Response gives the answer of the command ID.
type parallelMM struct {
	MM

	mu       sync.Mutex
	answers  map[string][]string
	execErr  error
	respErr  error
	execs    []string
	sent     map[string]string // answer by command ID
	gotTypes []C2ResponseType  // the response type of each GetC2Response
}

func (m *parallelMM) ExecC2Command(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.execs = append(m.execs, o.command)

	if m.execErr != nil {
		return "", m.execErr
	}

	answer := ""
	if list := m.answers[o.command]; len(list) > 0 {
		answer, m.answers[o.command] = list[0], list[1:]
	}

	id := strconv.Itoa(len(m.execs))
	m.sent[id] = answer

	return id, nil
}

func (m *parallelMM) GetC2Response(opts ...C2Option) (string, error) {
	o := NewC2Options(opts...)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.gotTypes = append(m.gotTypes, o.responseType)

	if m.respErr != nil {
		return "", m.respErr
	}

	return m.sent[o.commandID], nil
}

func (m *parallelMM) commands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string(nil), m.execs...)
}

// useParallelMM makes DefaultMM a parallelMM until the test ends.
func useParallelMM(t *testing.T, answers map[string][]string) *parallelMM {
	t.Helper()

	fake := &parallelMM{answers: answers, sent: map[string]string{}}
	saved := DefaultMM

	DefaultMM = fake

	t.Cleanup(func() { DefaultMM = saved })

	return fake
}

func TestNewC2Limiter(t *testing.T) {
	t.Parallel()

	for _, n := range []int{-1, 0} {
		if l := NewC2Limiter(n); l != nil {
			t.Errorf("NewC2Limiter(%d) = %v, want nil (no bound)", n, l)
		}
	}

	var unbounded *C2Limiter

	// A nil limiter never blocks.
	for range 3 {
		if err := unbounded.Acquire(t.Context()); err != nil {
			t.Errorf("nil limiter Acquire() error = %v", err)
		}
	}

	unbounded.Release()

	l := NewC2Limiter(2)

	for range 2 {
		if err := l.Acquire(t.Context()); err != nil {
			t.Fatalf("Acquire() of a free slot error = %v", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Acquire() with no free slot and a canceled context = %v, want %v", err, context.Canceled)
	}

	expiring, cancelExpiring := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancelExpiring()

	if err := l.Acquire(expiring); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Acquire() with no free slot until the context ends = %v, want %v", err, context.DeadlineExceeded)
	}

	l.Release()

	if err := l.Acquire(ctx); err != nil {
		t.Errorf("Acquire() of a released slot with a canceled context error = %v, want the free slot", err)
	}
}

// A command that waits to retry frees its limiter slot, so the other commands
// do not wait for the retry delay.
func TestScheduleC2ParallelCommandFreesItsSlotWhileItWaitsToRetry(t *testing.T) { //nolint:paralleltest // replaces DefaultMM
	useParallelMM(t, map[string][]string{"slow": {"not yet"}, "fast": {"ok"}})

	var (
		limiter       = NewC2Limiter(1)
		slowCtx, stop = context.WithCancel(t.Context())
		slow, fast    StateGroup
		retried       = make(chan struct{})
		fastRan       = make(chan struct{})
	)

	ScheduleC2ParallelCommand(slowCtx, &C2ParallelCommand{
		Wait:    &slow,
		Limiter: limiter,
		Options: []C2Option{C2Command("slow")},
		Expected: func(resp string) error {
			if resp == "not yet" {
				close(retried)

				return C2RetryError{Delay: time.Hour}
			}

			return nil
		},
	})

	<-retried

	ScheduleC2ParallelCommand(t.Context(), &C2ParallelCommand{
		Wait:     &fast,
		Limiter:  limiter,
		Options:  []C2Option{C2Command("fast")},
		Expected: func(string) error { close(fastRan); return nil },
	})

	select {
	case <-fastRan:
	case <-time.After(time.Minute):
		t.Error("a command did not run while the only slot's command waited an hour to retry")
	}

	stop()
	slow.Wait()
	fast.Wait()

	if slow.ErrCount != 0 || len(slow.States) != 0 {
		t.Errorf("command stopped while it waited to retry recorded %+v, want nothing", slow.States)
	}
}

// Queued receives the wait for a slot each time the command runs, and a
// context that ends while the command waits for a slot is its error.
func TestScheduleC2ParallelCommandQueued(t *testing.T) { //nolint:paralleltest // replaces DefaultMM
	fake := useParallelMM(t, map[string][]string{"ip": {"none", "10.0.0.1"}})

	var (
		limiter = NewC2Limiter(1)
		wg      StateGroup
		mu      sync.Mutex
		waits   []time.Duration
	)

	ScheduleC2ParallelCommand(t.Context(), &C2ParallelCommand{
		Wait:    &wg,
		Limiter: limiter,
		Queued: func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()

			waits = append(waits, d)
		},
		Options: []C2Option{C2Command("ip")},
		Expected: func(resp string) error {
			if resp == "none" {
				return C2RetryError{Delay: time.Millisecond}
			}

			wg.AddSuccess("set", nil)

			return nil
		},
	})

	wg.Wait()

	if len(waits) != 2 {
		t.Errorf("Queued called %d times for a command that ran twice, want 2", len(waits))
	}

	if diff := cmp.Diff([]string{"ip", "ip"}, fake.commands()); diff != "" {
		t.Errorf("commands sent mismatch (-want +got):\n%s", diff)
	}

	if err := limiter.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire() after the command ended error = %v, want its slot free", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var blocked StateGroup

	ScheduleC2ParallelCommand(ctx, &C2ParallelCommand{
		Wait: &blocked, Limiter: limiter, Options: []C2Option{C2Command("ip")}, Meta: map[string]any{"host": "a"},
	})
	blocked.Wait()

	if len(blocked.States) != 1 || !errors.Is(blocked.States[0].Err, context.Canceled) {
		t.Errorf("command that never got a slot recorded %+v, want a %v error", blocked.States, context.Canceled)
	}
}

// Each expected func gets its own response type, an error stops the
// remaining checks only when no response comes, and a retry runs every check
// again.
func TestScheduleC2ParallelCommandChecks(t *testing.T) { //nolint:paralleltest // replaces DefaultMM
	var (
		errBad    = errors.New("bad output")
		errGone   = errors.New("gone")
		errClient = errors.New("no client")
	)

	for name, tc := range map[string]struct {
		execErr, respErr error
		stdout, stderr   func(string) error
		wantTypes        []C2ResponseType
		wantErrs         []error
	}{
		"both checks run": {
			stdout:    func(string) error { return errBad },
			stderr:    func(string) error { return nil },
			wantTypes: []C2ResponseType{C2ResponseBoth, C2ResponseStdout, C2ResponseStderr},
			wantErrs:  []error{errBad},
		},
		"no response stops the checks": {
			respErr:   errGone,
			stdout:    func(string) error { return nil },
			stderr:    func(string) error { return nil },
			wantTypes: []C2ResponseType{C2ResponseBoth},
			wantErrs:  []error{errGone},
		},
		"exec error": {
			execErr:  errClient,
			stdout:   func(string) error { return nil },
			wantErrs: []error{errClient},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := useParallelMM(t, nil)
			fake.execErr, fake.respErr = tc.execErr, tc.respErr

			var wg StateGroup

			ScheduleC2ParallelCommand(t.Context(), &C2ParallelCommand{
				Wait:           &wg,
				Options:        []C2Option{C2Command("run")},
				Expected:       func(string) error { return nil },
				ExpectedStdout: tc.stdout,
				ExpectedStderr: tc.stderr,
			})
			wg.Wait()

			if len(wg.States) != len(tc.wantErrs) {
				t.Fatalf("recorded %+v, want errors %v", wg.States, tc.wantErrs)
			}

			for i, st := range wg.States {
				if !errors.Is(st.Err, tc.wantErrs[i]) {
					t.Errorf("recorded error %d = %v, want %v", i, st.Err, tc.wantErrs[i])
				}
			}

			if diff := cmp.Diff(tc.wantTypes, fake.gotTypes); diff != "" {
				t.Errorf("response types read mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("retry from stdout", func(t *testing.T) {
		fake := useParallelMM(t, nil)

		var (
			wg        StateGroup
			tries     int
			errStderr = errors.New("stderr check")
		)

		ScheduleC2ParallelCommand(t.Context(), &C2ParallelCommand{
			Wait:    &wg,
			Options: []C2Option{C2Command("run")},
			ExpectedStdout: func(string) error {
				if tries++; tries < 2 {
					return C2RetryError{Delay: time.Millisecond}
				}

				return nil
			},
			ExpectedStderr: func(string) error { return fmt.Errorf("try %d: %w", tries, errStderr) },
		})
		wg.Wait()

		if diff := cmp.Diff([]string{"run", "run"}, fake.commands()); diff != "" {
			t.Errorf("commands sent mismatch (-want +got):\n%s", diff)
		}

		// The first run stops at its retry, so only the second run checks stderr.
		if len(wg.States) != 1 || !errors.Is(wg.States[0].Err, errStderr) || tries != 2 {
			t.Errorf("recorded %+v after %d stdout checks, want one stderr error after 2", wg.States, tries)
		}
	})
}
