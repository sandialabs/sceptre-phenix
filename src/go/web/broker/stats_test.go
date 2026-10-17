package broker

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

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

	// the drop counters are process-wide totals other tests add to
	ignoreDrops := cmpopts.IgnoreFields(Stats{}, "SlowClientsDropped", "LogLinesDropped")

	want := Stats{Clients: 2, Queued: 3, Capacity: brokerChannelBuffer}
	if diff := cmp.Diff(want, CurrentStats(), ignoreDrops); diff != "" {
		t.Errorf("CurrentStats() with 2 clients and 3 broadcasts queued mismatch (-want +got):\n%s", diff)
	}

	forget(a)

	if got := CurrentStats().Clients; got != 1 {
		t.Errorf("CurrentStats().Clients after forget(a) = %d, want 1", got)
	}

	dropped := CurrentStats().SlowClientsDropped

	// b stops reading: once its queue is full, the next broadcast drops it
	for range cap(b.publish) {
		b.publish <- encodedPublish{}
	}

	deliver(encodedPublish{}) // with no policy, for every client

	if got := CurrentStats().Clients; got != 0 {
		t.Errorf("CurrentStats().Clients after deliver dropped the slow client = %d, want 0", got)
	}

	if got := CurrentStats().SlowClientsDropped; got != dropped+1 {
		t.Errorf("CurrentStats().SlowClientsDropped after deliver dropped the slow client = %d, want %d", got, dropped+1)
	}
}
