package mmcli

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// holdShared holds the shared connection with a "hold" command, once minimega has it, until the
// returned unhold is called or the test ends; received lists the commands minimega has received.
func holdShared(t *testing.T) (func(), func() []mmtest.Command) {
	t.Helper()

	release := make(chan struct{})
	unhold := sync.OnceFunc(func() { close(release) })

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-release
		}

		return reply("ok")
	})

	held := make(chan error, 1)

	go func() {
		cmd := NewCommand()
		cmd.Command = "hold"

		held <- ErrorResponse(Run(cmd))
	}()

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() { <-held })
	t.Cleanup(unhold)

	mmtest.Await(t, "hold")

	return unhold, received
}

func command(name string) *Command {
	cmd := NewCommand()
	cmd.Command = name

	return cmd
}

// A command waiting behind another for the shared connection gives up, never
// sent, once its context is done.
func TestRunContextGivesUpWaiting(t *testing.T) {
	unhold, received := holdShared(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() { done <- ErrorResponse(RunContext(ctx, command("waiting"))) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrNotSent) {
			t.Errorf("RunContext returned %v, want %v and %v", err, context.Canceled, ErrNotSent)
		}
	case <-time.After(maxWait):
		t.Fatal("RunContext kept waiting after its context was canceled")
	}

	unhold()

	if err := ErrorResponse(Run(command("after"))); err != nil {
		t.Fatalf("the next command failed: %v", err)
	}

	if n := mmtest.Count(received(), "waiting"); n != 0 {
		t.Errorf("minimega received the command that gave up %d times, want 0", n)
	}
}

// The tabular context variants give up as RunContext does, with no rows; the checked one returns
// an error wrapping the context's and RunTabularContext logs it.
func TestRunTabularContextGivesUpWaiting(t *testing.T) {
	_, received := holdShared(t)

	t.Run("checked", func(t *testing.T) {
		rows := giveUp(t, func(ctx context.Context) []map[string]string {
			rows, err := RunTabularCheckedContext(ctx, command("checked"), nil)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("got error %v, want %v", err, context.Canceled)
			}

			return rows
		})

		if len(rows) != 0 {
			t.Errorf("got rows %v, want none", rows)
		}
	})

	t.Run("logged", func(t *testing.T) {
		if rows := giveUp(t, func(ctx context.Context) []map[string]string {
			return RunTabularContext(ctx, command("logged"))
		}); len(rows) != 0 {
			t.Errorf("got rows %v, want none", rows)
		}
	})

	for _, name := range []string{"checked", "logged"} {
		if n := mmtest.Count(received(), name); n != 0 {
			t.Errorf("minimega received the %s command that gave up %d times, want 0", name, n)
		}
	}
}

// giveUp runs run with a context canceled while it waits, and returns what
// run returned, failing the test if it keeps waiting.
func giveUp[T any](t *testing.T, run func(ctx context.Context) T) T { //nolint:ireturn // T is concrete at each use
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan T, 1)

	go func() { done <- run(ctx) }()

	cancel()

	select {
	case got := <-done:
		return got
	case <-time.After(maxWait):
		t.Fatal("the command kept waiting after its context was canceled")

		panic("unreachable")
	}
}

// A command whose context is already done is never sent, even when the
// shared connection is free.
func TestRunContextDoneSendsNothing(t *testing.T) {
	received := useFakeMinimega(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 20 {
		if err := ErrorResponse(RunContext(ctx, command("late"))); !errors.Is(err, context.Canceled) {
			t.Fatalf("RunContext returned %v, want %v", err, context.Canceled)
		}
	}

	if n := mmtest.Count(received(), "late"); n != 0 {
		t.Errorf("minimega received %d commands whose context was done, want 0", n)
	}
}

// A context's deadline ends the wait with an error that is both ErrTimeout,
// as Run's default gives, and the context's own error.
func TestRunContextDeadlineIsATimeout(t *testing.T) {
	holdShared(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	err := ErrorResponse(RunContext(ctx, command("waiting")))
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrNotSent) {
		t.Fatalf("RunContext returned %v, want %v, %v and %v", err, ErrTimeout, context.DeadlineExceeded, ErrNotSent)
	}

	_, err = RunTabularCheckedContext(ctx, command("tabular"), nil)
	if !errors.Is(err, ctx.Err()) {
		t.Fatalf("RunTabularCheckedContext returned %v, want %v", err, ctx.Err())
	}
}

// A context that is never done still waits no longer than DefaultWaitTimeout.
//
//nolint:paralleltest // shortens the default deadlines
func TestRunContextGivesUpWaitingAfterTheDefault(t *testing.T) {
	saved := DefaultWaitTimeout
	DefaultWaitTimeout = testTimeout

	t.Cleanup(func() { DefaultWaitTimeout = saved })

	holdShared(t)

	err := ErrorResponse(RunContext(context.Background(), command("waiting")))
	if !errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunContext returned %v, want %v alone", err, ErrTimeout)
	}
}

// A command holding the shared connection past DefaultCommandTimeout is abandoned with ErrTimeout,
// and the commands behind it run on a new connection while minimega still works on it.
//
//nolint:paralleltest // shortens the default deadlines
func TestRunAbandonsAHungCommand(t *testing.T) {
	saved := DefaultCommandTimeout
	DefaultCommandTimeout = testTimeout

	t.Cleanup(func() { DefaultCommandTimeout = saved })

	release := make(chan struct{})
	unhang := sync.OnceFunc(func() { close(release) })

	t.Cleanup(unhang)

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hang" {
			<-release
		}

		return reply("ok")
	})

	// sent, so not ErrNotSent
	if err := ErrorResponse(Run(command("hang"))); !errors.Is(err, ErrTimeout) || errors.Is(err, ErrNotSent) {
		t.Fatalf("Run returned %v, want %v alone", err, ErrTimeout)
	}

	done := make(chan string, 1)

	go func() {
		got, _ := SingleResponse(Run(command("next")))
		done <- got
	}()

	select {
	case got := <-done:
		if got != "ok" {
			t.Fatalf("next command got %q, want ok", got)
		}
	case <-time.After(maxWait):
		t.Fatal("the next command waited behind the hung one")
	}
}
