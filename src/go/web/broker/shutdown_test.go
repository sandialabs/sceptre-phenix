package broker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/util/polltest"
	"phenix/web/lifecycle"
)

// dialBroker connects a websocket client to a server that hands each
// connection to the broker, as ServeWS does.
func dialBroker(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing websocket: %v", err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// readClose reads from conn until it closes, and returns how it closed.
func readClose(t *testing.T, conn *websocket.Conn) *websocket.CloseError {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}

		var closed *websocket.CloseError
		if !errors.As(err, &closed) {
			t.Fatalf("reading until the broker closes the websocket: %v, want a close frame", err)
		}

		return closed
	}
}

// Shutdown closes every client, and every one that connects after, with
// close code 1001 and the shutdown reason.
//
//nolint:paralleltest // runs the broker loop
func TestShutdownClosesClientsGoingAway(t *testing.T) {
	runBroker(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading connection: %v", err)

			return
		}

		NewClient(roleAllowing("*", "*"), "alice", conn).Go()
	}))
	t.Cleanup(srv.Close)

	before := dialBroker(t, srv)

	polltest.Until(t, "the broker has the client", func() bool { return CurrentStats().Clients == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if open := Shutdown(ctx); open != 0 {
		t.Errorf("Shutdown() = %d clients still open, want 0", open)
	}

	want := &websocket.CloseError{Code: websocket.CloseGoingAway, Text: lifecycle.CloseReason}

	if diff := cmp.Diff(want, readClose(t, before)); diff != "" {
		t.Errorf("close of a client open at Shutdown() mismatch (-want +got):\n%s", diff)
	}

	after := dialBroker(t, srv)

	if diff := cmp.Diff(want, readClose(t, after)); diff != "" {
		t.Errorf("close of a client connecting after Shutdown() mismatch (-want +got):\n%s", diff)
	}

	if got := CurrentStats().Clients; got != 0 {
		t.Errorf("CurrentStats().Clients after Shutdown() = %d, want 0", got)
	}
}
