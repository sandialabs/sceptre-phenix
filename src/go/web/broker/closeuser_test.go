package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"phenix/util/polltest"
)

// CloseUserClients closes, with code 1012 and its reason, the clients of the
// user only, whose role they hold as it was when they connected.
//
//nolint:paralleltest // runs the broker loop
func TestCloseUserClients(t *testing.T) {
	runBroker(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading connection: %v", err)

			return
		}

		NewClient(roleAllowing("*", "*"), r.URL.Query().Get("user"), conn).Go()
	}))
	t.Cleanup(srv.Close)

	dial := func(user string) *websocket.Conn {
		conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?user="+user, nil)
		if err != nil {
			t.Fatalf("dialing websocket as %s: %v", user, err)
		}

		t.Cleanup(func() { _ = resp.Body.Close() })
		t.Cleanup(func() { _ = conn.Close() })

		return conn
	}

	alice, _ := dial("alice"), dial("bob")

	polltest.Until(t, "the broker has both clients", func() bool { return CurrentStats().Clients == 2 })

	CloseUserClients("alice")

	closed := readClose(t, alice)
	if closed.Code != websocket.CloseServiceRestart || closed.Text != roleChangedReason {
		t.Errorf(
			"alice's websocket closed with %d %q, want %d %q",
			closed.Code,
			closed.Text,
			websocket.CloseServiceRestart,
			roleChangedReason,
		)
	}

	polltest.Until(t, "the broker has only bob's client", func() bool { return CurrentStats().Clients == 1 })
}

// CloseUserClients does not wait while the broker's queue is full, and the
// request still reaches the broker once it has room.
//
//nolint:paralleltest // fills the shared queue
func TestCloseUserClientsQueueFull(t *testing.T) {
	for range cap(closeUsers) - len(closeUsers) {
		closeUsers <- "filler"
	}

	returned := make(chan struct{})

	go func() {
		CloseUserClients("alice")
		close(returned)
	}()

	polltest.Until(t, "CloseUserClients returns with the queue full", func() bool {
		select {
		case <-returned:
			return true
		default:
			return false
		}
	})

	for {
		if user := <-closeUsers; user == "alice" {
			break
		}
	}
}
