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

type GroupStateError struct {
	Msg string
	Err error

	Meta map[string]any
}

func NewGroupSuccess(msg string, meta map[string]any) GroupStateError {
	return GroupStateError{Msg: msg, Meta: meta} //nolint:exhaustruct // partial initialization
}

func NewGroupError(err error, meta map[string]any) GroupStateError {
	return GroupStateError{Err: err, Meta: meta} //nolint:exhaustruct // partial initialization
}

func (g GroupStateError) Error() string {
	return g.Err.Error()
}

func (g GroupStateError) Unwrap() error {
	return g.Err
}

type StateGroup struct {
	sync.WaitGroup // embed

	mu sync.Mutex

	States   []GroupStateError
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

func (s *StateGroup) AddGroupStateError(state GroupStateError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.States = append(s.States, state)

	if state.Err != nil {
		s.ErrCount++
	}
}

type C2RetryError struct {
	Delay time.Duration
}

func (C2RetryError) Error() string {
	return "retry"
}

// C2Limiter bounds how many C2 commands run at once. Every command polls
// minimega for as long as it is in flight, so an unbounded fan-out over a large
// topology is a query storm every experiment on the cluster feels. A nil
// limiter imposes no bound.
type C2Limiter struct {
	slots chan struct{}
}

func NewC2Limiter(n int) *C2Limiter {
	if n <= 0 {
		return nil
	}

	return &C2Limiter{slots: make(chan struct{}, n)}
}

// Acquire blocks until a slot is free or ctx is done.
func (l *C2Limiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}

	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *C2Limiter) Release() {
	if l == nil {
		return
	}

	<-l.slots
}

type C2ParallelCommand struct {
	Wait           *StateGroup
	Limiter        *C2Limiter
	Options        []C2Option
	Meta           map[string]any
	Expected       func(string) error
	ExpectedStdout func(string) error
	ExpectedStderr func(string) error
}

func ScheduleC2ParallelCommand(ctx context.Context, cmd *C2ParallelCommand) {
	cmd.Wait.Add(1)

	go func() {
		defer cmd.Wait.Done()

		if err := cmd.Limiter.Acquire(ctx); err != nil {
			cmd.Wait.AddError(err, cmd.Meta)

			return
		}

		defer cmd.Limiter.Release()

		opts := make([]C2Option, 0, len(cmd.Options)+c2OptionPadding)
		opts = append(opts, cmd.Options...)
		opts = append(opts, C2Context(ctx), C2Wait())

		id, err := ExecC2Command(opts...)
		if err != nil {
			cmd.Wait.AddError(fmt.Errorf("executing C2 command: %w", err), cmd.Meta)

			return
		}

		opts = append(opts, C2CommandID(id))

		if cmd.Expected != nil && !cmd.check(ctx, opts, "response", cmd.Expected) {
			return
		}

		if cmd.ExpectedStdout != nil {
			opts = append(opts, C2ResponseTypeStdout())

			if !cmd.check(ctx, opts, "STDOUT response", cmd.ExpectedStdout) {
				return
			}
		}

		if cmd.ExpectedStderr != nil {
			opts = append(opts, C2ResponseTypeStderr())

			if !cmd.check(ctx, opts, "STDERR response", cmd.ExpectedStderr) {
				return
			}
		}
	}()
}

// check fetches one response and evaluates it with expect, rescheduling the
// whole command on a C2RetryError. It reports whether to carry on with the
// remaining checks.
func (cmd *C2ParallelCommand) check(ctx context.Context, opts []C2Option, what string, expect func(string) error) bool {
	resp, err := GetC2Response(opts...)
	if err != nil {
		cmd.Wait.AddError(fmt.Errorf("getting %s for C2 command: %w", what, err), cmd.Meta)

		return false
	}

	err = expect(resp)
	if err == nil {
		return true
	}

	var retry C2RetryError

	if !errors.As(err, &retry) {
		cmd.Wait.AddError(err, cmd.Meta)

		return true
	}

	if err := util.SleepContext(ctx, retry.Delay); err != nil {
		return false
	}

	ScheduleC2ParallelCommand(ctx, cmd)

	return true
}
