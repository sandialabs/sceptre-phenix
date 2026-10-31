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

type C2ParallelCommand struct {
	Wait           *StateGroup
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

		opts := make([]C2Option, 0, len(cmd.Options)+c2OptionPadding)
		opts = append(opts, cmd.Options...)
		opts = append(opts, C2Context(ctx), C2Wait())

		id, err := ExecC2Command(opts...)
		if err != nil {
			cmd.Wait.AddError(fmt.Errorf("executing C2 command: %w", err), cmd.Meta)

			return
		}

		opts = append(opts, C2CommandID(id))

		if cmd.Expected != nil {
			resp, err := GetC2Response(opts...)
			if err != nil {
				cmd.Wait.AddError(fmt.Errorf("getting response for C2 command: %w", err), cmd.Meta)

				return
			}

			if err := cmd.Expected(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					err := util.SleepContext(ctx, retry.Delay)
					if err != nil {
						return
					}

					ScheduleC2ParallelCommand(ctx, cmd)
				} else {
					cmd.Wait.AddError(err, cmd.Meta)
				}
			}
		}

		if cmd.ExpectedStdout != nil {
			opts = append(opts, C2ResponseTypeStdout())

			resp, err := GetC2Response(opts...)
			if err != nil {
				cmd.Wait.AddError(
					fmt.Errorf("getting STDOUT response for C2 command: %w", err),
					cmd.Meta,
				)

				return
			}

			if err := cmd.ExpectedStdout(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					err := util.SleepContext(ctx, retry.Delay)
					if err != nil {
						return
					}

					ScheduleC2ParallelCommand(ctx, cmd)
				} else {
					cmd.Wait.AddError(err, cmd.Meta)
				}
			}
		}

		if cmd.ExpectedStderr != nil {
			opts = append(opts, C2ResponseTypeStderr())

			resp, err := GetC2Response(opts...)
			if err != nil {
				cmd.Wait.AddError(
					fmt.Errorf("getting STDERR response for C2 command: %w", err),
					cmd.Meta,
				)

				return
			}

			if err := cmd.ExpectedStderr(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					err := util.SleepContext(ctx, retry.Delay)
					if err != nil {
						return
					}

					ScheduleC2ParallelCommand(ctx, cmd)
				} else {
					cmd.Wait.AddError(err, cmd.Meta)
				}
			}
		}
	}()
}
