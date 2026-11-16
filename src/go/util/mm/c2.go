package mm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"phenix/util"
)

const c2OptionPadding = 2

type GroupState struct {
	Msg string
	Err error

	Meta map[string]any
}

func NewGroupSuccess(msg string, meta map[string]any) GroupState {
	return GroupState{Msg: msg, Meta: meta} //nolint:exhaustruct // partial initialization
}

func NewGroupError(err error, meta map[string]any) GroupState {
	return GroupState{Err: err, Meta: meta} //nolint:exhaustruct // partial initialization
}

type StateGroup struct {
	sync.WaitGroup // embed

	mu sync.Mutex

	States   []GroupState
	ErrCount int
}

func (s *StateGroup) AddSuccess(msg string, meta map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.States = append(s.States, NewGroupSuccess(msg, meta))
}

func (s *StateGroup) AddError(err error, meta map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.States = append(s.States, NewGroupError(err, meta))
	s.ErrCount++
}

type C2RetryError struct {
	Delay time.Duration
}

func (C2RetryError) Error() string {
	return "retry"
}

// C2Limiter bounds how many C2 commands run at the same time. Each command
// polls minimega while it runs, so a large topology without a bound sends a
// burst of queries that all experiments on the cluster feel. A nil limiter
// sets no bound.
type C2Limiter struct {
	slots chan struct{}
}

// NewC2Limiter returns a limiter of n slots, or nil (no bound) when n is 0 or
// less.
func NewC2Limiter(n int) *C2Limiter {
	if n <= 0 {
		return nil
	}

	return &C2Limiter{slots: make(chan struct{}, n)}
}

// Acquire waits until a slot is free or ctx is done. A free slot comes first,
// as if there were no limiter.
func (l *C2Limiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}

	if err := ctx.Err(); err != nil {
		select {
		case l.slots <- struct{}{}:
			return nil
		default:
			return err
		}
	}

	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees a slot that Acquire got.
func (l *C2Limiter) Release() {
	if l == nil {
		return
	}

	<-l.slots
}

type C2ParallelCommand struct {
	Wait *StateGroup
	// Limiter, when set, bounds the commands that run at the same time. The
	// command holds a slot while it runs, but not while it waits to retry.
	Limiter *C2Limiter
	// Queued, when set, receives how long the command waited for a slot, each
	// time it gets one.
	Queued         func(time.Duration)
	Options        []C2Option
	Meta           map[string]any
	Expected       func(string) error
	ExpectedStdout func(string) error
	ExpectedStderr func(string) error
}

// ScheduleC2ParallelCommand runs cmd in the background. When an expected func
// asks for a retry, cmd runs again after the delay, without its slot meanwhile.
func ScheduleC2ParallelCommand(ctx context.Context, cmd *C2ParallelCommand) {
	cmd.Wait.Add(1)

	go func() {
		defer cmd.Wait.Done()

		delay, retry := cmd.run(ctx)
		if !retry {
			return
		}

		if err := util.SleepContext(ctx, delay); err != nil {
			return
		}

		ScheduleC2ParallelCommand(ctx, cmd)
	}()
}

// run runs cmd once in a limiter slot. It returns the delay and true when an
// expected func asks for a retry.
func (cmd *C2ParallelCommand) run(ctx context.Context) (time.Duration, bool) {
	queued := time.Now()

	if err := cmd.Limiter.Acquire(ctx); err != nil {
		cmd.Wait.AddError(fmt.Errorf("waiting to run C2 command: %w", err), cmd.Meta)

		return 0, false
	}

	defer cmd.Limiter.Release()

	if cmd.Queued != nil {
		cmd.Queued(time.Since(queued))
	}

	opts := make([]C2Option, 0, len(cmd.Options)+c2OptionPadding)
	opts = append(opts, cmd.Options...)
	opts = append(opts, C2Context(ctx), C2Wait())

	id, err := ExecC2Command(opts...)
	if err != nil {
		cmd.Wait.AddError(fmt.Errorf("executing C2 command: %w", err), cmd.Meta)

		return 0, false
	}

	opts = append(opts, C2CommandID(id))

	checks := []struct {
		what   string
		typ    []C2Option
		expect func(string) error
	}{
		{"response", nil, cmd.Expected},
		{"STDOUT response", []C2Option{C2ResponseTypeStdout()}, cmd.ExpectedStdout},
		{"STDERR response", []C2Option{C2ResponseTypeStderr()}, cmd.ExpectedStderr},
	}

	for _, c := range checks {
		if c.expect == nil {
			continue
		}

		opts = append(opts, c.typ...)

		delay, retry, ok := cmd.check(opts, c.what, c.expect)
		if retry || !ok {
			return delay, retry
		}
	}

	return 0, false
}

// check gets one response and gives it to expect. It returns the delay and
// true when expect asks for a retry, and ok false when the remaining checks
// must not run.
func (cmd *C2ParallelCommand) check(
	opts []C2Option,
	what string,
	expect func(string) error,
) (time.Duration, bool, bool) {
	resp, err := GetC2Response(opts...)
	if err != nil {
		cmd.Wait.AddError(fmt.Errorf("getting %s for C2 command: %w", what, err), cmd.Meta)

		return 0, false, false
	}

	err = expect(resp)
	if err == nil {
		return 0, false, true
	}

	var retry C2RetryError

	if errors.As(err, &retry) {
		return retry.Delay, true, false
	}

	cmd.Wait.AddError(err, cmd.Meta)

	return 0, false, true
}
