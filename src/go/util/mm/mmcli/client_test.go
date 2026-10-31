package mmcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

const (
	// testTimeout is the timeout given to commands the fake never answers.
	testTimeout = 20 * time.Millisecond
	// maxWait bounds how long a command may take before the test gives up.
	maxWait = 5 * time.Second
)

// useFakeMinimega points the package at the fake minimega (see mmtest.Use) with a fresh shared
// connection, and returns mmtest.Use's listing of the commands received.
func useFakeMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	received := mmtest.Use(t, handle)

	resetConn()
	t.Cleanup(resetConn)

	return received
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

// A lost connection is an error, not an empty success; the next command redials.
func TestRunReportsLostConnectionAndRedials(t *testing.T) {
	useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "break" {
			return mmtest.Disconnect()
		}

		return reply("ok")
	})

	broken := NewCommand()
	broken.Command = "break"

	// miniclient reports a close it saw itself as "server disconnected", with no sentinel to match.
	err := ErrorResponse(Run(broken))
	if !errors.Is(err, ErrNoResponse) && (err == nil || !strings.Contains(err.Error(), "server disconnected")) {
		t.Errorf("ErrorResponse(Run(break)) = %v, want %v or miniclient's server disconnected", err, ErrNoResponse)
	}

	if _, err := SingleResponse(Run(broken)); err == nil {
		t.Error("SingleResponse(Run(break)) error = nil, want a lost connection")
	}

	healthy := NewCommand()
	healthy.Command = "version"

	got, err := SingleResponse(Run(healthy))
	if err != nil {
		t.Fatalf("SingleResponse(Run(version)) after a lost connection error = %v, want a redial", err)
	}

	if got != "ok" {
		t.Errorf("SingleResponse(Run(version)) after a lost connection = %q, want %q", got, "ok")
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

	// errors.Is, not a substring match: the sentinel must survive the response channel's
	// round trip as a string (see reconstructErr).
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("ErrorResponse(Run(hang, %v timeout)) = %v, want %v", testTimeout, err, ErrTimeout)
	}

	if elapsed > maxWait {
		t.Errorf("Run(hang, %v timeout) took %v, want under %v", testTimeout, elapsed, maxWait)
	}

	got := <-done

	if got.err != nil {
		t.Errorf("SingleResponse(Run(slow)) beside a timeout error = %v, want nil", got.err)
	}

	if got.body != "slow-ok" {
		t.Errorf("SingleResponse(Run(slow)) beside a timeout = %q, want %q", got.body, "slow-ok")
	}
}

// reconstructErr turns the strings errResponse sends back into errors matching phenix's own
// sentinels, so callers can branch on a timeout or a lost connection.
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

// genClientErr draws an error as this package reports one: a timeout, a wait given up, an
// ended stream, or minimega's own text.
func genClientErr() *rapid.Generator[error] {
	return rapid.Custom(func(t *rapid.T) error {
		text := rapid.StringMatching(`[a-z]{1,4}(: [a-z]{1,4})?`).Draw(t, "text")

		return rapid.SampledFrom([]error{
			ErrTimeout,
			gaveUp(nil),
			gaveUp(context.Canceled),
			gaveUp(context.DeadlineExceeded),
			fmt.Errorf("running minimega command: %w", ErrNoResponse),
			fmt.Errorf("running minimega command: %w", errors.New(text)),
			errors.New(text),
		}).Draw(t, "err")
	})
}

// An error flattened into a response and rebuilt by reconstructErr keeps its message and is
// each of phenix's sentinels exactly when the original was.
func TestReconstructErrProperties(t *testing.T) {
	t.Parallel()

	sentinels := []error{ErrTimeout, ErrNoResponse, ErrNotSent, context.Canceled, context.DeadlineExceeded}

	rapid.Check(t, func(t *rapid.T) {
		err := genClientErr().Draw(t, "err")
		got := reconstructErr(errResponse(err).Resp[0].Error)

		if got.Error() != err.Error() {
			t.Fatalf("reconstructErr(%q) message = %q, want the original", err, got)
		}

		for _, sentinel := range sentinels {
			if want := errors.Is(err, sentinel); errors.Is(got, sentinel) != want {
				t.Fatalf("errors.Is(reconstructErr(%q), %v) = %t, want %t", err, sentinel, !want, want)
			}
		}
	})
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
