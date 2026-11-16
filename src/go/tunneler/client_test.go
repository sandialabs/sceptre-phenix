package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	ft "phenix/web/forward/forwardtypes"
)

// The default socket path is $TMPDIR/phenix/tunneler.sock, and /tmp when TMPDIR is empty.
func TestSocketPathDefault(t *testing.T) {
	tests := []struct {
		tmpdir string
		want   string
	}{
		{tmpdir: "/var/run/user", want: "/var/run/user/phenix/tunneler.sock"},
		{tmpdir: "", want: "/tmp/phenix/tunneler.sock"},
	}

	for _, test := range tests {
		t.Run("TMPDIR="+test.tmpdir, func(t *testing.T) {
			t.Setenv("TMPDIR", test.tmpdir)

			if got := socketPath(); got != test.want {
				t.Errorf("socketPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewClientReportsMissingSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "missing.sock")
	swapValue(t, &socketPath, func() string { return path })

	if _, err := newClient(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("newClient() error = %v, want %v", err, fs.ErrNotExist)
	}
}

// Each client request goes to handleConnection, which changes the listener
// and replies with the result or an error.
func TestClientManagesListenersThroughServer(t *testing.T) {
	var (
		manager       = useListenerManager(t)
		local, _      = manager.add(ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22, Owner: "alice"})
		occupiedPort  = occupiedTCPPort(t)
		freePort      = freeTCPPort(t)
		ignoreSockets = cmpopts.IgnoreUnexported(LocalListener{})
	)

	listeners, err := pipeClient(t, handleConnection).getLocalListeners()
	if err != nil {
		t.Fatalf("getLocalListeners() error = %v", err)
	}

	want := Listeners{{Listener: local.Listener, ID: local.ID}}
	if diff := cmp.Diff(want, listeners, ignoreSockets); diff != "" {
		t.Errorf("getLocalListeners() mismatch (-want +got):\n%s", diff)
	}

	steps := []struct {
		name          string
		call          func(client) error
		wantErr       bool
		wantListening bool
		wantPort      int
	}{
		{name: "activate", call: func(c client) error { return c.activateLocalListener(1) }, wantListening: true},
		{
			name:          "activate active listener",
			call:          func(c client) error { return c.activateLocalListener(1) },
			wantErr:       true,
			wantListening: true,
		},
		{
			name:          "move to occupied port rolls back",
			call:          func(c client) error { return c.moveLocalListener(1, occupiedPort) },
			wantErr:       true,
			wantListening: true,
		},
		{
			name:          "move",
			call:          func(c client) error { return c.moveLocalListener(1, freePort) },
			wantListening: true,
			wantPort:      freePort,
		},
		{name: "deactivate", call: func(c client) error { return c.deactivateLocalListener(1) }, wantPort: freePort},
		{
			name:     "deactivate inactive listener",
			call:     func(c client) error { return c.deactivateLocalListener(1) },
			wantErr:  true,
			wantPort: freePort,
		},
		{
			name:     "move inactive listener to occupied port",
			call:     func(c client) error { return c.moveLocalListener(1, occupiedPort) },
			wantPort: occupiedPort,
		},
		{
			name:     "activate on occupied port",
			call:     func(c client) error { return c.activateLocalListener(1) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
		{
			name:     "move unknown listener",
			call:     func(c client) error { return c.moveLocalListener(2, freePort) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
		{
			name:     "move to port 0",
			call:     func(c client) error { return c.moveLocalListener(1, 0) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
		{
			name:     "move listener 0",
			call:     func(c client) error { return c.moveLocalListener(0, freePort) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
		{
			name:     "activate listener 0",
			call:     func(c client) error { return c.activateLocalListener(0) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
		{
			name:     "deactivate listener -1",
			call:     func(c client) error { return c.deactivateLocalListener(-1) },
			wantErr:  true,
			wantPort: occupiedPort,
		},
	}

	for _, step := range steps {
		ok := t.Run(step.name, func(t *testing.T) {
			err := step.call(pipeClient(t, handleConnection))
			if (err != nil) != step.wantErr {
				t.Fatalf("request error = %v, want error %t", err, step.wantErr)
			}

			var listening bool

			_ = manager.withListener(local.ID, func(ll *LocalListener) error {
				listening = ll.Listening
				if ll.SrcPort != step.wantPort {
					t.Errorf("listener port = %d, want %d", ll.SrcPort, step.wantPort)
				}

				return nil
			})

			if listening != step.wantListening {
				t.Errorf("listener listening = %t, want %t", listening, step.wantListening)
			}
		})

		if !ok {
			break
		}
	}
}

// A client request fails when the connection closes before the request or
// before the reply.
func TestClientReportsConnectionErrors(t *testing.T) {
	useListenerManager(t)

	calls := []struct {
		name string
		call func(client) error
	}{
		{name: "list", call: func(c client) error { _, err := c.getLocalListeners(); return err }},
		{name: "move", call: func(c client) error { return c.moveLocalListener(1, 2) }},
		{name: "activate", call: func(c client) error { return c.activateLocalListener(1) }},
		{name: "deactivate", call: func(c client) error { return c.deactivateLocalListener(1) }},
	}

	noReply := func(conn net.Conn) {
		_ = json.NewDecoder(conn).Decode(new(Message))
		_ = conn.Close()
	}

	for _, call := range calls {
		t.Run(call.name+" closed before request", func(t *testing.T) {
			cli := pipeClient(t, handleConnection)
			_ = cli.close()

			if err := call.call(cli); !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("request error = %v, want %v", err, io.ErrClosedPipe)
			}
		})

		t.Run(call.name+" closed before reply", func(t *testing.T) {
			if err := call.call(pipeClient(t, noReply)); !errors.Is(err, io.EOF) {
				t.Errorf("request error = %v, want %v", err, io.EOF)
			}
		})
	}
}

func TestClientRejectsMalformedListeners(t *testing.T) {
	for _, reply := range []string{`{"type":"LISTENERS"}`, `{"type":"LISTENERS","payload":{"id":1}}`} {
		t.Run(reply, func(t *testing.T) {
			cli := pipeClient(t, func(conn net.Conn) {
				defer func() { _ = conn.Close() }()

				_ = json.NewDecoder(conn).Decode(new(Message))
				_, _ = io.WriteString(conn, reply)
			})

			if listeners, err := cli.getLocalListeners(); err == nil {
				t.Errorf("getLocalListeners() = %v, want error", listeners)
			}
		})
	}
}

// handleConnection closes the connection without a reply for a request it
// cannot decode or does not know.
func TestHandleConnectionClosesOnBadRequest(t *testing.T) {
	useListenerManager(t)

	for _, request := range []string{"not JSON\n", `{"type":"UNKNOWN"}` + "\n"} {
		t.Run(request, func(t *testing.T) {
			cli := pipeClient(t, handleConnection)
			_, _ = io.WriteString(cli.conn, request)

			var reply Message
			if err := cli.dec.Decode(&reply); !errors.Is(err, io.EOF) {
				t.Errorf("reading reply = (%+v, %v), want %v", reply, err, io.EOF)
			}
		})
	}
}

// handleConnection returns when the client leaves before the reply.
func TestHandleConnectionReturnsWhenClientLeaves(t *testing.T) {
	useListenerManager(t)

	for _, messageType := range []MessageType{LISTENERS, MOVE, ACTIVATE, DEACTIVATE} {
		t.Run(string(messageType), func(t *testing.T) {
			server, conn := net.Pipe()
			done := make(chan struct{})

			go func() {
				defer close(done)

				handleConnection(server)
			}()

			message := Message{Type: messageType, Payload: json.RawMessage(`{"id":1,"port":2}`)}
			if err := json.NewEncoder(conn).Encode(message); err != nil {
				t.Fatalf("sending %s request: %v", messageType, err)
			}

			_ = conn.Close()
			<-done
		})
	}
}

// AddHeaderTransport adds its headers to each request and keeps the request's own headers.
func TestAddHeaderTransport(t *testing.T) {
	var (
		inner     = &recordTransport{}
		transport = AddHeaderTransport{T: inner, Headers: http.Header{"X-Added": {"1", "2"}}}
	)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://phenix/api/v1/experiments", nil)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}

	request.Header.Set("X-Own", "own")

	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}

	_ = response.Body.Close()

	want := http.Header{"X-Own": {"own"}, "X-Added": {"1", "2"}}
	if diff := cmp.Diff(want, inner.header); diff != "" {
		t.Errorf("request headers mismatch (-want +got):\n%s", diff)
	}
}

// recordTransport keeps the headers of the last request and answers with status 200.
type recordTransport struct {
	header http.Header
}

func (r *recordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.header = req.Header.Clone()

	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
}

// pipeClient gives a client connected to serve over [net.Pipe]. The test waits
// for serve to return at cleanup.
func pipeClient(t *testing.T, serve func(net.Conn)) client {
	t.Helper()

	server, conn := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)

		serve(server)
	}()

	t.Cleanup(func() {
		_ = conn.Close()
		<-done
	})

	return client{conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
}

// swapValue sets *p to v for the test and sets the old value again at cleanup.
func swapValue[T any](t *testing.T, p *T, v T) {
	t.Helper()

	old := *p
	*p = v

	t.Cleanup(func() { *p = old })
}

// useListenerManager gives the test an empty listener registry and
// deactivates its listeners at cleanup.
func useListenerManager(t *testing.T) *listenerManager {
	t.Helper()

	manager := newListenerManager()
	swapValue(t, &localListeners, manager)

	t.Cleanup(func() {
		for _, listener := range manager.snapshot() {
			_ = manager.withListener(listener.ID, func(ll *LocalListener) error {
				if ll.Listening {
					return deactivateLocalListener(ll)
				}

				return nil
			})
		}
	})

	return manager
}

// boundPort gives the TCP port that the active listener id listens on.
func boundPort(t *testing.T, manager *listenerManager, id int) int {
	t.Helper()

	var port int

	err := manager.withListener(id, func(ll *LocalListener) error {
		if !ll.Listening {
			return errors.New("listener is inactive")
		}

		port = ll.listener.Addr().(*net.TCPAddr).Port

		return nil
	})
	if err != nil {
		t.Fatalf("getting port of listener %d: %v", id, err)
	}

	return port
}

// occupiedTCPPort gives a TCP port that a listener holds until cleanup.
func occupiedTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("creating occupied listener: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	return listener.Addr().(*net.TCPAddr).Port
}

// shortTempDir makes a directory in /tmp, because a unix socket path in
// t.TempDir can be too long on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "tun") //nolint:usetesting // t.TempDir paths are too long for unix sockets on macOS
	if err != nil {
		t.Fatalf("creating temporary directory: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}
