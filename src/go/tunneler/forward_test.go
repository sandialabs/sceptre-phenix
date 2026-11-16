package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/websocket"

	ft "phenix/web/forward/forwardtypes"
)

// An active listener sends the bytes of each TCP connection through the
// websocket of its phēnix forward, in the two directions.
func TestActivatedListenerForwardsTraffic(t *testing.T) {
	requests := make(chan *http.Request, 1)

	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		requests <- ws.Request()

		data := make([]byte, len("ping"))
		if _, err := io.ReadFull(ws, data); err != nil {
			return
		}

		_, _ = ws.Write(append([]byte("pong "), data...))
	}))
	t.Cleanup(server.Close)

	manager := useListenerManager(t)
	swapValue(t, &wsEndpoint, "ws"+strings.TrimPrefix(server.URL, "http"))
	swapValue(t, &origin, server.URL)
	swapValue(t, &headers, http.Header{"X-Phenix-Auth-Token": {"Bearer token"}})

	local, _ := manager.add(ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22})
	if err := manager.withListener(local.ID, activateLocalListener); err != nil {
		t.Fatalf("activating listener: %v", err)
	}

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(boundPort(t, manager, local.ID)))

	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("connecting to listener: %v", err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("writing to listener: %v", err)
	}

	got := make([]byte, len("pong ping"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading from listener: %v", err)
	}

	if string(got) != "pong ping" {
		t.Errorf("listener reply = %q, want %q", got, "pong ping")
	}

	_ = conn.Close()

	request := <-requests

	wantRequest := map[string]string{
		"path":   "/api/v1/experiments/exp/vms/vm/forwards/10.0.0.1/22/ws",
		"origin": server.URL,
		"token":  "Bearer token",
	}

	gotRequest := map[string]string{
		"path":   request.URL.Path,
		"origin": request.Header.Get("Origin"),
		"token":  request.Header.Get("X-Phenix-Auth-Token"),
	}

	if diff := cmp.Diff(wantRequest, gotRequest); diff != "" {
		t.Errorf("forward websocket request mismatch (-want +got):\n%s", diff)
	}

	if err := manager.withListener(local.ID, deactivateLocalListener); err != nil {
		t.Fatalf("deactivating listener: %v", err)
	}

	if again, err := net.Dial("tcp", address); err == nil {
		_ = again.Close()

		t.Errorf("connecting to %s after deactivation succeeded, want error", address)
	}
}

// forwardThrough activates a listener whose forward websocket is served by
// handler, and returns a connection to its local port with a 10 s deadline.
func forwardThrough(t *testing.T, handler http.Handler) net.Conn {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	manager := useListenerManager(t)
	swapValue(t, &wsEndpoint, "ws"+strings.TrimPrefix(server.URL, "http"))
	swapValue(t, &origin, server.URL)

	local, _ := manager.add(ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22})
	if err := manager.withListener(local.ID, activateLocalListener); err != nil {
		t.Fatalf("activating listener: %v", err)
	}

	t.Cleanup(func() { _ = manager.withListener(local.ID, deactivateLocalListener) })

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(boundPort(t, manager, local.ID)))

	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("connecting to listener: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	return conn
}

// The local connection ends, not hangs, when phēnix refuses the websocket or
// closes it.
func TestForwardClosesLocalConnection(t *testing.T) {
	tests := map[string]http.Handler{
		"phēnix refuses the websocket": http.NotFoundHandler(),
		"phēnix closes the websocket": websocket.Handler(func(ws *websocket.Conn) {
			_, _ = io.WriteString(ws, "bye")
		}),
	}

	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			conn := forwardThrough(t, handler)

			got, err := io.ReadAll(conn)
			if err != nil {
				t.Fatalf("reading from listener: %v, want the connection closed", err)
			}

			if name == "phēnix closes the websocket" && string(got) != "bye" {
				t.Errorf("listener sent %q before closing, want %q", got, "bye")
			}
		})
	}
}

// The websocket to phēnix ends when the program on the local port closes its
// connection.
func TestForwardClosesWebsocketWhenLocalSideEnds(t *testing.T) {
	ended := make(chan error, 1)

	conn := forwardThrough(t, websocket.Handler(func(ws *websocket.Conn) {
		_, err := io.Copy(io.Discard, ws)
		ended <- err
	}))

	if _, err := io.WriteString(conn, "hello"); err != nil {
		t.Fatalf("writing to listener: %v", err)
	}

	_ = conn.Close()

	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("websocket to phēnix still open 10 s after the local connection closed")
	}
}

// createLocalListener activates a new listener when no username is set or
// when the username owns the forward.
func TestCreateLocalListener(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		owner         string
		occupied      bool
		wantErr       bool
		wantListening bool
	}{
		{name: "owner", username: "alice", owner: "alice", wantListening: true},
		{name: "no username", owner: "bob", wantListening: true},
		{name: "other owner", username: "alice", owner: "bob"},
		{name: "occupied port", owner: "alice", occupied: true, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := useListenerManager(t)
			swapValue(t, &username, test.username)

			listener := ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22, Owner: test.owner}
			if test.occupied {
				listener.SrcPort = occupiedTCPPort(t)
			}

			if err := createLocalListener(listener); (err != nil) != test.wantErr {
				t.Fatalf("createLocalListener() error = %v, want error %t", err, test.wantErr)
			}

			if err := createLocalListener(listener); err != nil {
				t.Errorf("createLocalListener() of a known listener error = %v, want nil", err)
			}

			snapshot := manager.snapshot()
			if len(snapshot) != 1 {
				t.Fatalf("listener count = %d, want 1", len(snapshot))
			}

			if snapshot[0].Listening != test.wantListening {
				t.Errorf("listener listening = %t, want %t", snapshot[0].Listening, test.wantListening)
			}
		})
	}
}

// deleteLocalListener removes the listener and stops its port, and ignores an unknown key.
func TestDeleteLocalListener(t *testing.T) {
	manager := useListenerManager(t)

	local, _ := manager.add(ft.Listener{Exp: "exp", VM: "vm", DstHost: "10.0.0.1", DstPort: 22})
	if err := manager.withListener(local.ID, activateLocalListener); err != nil {
		t.Fatalf("activating listener: %v", err)
	}

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(boundPort(t, manager, local.ID)))

	deleteLocalListener("unknown")

	if got := len(manager.snapshot()); got != 1 {
		t.Fatalf("listener count after deleting an unknown key = %d, want 1", got)
	}

	deleteLocalListener(local.ToKey())

	if got := len(manager.snapshot()); got != 0 {
		t.Errorf("listener count after delete = %d, want 0", got)
	}

	if again, err := net.Dial("tcp", address); err == nil {
		_ = again.Close()

		t.Errorf("connecting to %s after delete succeeded, want error", address)
	}
}
