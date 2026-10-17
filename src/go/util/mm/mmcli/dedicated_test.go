package mmcli

import (
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

// A dedicated command (for long-running work such as an experiment's script or
// launching its VMs) runs while another command holds the shared connection.
func TestRunDedicatedDoesNotWaitForSharedConnection(t *testing.T) {
	holdShared(t)

	done := make(chan string, 1)

	go func() {
		cmd := NewCommand()
		cmd.Command = "vm launch"

		got, _ := SingleResponse(RunDedicated(cmd))

		done <- got
	}()

	select {
	case got := <-done:
		if got != "ok" {
			t.Fatalf("dedicated command got %q, want %q", got, "ok")
		}
	case <-time.After(maxWait):
		t.Fatal("dedicated command waited for the shared connection")
	}
}

// RunStartedContext calls started just before sending the command, once it has
// stopped waiting behind other commands, on both connection types.
func TestRunStartedNotifiesBeforeSending(t *testing.T) {
	var (
		mu   sync.Mutex
		sent []string
	)

	started := make(chan struct{}, 2)

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		select {
		case <-started:
			mu.Lock()
			sent = append(sent, cmd.Base)
			mu.Unlock()
		default:
			t.Errorf("command %q sent before started was called", cmd.Base)
		}

		return reply("ok")
	})

	notify := func() { started <- struct{}{} }

	shared := NewCommand()
	shared.Command = "shared"

	if rows := RunTabularStartedContext(t.Context(), shared, notify); len(rows) != 0 {
		t.Fatalf("unexpected rows %v", rows)
	}

	private := NewCommand()
	private.Command = "private"
	private.Timeout = maxWait

	if err := ErrorResponse(RunStartedContext(t.Context(), private, notify)); err != nil {
		t.Fatalf("private command failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(sent) != 2 {
		t.Fatalf("sent %v, want both commands", sent)
	}
}
