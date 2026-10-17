package scorch

import (
	"strconv"
	"strings"
	"testing"

	"phenix/util/polltest"
)

// startComponents starts the SCORCH processors, once for all tests, as
// useScorch does.
func startComponents() {
	startOnce.Do(func() { Start("/") })
}

func update(cmp, status, out string) ComponentUpdate {
	return ComponentUpdate{
		Exp: "exp", CmpName: cmp, Stage: "start", Status: status, Output: []byte(out),
	}
}

func componentKey(cmp string) string { return "exp|0|0|start|" + cmp }

// A viewer that stopped reading does not hold up a component's updates: the
// others get every chunk, and it is dropped.
//
//nolint:paralleltest // uses the package's component state
func TestSlowComponentViewerDoesNotBlockUpdates(t *testing.T) {
	startComponents()

	const cmp = "slow-viewer"

	UpdateComponent(update(cmp, statusStart, ""))
	UpdateComponent(update(cmp, statusRunning, ""))

	var (
		fastConn = newFastConn()
		fast     = newViewer(fastConn)
		slowConn = newStuckConn()
		slow     = newViewer(slowConn)
	)

	wsRequests <- wsRequest{key: componentKey(cmp), v: fast}
	wsRequests <- wsRequest{key: componentKey(cmp), v: slow}

	go fast.serve()
	go slow.serve()

	t.Cleanup(fast.leave)
	t.Cleanup(slow.leave)

	var want strings.Builder

	polltest.Within(t, "sending the updates", func() {
		for i := range 2 * viewerQueue {
			line := strconv.Itoa(i) + "\n"
			want.WriteString(line)

			UpdateComponent(update(cmp, statusRunning, line))
		}

		UpdateComponent(update(cmp, statusSuccess, ""))
	})

	polltest.Within(t, "the fast viewer's output ending", func() { <-fastConn.closed })

	if got := fastConn.String(); got != want.String()+finishedMarker {
		t.Errorf("fast viewer got %d bytes, want %d", len(got), want.Len()+len(finishedMarker))
	}

	// its queue filled behind the write it is stuck in
	if !drained(slow) {
		t.Error("the slow viewer was not dropped")
	}
}

// A viewer of a finished component gets its whole output, then the finished
// marker, and is closed.
//
//nolint:paralleltest // uses the package's component state
func TestFinishedComponentReplaysOutputThenCloses(t *testing.T) {
	startComponents()

	const cmp = "finished"

	UpdateComponent(update(cmp, statusStart, ""))
	UpdateComponent(update(cmp, statusRunning, "one\n"))
	UpdateComponent(update(cmp, statusRunning, "two\n"))
	UpdateComponent(update(cmp, statusSuccess, ""))

	conn := newFastConn()
	v := newViewer(conn)

	wsRequests <- wsRequest{key: componentKey(cmp), v: v}

	polltest.Within(t, "serving the viewer", v.serve)

	if got, want := conn.String(), "one\ntwo\n"+finishedMarker; got != want {
		t.Errorf("viewer got %q, want %q", got, want)
	}
}

// A viewer whose client left is forgotten at the next update.
//
//nolint:paralleltest // uses the package's component state
func TestViewerThatLeavesIsForgotten(t *testing.T) {
	startComponents()

	const cmp = "left"

	UpdateComponent(update(cmp, statusStart, ""))
	UpdateComponent(update(cmp, statusRunning, "one\n"))

	v := newViewer(newFastConn())

	wsRequests <- wsRequest{key: componentKey(cmp), v: v}

	v.leave()

	// the update's handshake orders this test's read of ws after the edit
	UpdateComponent(update(cmp, statusRunning, "two\n"))

	if _, ok := ws[componentKey(cmp)][v.id]; ok {
		t.Error("the viewer that left is still listed")
	}

	UpdateComponent(update(cmp, statusSuccess, ""))
}
