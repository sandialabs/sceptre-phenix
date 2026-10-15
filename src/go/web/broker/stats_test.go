package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// idleClient is a client of a real websocket connection whose read and write
// loops do not run, so what is queued for it stays queued.
func idleClient(t *testing.T) *Client {
	t.Helper()

	clients := make(chan *Client, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrading connection: %v", err)

			return
		}

		clients <- NewClient(roleAllowing("vms", "list"), "", conn)
	}))
	t.Cleanup(srv.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing websocket: %v", err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })
	t.Cleanup(func() { _ = conn.Close() })

	return <-clients
}

// The client count follows clients as they join, leave, and are dropped for
// not keeping up.
//
//nolint:paralleltest // clients is shared package state
func TestCurrentStatsCountsClients(t *testing.T) {
	withClients(t)

	t.Cleanup(func() {
		clientCount.Store(0)

		for len(unregister) > 0 {
			<-unregister
		}
	})

	a, b := idleClient(t), idleClient(t)

	// as the broker loop registers them
	for _, c := range []*Client{a, b} {
		clients[c] = true
		clientCount.Add(1)
	}

	if got := CurrentStats().Clients; got != 2 {
		t.Fatalf("after two joined: %d clients, want 2", got)
	}

	forget(a)

	if got := CurrentStats().Clients; got != 1 {
		t.Fatalf("after one left: %d clients, want 1", got)
	}

	// b stops reading: once its queue is full, the next broadcast drops it
	for range cap(b.publish) {
		b.publish <- encodedPublish{}
	}

	deliver(encodedPublish{}) // with no policy, for every client

	if got := CurrentStats().Clients; got != 0 {
		t.Errorf("after the slow client was dropped: %d clients, want 0", got)
	}
}

//nolint:paralleltest // broadcast is shared package state
func TestCurrentStatsReportsQueue(t *testing.T) {
	t.Cleanup(func() {
		for len(broadcast) > 0 {
			<-broadcast
		}
	})

	for range 3 {
		broadcast <- encodedPublish{}
	}

	stats := CurrentStats()

	if stats.Queued != 3 || stats.Capacity != brokerChannelBuffer {
		t.Errorf("stats = %+v, want 3 queued of %d", stats, brokerChannelBuffer)
	}
}
