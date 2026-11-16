package scorch

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"phenix/util/polltest"
)

// startComponents starts the SCORCH processors, once for all tests, as
// useScorch does.
func startComponents() {
	startOnce.Do(func() { Start(context.Background(), "/") })
}

func update(cmp, status, out string) ComponentUpdate {
	return ComponentUpdate{
		Exp: "exp", CmpName: cmp, Stage: "start", Status: status, Output: []byte(out),
	}
}

// componentTypeBreak is the type of a component that opens a terminal.
const componentTypeBreak = "break"

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

	// the fast viewer reads each line before the next is sent, so on a busy
	// machine only the stuck viewer's queue fills
	for i := range 2 * viewerQueue {
		line := strconv.Itoa(i) + "\n"
		want.WriteString(line)
		sent := want.Len()

		polltest.Within(t, "sending update "+strconv.Itoa(i), func() {
			UpdateComponent(update(cmp, statusRunning, line))
		})
		polltest.Until(t, "the fast viewer reading line "+strconv.Itoa(i), func() bool {
			return len(fastConn.String()) >= sent
		})
	}

	// before the success update, which finishes every viewer still listed;
	// the update's handshake orders this read of ws after the edit
	if _, ok := ws[componentKey(cmp)][slow.id]; ok {
		t.Errorf("UpdateComponent(%s) x %d: ws[%q] still lists the slow viewer, want it dropped",
			cmp, 2*viewerQueue, componentKey(cmp))
	}

	// its queue filled behind the write it is stuck in
	if !drained(slow) {
		t.Errorf("UpdateComponent(%s) x %d: slow viewer's queue still open, want it dropped", cmp, 2*viewerQueue)
	}

	polltest.Within(t, "sending the success update", func() {
		UpdateComponent(update(cmp, statusSuccess, ""))
	})

	polltest.Within(t, "the fast viewer's output ending", func() { <-fastConn.closed })

	if got := fastConn.String(); got != want.String()+finishedMarker {
		t.Errorf("UpdateComponent(%s) x %d: fast viewer received %d bytes, want %d",
			cmp, 2*viewerQueue, len(got), want.Len()+len(finishedMarker))
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
		t.Errorf("viewer of finished component %s received %q, want %q", cmp, got, want)
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
		t.Errorf("after UpdateComponent(%s), ws[%q] lists viewer %v that left, want it removed", cmp, componentKey(cmp), v.id)
	}

	UpdateComponent(update(cmp, statusSuccess, ""))
}

// componentOutput asks the component processor for a component's output, as GetComponentOutput does.
func componentOutput(cmp string) outputResponse {
	req := outputRequest{key: componentKey(cmp), resp: make(chan outputResponse, 1)}
	outputRequests <- req

	return <-req.resp
}

// A component runs from its first running or background update until it
// succeeds or fails; only a running break is a terminal; output is answered
// once the component is done, and starting it again forgets the old output.
//
//nolint:paralleltest // uses the package's component state
func TestComponentOutputRequests(t *testing.T) {
	startComponents()

	const cmp = "requests"

	brk := update(cmp, statusStart, "")
	brk.CmpType = componentTypeBreak

	plain := update(cmp, statusStart, "")
	plain.CmpType = "shell"

	for _, step := range []struct {
		name   string
		update ComponentUpdate
		want   outputResponse
	}{
		{"break started", brk, outputResponse{}},
		{"break running", update(cmp, statusRunning, "one\n"), outputResponse{running: true, terminal: true}},
		{"break succeeded", update(cmp, statusSuccess, ""), outputResponse{output: []byte("one\n")}},
		{"started again", plain, outputResponse{}},
		{"in the background", update(cmp, statusBackground, "two\n"), outputResponse{running: true}},
		{"empty output", update(cmp, statusRunning, ""), outputResponse{running: true}},
		{"failed", update(cmp, statusFailure, ""), outputResponse{output: []byte("two\n")}},
		{"unknown status", update(cmp, "paused", "ignored\n"), outputResponse{output: []byte("two\n")}},
	} {
		UpdateComponent(step.update)

		got := componentOutput(cmp)
		if got.running != step.want.running || got.terminal != step.want.terminal || string(got.output) != string(step.want.output) {
			t.Errorf("after %s, output request = %+v, want %+v", step.name, got, step.want)
		}
	}

	if got := componentOutput("never-updated"); got.running || got.terminal || got.output != nil {
		t.Errorf("output request of an unknown component = %+v, want none", got)
	}
}

// processComponents returns once its context is done.
//
//nolint:paralleltest // reads the package's component channels
func TestProcessComponentsStopsWithItsContext(t *testing.T) {
	startComponents()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	polltest.Within(t, "processComponents with a done context", func() { processComponents(ctx) })
}
