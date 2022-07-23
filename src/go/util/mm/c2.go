package mm

import (
	"context"
	"errors"
	"fmt"
	"phenix/util"
	"sync"
	"time"
)

type GroupError struct {
	Err  error
	Meta map[string]interface{}
}

func NewGroupError(err error, meta map[string]interface{}) GroupError {
	return GroupError{Err: err, Meta: meta}
}

func (this GroupError) Error() string {
	return this.Err.Error()
}

func (this GroupError) Unwrap() error {
	return this.Err
}

type ErrGroup struct {
	sync.Mutex     // embed
	sync.WaitGroup // embed

	Errors []GroupError
}

func (this *ErrGroup) AddError(err error, meta map[string]interface{}) {
	this.Lock()
	defer this.Unlock()

	this.Errors = append(this.Errors, NewGroupError(err, meta))
}

func (this *ErrGroup) AddGroupError(err GroupError) {
	this.Lock()
	defer this.Unlock()

	this.Errors = append(this.Errors, err)
}

type C2RetryError struct {
	Delay time.Duration
}

func (C2RetryError) Error() string {
	return "retry"
}

type C2ParallelCommand struct {
	Wait           *ErrGroup
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
