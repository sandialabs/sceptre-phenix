package mmcli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

const (
	// testTimeout is the timeout given to commands the fake never answers.
	testTimeout = 20 * time.Millisecond
	// maxWait bounds how long a command may take before the test gives up.
	maxWait = 5 * time.Second
)

// useFakeMinimega points the package at the fake minimega (see mmtest.Use),
// with a shared connection of its own.
func useFakeMinimega(t *testing.T, handle mmtest.Handler) {
	t.Helper()

	mmtest.Use(t, handle)

	resetConn()
	t.Cleanup(resetConn)
}

// resetConn drops the shared connection, so the next command dials anew.
func resetConn() {
	mu.Lock()
	defer mu.Unlock()

	if mm != nil {
		_ = mm.Close()
	}

	mm, mmDead = nil, false
}

// reply is the answer a healthy minimega sends.
func reply(body string) []*minicli.Response {
	return []*minicli.Response{mmtest.Text("fake", body)}
}

// A lost connection is an error, not an empty success: miniclient records the
// failure only in its own error field and closes the response channel having
// sent nothing. The next command redials.
func TestRunReportsLostConnectionAndRedials(t *testing.T) {
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "break" {
			return mmtest.Disconnect()
		}

		return reply("ok")
	})

	broken := NewCommand()
	broken.Command = "break"

	err := ErrorResponse(Run(broken))
	if err == nil {
		t.Fatal("ErrorResponse returned nil for a lost connection")
	}

	if !strings.Contains(err.Error(), "no response from minimega") &&
		!strings.Contains(err.Error(), "server disconnected") {
		t.Fatalf("unexpected error text: %v", err)
	}

	if _, err := SingleResponse(Run(broken)); err == nil {
		t.Fatal("SingleResponse returned nil error for a lost connection")
	}

	healthy := NewCommand()
	healthy.Command = "version"

	got, err := SingleResponse(Run(healthy))
	if err != nil {
		t.Fatalf("command after a lost connection failed instead of redialing: %v", err)
	}

	if got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}
}

// A command with a timeout runs on a connection of its own, so giving up on it
// cannot truncate the response of a command in flight on the shared one.
func TestRunTimesOutWithoutDisturbingOtherCommands(t *testing.T) {
	timedOut := make(chan struct{})

	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch cmd.Base {
		case "hang":
			<-t.Context().Done()
		case "slow":
			// Answer once the other command has timed out, so the two overlap.
			<-timedOut

			return reply("slow-ok")
		}

		return reply("ok")
	})

	type result struct {
		body string
		err  error
	}

	done := make(chan result, 1)

	go func() {
		slow := NewCommand()
		slow.Command = "slow"

		body, err := SingleResponse(Run(slow))

		done <- result{body: body, err: err}
	}()

	mmtest.Await(t, "slow")

	hang := NewCommand()
	hang.Command = "hang"
	hang.Timeout = testTimeout

	start := time.Now()
	err := ErrorResponse(Run(hang))
	elapsed := time.Since(start)

	close(timedOut)

	// errors.Is, not a substring match: the error crosses the response channel
	// as a plain string, so callers can only branch on the timeout if the
	// sentinel identity survives the round trip (see reconstructErr).
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected a timeout error, got %v", err)
	}

	if elapsed > maxWait {
		t.Fatalf("timeout took %v, want under %v", elapsed, maxWait)
	}

	got := <-done

	if got.err != nil {
		t.Fatalf("concurrent command failed because of an unrelated timeout: %v", got.err)
	}

	if got.body != "slow-ok" {
		t.Fatalf("concurrent command got %q, want %q: its response was truncated", got.body, "slow-ok")
	}
}

// errResponse flattens an error to a string to send it through a response
// channel; reconstructErr must turn phenix's own sentinels back into errors
// that match them, so callers can branch on a timeout or a lost connection.
func TestReconstructErrKeepsSentinelIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  string
		want error
	}{
		{"bare timeout", ErrTimeout.Error(), ErrTimeout},
		{"bare no-response", ErrNoResponse.Error(), ErrNoResponse},
		{
			"wrapped no-response",
			"running minimega command: " + ErrNoResponse.Error(),
			ErrNoResponse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := reconstructErr(tc.msg)

			if !errors.Is(got, tc.want) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", got, tc.want)
			}

			if got.Error() != tc.msg {
				t.Fatalf("message = %q, want %q", got.Error(), tc.msg)
			}
		})
	}

	// An unrelated message must stay an opaque error, not be coerced.
	if err := reconstructErr("vm not found: foo"); errors.Is(err, ErrTimeout) {
		t.Fatal("unrelated message matched ErrTimeout")
	}
}

// TestRunConcurrentCommands covers the serialization around the shared
// connection. mu is held across the whole exchange and released inside guard,
// which then calls markDead -- and markDead takes mu itself, so releasing in the
// wrong order deadlocks every caller.
func TestRunConcurrentCommands(t *testing.T) {
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		return reply(cmd.Raw)
	})

	const workers = 8

	var (
		wg   sync.WaitGroup
		errs = make(chan error, workers)
		done = make(chan struct{})
	)

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			cmd := &Command{Command: fmt.Sprintf("cmd-%d", i)}

			// the fake echoes the command it was sent, so a worker seeing
			// anything else was handed another worker's response
			want := cmd.String()

			var count int

			for resp := range Run(cmd) {
				if len(resp.Resp) == 0 {
					errs <- fmt.Errorf("%s got a response with no rows", want)

					return
				}

				if got := resp.Resp[0]; got.Response != want || got.Error != "" {
					errs <- fmt.Errorf("%s got response %q, error %q", want, got.Response, got.Error)

					return
				}

				count++
			}

			if count != 1 {
				errs <- fmt.Errorf("%s got %d responses, want 1", want, count)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(maxWait):
		t.Fatal("concurrent commands deadlocked")
	}

	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
