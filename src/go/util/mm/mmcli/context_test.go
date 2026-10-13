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

// holdShared runs a "hold" command on the shared connection until the
// returned function is called (or the test ends), and waits until minimega
// has it.
func holdShared(t *testing.T) func() {
	t.Helper()

	release := make(chan struct{})
	unhold := sync.OnceFunc(func() { close(release) })

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
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

	return unhold
}

func command(name string) *Command {
	cmd := NewCommand()
	cmd.Command = name

	return cmd
}

// A command waiting behind another for the shared connection gives up, never
// sent, once its context is done.
func TestRunContextGivesUpWaiting(t *testing.T) {
	unhold := holdShared(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() { done <- ErrorResponse(RunContext(ctx, command("waiting"))) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("RunContext returned %v, want %v", err, context.Canceled)
		}
	case <-time.After(maxWait):
		t.Fatal("RunContext kept waiting after its context was canceled")
	}

	unhold()

	if err := ErrorResponse(Run(command("after"))); err != nil {
		t.Fatalf("the next command failed: %v", err)
	}
}

// Without a context, a command gives up waiting for the shared connection
// after DefaultWaitTimeout.
//
//nolint:paralleltest // shortens the default deadlines
func TestRunGivesUpWaitingAfterTheDefault(t *testing.T) {
	saved := DefaultWaitTimeout
	DefaultWaitTimeout = testTimeout

	t.Cleanup(func() { DefaultWaitTimeout = saved })

	holdShared(t)

	if err := ErrorResponse(Run(command("waiting"))); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Run returned %v, want %v", err, ErrTimeout)
	}
}

// A command holding the shared connection past DefaultCommandTimeout is
// abandoned: its caller gets ErrTimeout, and the commands behind it run on a
// new connection while minimega still works on it.
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

	if err := ErrorResponse(Run(command("hang"))); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Run returned %v, want %v", err, ErrTimeout)
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
