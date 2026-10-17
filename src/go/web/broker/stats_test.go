package broker

import "testing"

// The stats report the broadcast queue, and count clients as they leave and
// are dropped for not keeping up.
//
//nolint:paralleltest // clients and broadcast are shared package state
func TestCurrentStats(t *testing.T) {
	a, b := newFakeClient(newFakeConn(t)), newFakeClient(newFakeConn(t))

	withClients(t, a, b)

	t.Cleanup(func() {
		for len(unregister) > 0 {
			<-unregister
		}

		for len(broadcast) > 0 {
			<-broadcast
		}
	})

	for range 3 {
		broadcast <- encodedPublish{}
	}

	if stats := CurrentStats(); stats.Clients != 2 || stats.Queued != 3 || stats.Capacity != brokerChannelBuffer {
		t.Fatalf("stats = %+v, want 2 clients and 3 queued of %d", stats, brokerChannelBuffer)
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
