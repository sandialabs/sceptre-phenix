package mmcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/common"
)

const (
	// testTimeout is short enough to keep the suite fast but long enough that a
	// loaded CI box won't trip it spuriously.
	testTimeout = 200 * time.Millisecond
	// maxWait bounds how long a timeout-bearing command may take to return.
	maxWait = 5 * time.Second
	// slowReply holds a reply open well past testTimeout so a slow command and a
	// timing-out command genuinely overlap.
	slowReply = 4 * testTimeout
)

// fakeAction selects how the fake minimega answers a command.
type fakeAction int

const (
	// actionReply sends the supplied response.
	actionReply fakeAction = iota
	// actionHang answers nothing and leaves the connection open, modelling a
	// command minimega never gets around to answering.
	actionHang
	// actionClose drops the connection without answering, modelling a lost
	// socket or a restarted minimega.
	actionClose
	// actionStatusReply sends a progress frame before the supplied response, as
	// minimega does while an iomeshage transfer is in flight.
	actionStatusReply
)

type fakeHandler func(cmd string) (*miniclient.Response, fakeAction)

// reply is the response a healthy fake minimega sends.
func reply(body string) *miniclient.Response {
	return &miniclient.Response{
		Resp: minicli.Responses{{Host: "fake", Response: body}},
	}
}

// newFakeMinimega starts a fake minimega command socket in a temporary
// directory and returns that directory, suitable for common.MinimegaBase.
func newFakeMinimega(t *testing.T, handle fakeHandler) string {
	t.Helper()

	dir := t.TempDir()

	listener, err := net.Listen("unix", filepath.Join(dir, "minimega"))
	if err != nil {
		t.Fatalf("listening on fake minimega socket: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			// One goroutine per connection: commands carrying a timeout now dial
			// a connection of their own, so more than one is live at a time.
			go serveFake(conn, handle)
		}
	}()

	return dir
}

func serveFake(conn net.Conn, handle fakeHandler) {
	defer func() { _ = conn.Close() }()

	var (
		dec = json.NewDecoder(conn)
		enc = json.NewEncoder(conn)
	)

	for {
		var req miniclient.Request

		if err := dec.Decode(&req); err != nil {
			return
		}

		resp, action := handle(req.Command)

		switch action {
		case actionClose:
			return
		case actionHang:
			continue
		case actionReply:
			if err := enc.Encode(resp); err != nil {
				return
			}
		case actionStatusReply:
			status := &miniclient.Response{
				Rendered: "transferring file disk.qc2: 50.000000% complete",
				Status:   true,
			}

			if err := enc.Encode(status); err != nil {
				return
			}

			if err := enc.Encode(resp); err != nil {
				return
			}
		}
	}
}

// useFakeMinimega points the package at the given fake socket directory and
// resets the shared connection, restoring both afterwards.
func useFakeMinimega(t *testing.T, dir string) {
	t.Helper()

	prev := common.MinimegaBase
	common.MinimegaBase = dir //nolint:reassign // test fixture

	reset := func() {
		mu.Lock()
		defer mu.Unlock()

		if mm != nil {
			_ = mm.Close()
		}

		mm, mmDead = nil, false
	}

	reset()

	t.Cleanup(func() {
		reset()

		common.MinimegaBase = prev //nolint:reassign // test fixture
	})
}

func TestRunDeliversResponses(t *testing.T) {
	dir := newFakeMinimega(t, func(string) (*miniclient.Response, fakeAction) {
		return reply("pong"), actionReply
	})

	useFakeMinimega(t, dir)

	cmd := NewCommand()
	cmd.Command = "version"

	got, err := SingleResponse(Run(cmd))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "pong" {
		t.Fatalf("got %q, want %q", got, "pong")
	}
}

// TestRunSkipsStatusFrames: minimega publishes progress frames (Status set,
// More unset) while an iomeshage transfer is in flight. A client that reads one
// as the final response returns nothing for that command and leaves the real
// reply on the socket, where the next command on the shared connection picks
// it up.
func TestRunSkipsStatusFrames(t *testing.T) {
	dir := newFakeMinimega(t, func(cmd string) (*miniclient.Response, fakeAction) {
		return reply(cmd), actionStatusReply
	})

	useFakeMinimega(t, dir)

	for _, command := range []string{"vm launch", "vm info"} {
		cmd := NewCommand()
		cmd.Command = command

		got, err := SingleResponse(Run(cmd))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", command, err)
		}

		if got != cmd.String() {
			t.Fatalf("%s: got %q, want %q", command, got, cmd.String())
		}
	}
}

// TestRunSurfacesLostConnection is the regression test for a lost connection
// being reported as a successful empty result. miniclient records the failure
// only in its own private error field and closes the response channel having
// sent nothing, which every helper here used to read as success: ErrorResponse
// returned nil, SingleResponse returned ("", nil), and RunTabular returned an
// empty slice with nothing logged.
func TestRunSurfacesLostConnection(t *testing.T) {
	dir := newFakeMinimega(t, func(string) (*miniclient.Response, fakeAction) {
		return nil, actionClose
	})

	useFakeMinimega(t, dir)

	cmd := NewCommand()
	cmd.Command = "version"

	err := ErrorResponse(Run(cmd))
	if err == nil {
		t.Fatal("ErrorResponse returned nil for a lost connection")
	}

	// ErrorResponse rebuilds errors with errors.New, so the sentinel identity is
	// not preserved across the channel -- match on the text instead.
	if !strings.Contains(err.Error(), "no response from minimega") &&
		!strings.Contains(err.Error(), "server disconnected") {
		t.Fatalf("unexpected error text: %v", err)
	}

	cmd = NewCommand()
	cmd.Command = "version"

	if _, err := SingleResponse(Run(cmd)); err == nil {
		t.Fatal("SingleResponse returned nil error for a lost connection")
	}
}

// TestRunRedialsAfterConnectionLoss covers guard marking the connection dead and
// conn() redialing on any sticky error. A restarted minimega reports "server
// disconnected", which the old substring list did not match, wedging phenix
// until the process itself was restarted.
func TestRunRedialsAfterConnectionLoss(t *testing.T) {
	dir := newFakeMinimega(t, func(cmd string) (*miniclient.Response, fakeAction) {
		if strings.Contains(cmd, "break") {
			return nil, actionClose
		}

		return reply("ok"), actionReply
	})

	useFakeMinimega(t, dir)

	broken := NewCommand()
	broken.Command = "break"

	if err := ErrorResponse(Run(broken)); err == nil {
		t.Fatal("expected the connection-losing command to report an error")
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

func TestRunWithTimeout(t *testing.T) {
	dir := newFakeMinimega(t, func(string) (*miniclient.Response, fakeAction) {
		return nil, actionHang
	})

	useFakeMinimega(t, dir)

	cmd := NewCommand()
	cmd.Command = "version"
	cmd.Timeout = testTimeout

	start := time.Now()
	err := ErrorResponse(Run(cmd))
	elapsed := time.Since(start)

	// errors.Is, not a substring match: the error crosses the response channel
	// as a plain string, so callers can only branch on the timeout if the
	// sentinel identity survives the round trip (see reconstructErr).
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected a timeout error, got %v", err)
	}

	if elapsed > maxWait {
		t.Fatalf("timeout took %v, want under %v", elapsed, maxWait)
	}
}

// TestTimeoutDoesNotDisturbConcurrentCommands locks in the property that made
// the timeout path worth changing: one command's deadline must not damage
// another command that is already in flight. The old code Close()d the shared
// *miniclient.Conn, so a concurrent reader's stream was truncated and came back
// empty with no error -- which is how a healthy cluster ended up with
// Headnode() == "" and cluster commands silently running on the wrong node.
// Timeout-bearing commands now run on a connection of their own.
//
// Note this asserts the desired property rather than reproducing the historical
// failure exactly: the old code also serialized everything through conn(), so no
// single test discriminates against every past variant. It does catch the
// obvious naive fix of keeping the shared connection and still closing it.
func TestTimeoutDoesNotDisturbConcurrentCommands(t *testing.T) {
	dir := newFakeMinimega(t, func(cmd string) (*miniclient.Response, fakeAction) {
		switch {
		case strings.Contains(cmd, "hang"):
			return nil, actionHang
		case strings.Contains(cmd, "slow"):
			// Hold this connection's reply open past the other command's
			// deadline so the two genuinely overlap.
			time.Sleep(slowReply)

			return reply("slow-ok"), actionReply
		}

		return reply("ok"), actionReply
	})

	useFakeMinimega(t, dir)

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

	// Let the slow command take the shared connection first.
	time.Sleep(testTimeout / 2)

	hang := NewCommand()
	hang.Command = "hang"
	hang.Timeout = testTimeout

	if err := ErrorResponse(Run(hang)); err == nil {
		t.Fatal("expected the hung command to time out")
	}

	got := <-done

	if got.err != nil {
		t.Fatalf("concurrent command failed because of an unrelated timeout: %v", got.err)
	}

	if got.body != "slow-ok" {
		t.Fatalf(
			"concurrent command got %q, want %q -- its response stream was truncated",
			got.body, "slow-ok",
		)
	}
}

// TestReconstructErrKeepsSentinelIdentity covers the round trip that made
// ExecC2Command's `errors.Is(err, ErrTimeout)` branch dead code: errResponse
// flattens an error to a string, and rebuilding it with [errors.New] produced
// something that read correctly but matched nothing.
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
	dir := newFakeMinimega(t, func(cmd string) (*miniclient.Response, fakeAction) {
		return reply(cmd), actionReply
	})

	useFakeMinimega(t, dir)

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

// TestRunTabularErrKeepsRowsFromHealthyHosts: on a mesh a query can succeed
// on some hosts and fail on others (a `cc` query on a host that does not own
// the VM answers with an error). The rows must survive alongside the error so
// callers can decide whether they are sufficient.
func TestRunTabularErrKeepsRowsFromHealthyHosts(t *testing.T) {
	dir := newFakeMinimega(t, func(string) (*miniclient.Response, fakeAction) {
		return &miniclient.Response{
			Resp: minicli.Responses{
				{Host: "sibling", Error: "no client foo"},
				{Host: "owner", Header: []string{"id", "responses"}, Tabular: [][]string{{"7", "1"}}},
			},
		}, actionReply
	})

	useFakeMinimega(t, dir)

	cmd := NewCommand()
	cmd.Command = "cc commands"
	cmd.Columns = []string{"host", "responses"}

	rows, err := RunTabularErr(cmd)
	if err == nil || !strings.Contains(err.Error(), "no client foo") {
		t.Fatalf("err = %v, want the sibling host's error", err)
	}

	want := []map[string]string{{"host": "owner", "responses": "1"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}

	if got := RunTabular(cmd); !reflect.DeepEqual(got, want) {
		t.Fatalf("RunTabular rows = %v, want %v", got, want)
	}
}
