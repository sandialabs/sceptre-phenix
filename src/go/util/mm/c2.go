package mm

import (
	"context"
	"errors"
	"fmt"
	"phenix/util"
	"sync"
	"time"
)

type GroupState struct {
	Msg string
	Err error

	Meta map[string]interface{}
}

func NewGroupSuccess(msg string, meta map[string]interface{}) GroupState {
	return GroupState{Msg: msg, Meta: meta}
}

func NewGroupError(err error, meta map[string]interface{}) GroupState {
	return GroupState{Err: err, Meta: meta}
}

func (this GroupState) Error() string {
	return this.Err.Error()
}

func (this GroupState) Unwrap() error {
	return this.Err
}

type StateGroup struct {
	sync.Mutex     // embed
	sync.WaitGroup // embed

	States   []GroupState
	ErrCount int
}

func (this *StateGroup) AddSuccess(msg string, meta map[string]interface{}) {
	this.Lock()
	defer this.Unlock()

	this.States = append(this.States, NewGroupSuccess(msg, meta))
}

func (this *StateGroup) AddError(err error, meta map[string]interface{}) {
	this.Lock()
	defer this.Unlock()

	this.States = append(this.States, NewGroupError(err, meta))
	this.ErrCount++
}

func (this *StateGroup) AddGroupState(state GroupState) {
	this.Lock()
	defer this.Unlock()

	this.States = append(this.States, state)

	if state.Err != nil {
		this.ErrCount++
	}
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
	Meta           map[string]interface{}
	Expected       func(string) error
	ExpectedStdout func(string) error
	ExpectedStderr func(string) error
}

func ScheduleC2ParallelCommand(ctx context.Context, cmd *C2ParallelCommand) {
	cmd.Wait.Add(1)

	go func() {
		defer cmd.Wait.Done()

		opts := append(cmd.Options, C2Context(ctx), C2Wait())

		id, err := ExecC2Command(opts...)
		if err != nil {
			cmd.Wait.AddError(fmt.Errorf("executing C2 command: %w", err), cmd.Meta)
			return
		}

		opts = append(cmd.Options, C2CommandID(id))

		if cmd.Expected != nil {
			resp, err := GetC2Response(opts...)
			if err != nil {
				cmd.Wait.AddError(fmt.Errorf("getting response for C2 command: %w", err), cmd.Meta)
				return
			}

			if err := cmd.Expected(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					if err := util.SleepContext(ctx, retry.Delay); err != nil {
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
				cmd.Wait.AddError(fmt.Errorf("getting STDOUT response for C2 command: %w", err), cmd.Meta)
				return
			}

			if err := cmd.ExpectedStdout(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					if err := util.SleepContext(ctx, retry.Delay); err != nil {
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
				cmd.Wait.AddError(fmt.Errorf("getting STDERR response for C2 command: %w", err), cmd.Meta)
				return
			}

			if err := cmd.ExpectedStderr(resp); err != nil {
				var retry C2RetryError

				if errors.As(err, &retry) {
					if err := util.SleepContext(ctx, retry.Delay); err != nil {
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
