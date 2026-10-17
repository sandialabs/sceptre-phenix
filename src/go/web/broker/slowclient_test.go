package broker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/experiment"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/polltest"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

// runBroker runs the broker loop until the test ends. Register any clients
// with withClients first: the loop owns the clients map while it runs.
func runBroker(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		run(ctx, nil, nil)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})
}

// withLogQueue gives the test an empty log queue and no untold drops.
func withLogQueue(t *testing.T) {
	t.Helper()

	saved := logs
	logs = make(chan encodedPublish, logBufferSize)

	logsUntold.Store(0)

	t.Cleanup(func() {
		logs = saved

		logsUntold.Store(0)
	})
}

// A client that stops reading is dropped once its queue fills, with a close
// code telling it to come back, while every other client gets every message.
//
//nolint:paralleltest // swaps the broker's clients
func TestSlowClientDoesNotDelayOthers(t *testing.T) {
	var (
		fastConn = newFakeConn(t)
		slowConn = newBlockingConn(t)
		fast     = newFakeClient(fastConn)
		slow     = newFakeClient(slowConn)
	)

	withClients(t, fast, slow)

	go fast.write()
	go slow.write()

	t.Cleanup(fast.Stop)
	t.Cleanup(slow.Stop)

	dropped := slowDropped.Load()

	runBroker(t)

	const batch = publishChannelBuffer / 2

	sent := 0

	var got [][]byte

	// in batches the fast client is let to keep up with: its writer is a
	// goroutine like any other
	for range 4 {
		polltest.Within(t, "broadcasting a batch", func() {
			for range batch {
				Broadcast(nil, bt.NewResource("experiment", strconv.Itoa(sent), "update"), nil)
				sent++
			}
		})

		polltest.Within(t, "the fast client receiving its batch", func() {
			for len(got) < sent {
				got = append(got, fastConn.messages()...)

				runtime.Gosched()
			}
		})
	}

	for i, msg := range got {
		if !strings.Contains(string(msg), fmt.Sprintf(`"name":"%d"`, i)) {
			t.Errorf("fast client's message %d = %s, want the broadcast named %d", i, msg, i)

			break
		}
	}

	polltest.Within(t, "dropping the slow client", func() { <-slow.done })
	polltest.Within(t, "closing the slow client's connection", func() { <-slowConn.closed })

	select {
	case f := <-slowConn.controls:
		if f.kind != websocket.CloseMessage || binary.BigEndian.Uint16(f.data) != websocket.CloseTryAgainLater {
			t.Errorf("dropped slow client's control frame = %d %q, want close %d",
				f.kind, f.data, websocket.CloseTryAgainLater)
		}
	default:
		t.Errorf("dropped slow client was sent no control frame, want close %d", websocket.CloseTryAgainLater)
	}

	if n := slowDropped.Load() - dropped; n != 1 {
		t.Errorf("after %d broadcasts, slowDropped grew by %d, want 1", sent, n)
	}

	if isClosed(fast.done) {
		t.Errorf("after %d broadcasts, the fast client is done, want it kept", sent)
	}
}

// Dropping a client does not wait for the write it is stuck in, which ends
// once the connection is closed under it.
//
//nolint:paralleltest // swaps the broker's clients
func TestDroppingAClientDoesNotWaitForItsWrite(t *testing.T) {
	conn := newBlockingConn(t)
	cli := newFakeClient(conn)

	withClients(t, cli)

	wrote := make(chan struct{})

	go func() {
		defer close(wrote)

		cli.write()
	}()

	cli.publish <- encodedPublish{data: []byte("{}")}

	polltest.Within(t, "the write starting", func() { <-conn.writing })

	start := time.Now()

	drop(cli)

	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("drop(client stuck in a write) took %v, want at most 50ms", d)
	}

	polltest.Within(t, "the stuck write ending", func() { <-wrote })
}

// Logging never blocks on the broker; lines that do not fit are counted and
// announced once the loop runs.
//
//nolint:paralleltest // swaps the broker's clients and log queue
func TestLoggingNeverBlocksTheBroker(t *testing.T) {
	withLogQueue(t)

	reader := newFakeClient(nil)
	reader.publish = make(chan any, 2*logBufferSize)
	reader.logsSubscribed.Store(true)

	withClients(t, reader)

	plog.AddHandler("broker-test", plog.NewUIHandler("info", func(ts time.Time, level, typ, msg string) {
		entry, _ := json.Marshal(plog.LogEntry{Time: ts.UnixNano(), Level: level, Type: typ, Message: msg})
		BroadcastLog(entry)
	}))
	t.Cleanup(func() { plog.RemoveHandler("broker-test") })

	const lines = 10 * logBufferSize

	dropped := logsDropped.Load()

	polltest.Within(t, "logging with the broker stopped", func() {
		for i := range lines {
			plog.Warn(plog.TypeSystem, "line", "n", i)
		}
	})

	plog.RemoveHandler("broker-test")

	if n := logsDropped.Load() - dropped; n != lines-logBufferSize {
		t.Errorf("%d plog.Warn lines with the broker stopped dropped %d, want %d", lines, n, lines-logBufferSize)
	}

	runBroker(t)

	// a line sent while the queue is still full would be dropped too
	polltest.Until(t, "the broker taking the queued lines", func() bool { return len(logs) == 0 })

	BroadcastLog(json.RawMessage(`{"msg": "after"}`))
	Broadcast(nil, bt.NewResource("experiment", "exp", "start"), nil)

	// the lines that fit, the note of those that did not, the line after
	// them, and the state message, which may come between any of them
	var (
		got   []encodedPublish
		state int
	)

	polltest.Within(t, "the reader receiving the lines", func() {
		for len(got)+state < logBufferSize+3 {
			out, _ := (<-reader.publish).(encodedPublish)
			if out.pub.Resource.Type == "experiment" {
				state++
			} else {
				got = append(got, out)
			}
		}
	})

	if state != 1 || len(got) != logBufferSize+2 {
		t.Fatalf("subscribed reader received %d log lines and %d state messages, want %d and 1", len(got), state, logBufferSize+2)
	}

	var skipped plog.LogEntry
	if err := json.Unmarshal(got[logBufferSize].pub.Result, &skipped); err != nil {
		t.Fatalf("decoding the skipped-lines note %s: %v", got[logBufferSize].pub.Result, err)
	}

	want := fmt.Sprintf("%d log lines skipped", lines-logBufferSize)
	if !strings.HasPrefix(skipped.Message, want) || skipped.Level != slog.LevelWarn.String() {
		t.Errorf("skipped-lines note = %s %q, want %s %q...", skipped.Level, skipped.Message, slog.LevelWarn, want)
	}

	var after map[string]any
	if err := json.Unmarshal(got[logBufferSize+1].pub.Result, &after); err != nil {
		t.Fatalf("decoding the line after the note %s: %v", got[logBufferSize+1].pub.Result, err)
	}

	if diff := cmp.Diff(map[string]any{"msg": "after"}, after); diff != "" {
		t.Errorf("BroadcastLog line after the note mismatch (-want +got):\n%s", diff)
	}
}

// The write loop pings with a control frame, which may go out alongside the
// close frame of a shutdown; the data writer stays single.
//
//nolint:paralleltest // shortens the ping period
func TestPingDoesNotRaceShutdown(t *testing.T) {
	saved := pingPeriod
	pingPeriod = time.Millisecond

	t.Cleanup(func() { pingPeriod = saved })

	for range 20 {
		conn := newFakeConn(t)
		cli := newFakeClient(conn)

		wrote := make(chan struct{})

		go func() {
			defer close(wrote)

			cli.write()
		}()

		cli.publish <- encodedPublish{data: []byte("{}")}

		polltest.Within(t, "a ping", func() {
			for f := range conn.controls {
				if f.kind == websocket.PingMessage {
					return
				}
			}
		})

		cli.shutdown(websocket.CloseNormalClosure, "")

		polltest.Within(t, "the write loop ending", func() { <-wrote })
	}
}

// withDeps restores the client's store and minimega calls after the test.
func withDeps(t *testing.T) {
	t.Helper()

	saved := deps

	t.Cleanup(func() { deps = saved })
}

// startFakeClient runs a client on conn with all its loops but the
// screenshot ticker, until the test ends.
func startFakeClient(t *testing.T, conn *fakeConn, role rbac.Role) *Client {
	t.Helper()

	c := newFakeClient(conn)
	c.role = role

	go c.read()
	go c.write()
	go c.work()

	t.Cleanup(c.Stop)

	return c
}

func listRequest(name string) []byte {
	return []byte(`{"resource": {"type": "experiment/vms", "name": "` + name + `", "action": "list"}, "request": {}}`)
}

// replies returns the names of the VM lists conn has been sent.
func vmListReplies(conn *fakeConn) []string {
	var names []string

	for _, msg := range conn.messages() {
		var pub bt.Publish
		if json.Unmarshal(msg, &pub) == nil && pub.Resource != nil && pub.Resource.Type == "experiment/vms" {
			names = append(names, pub.Resource.Name)
		}
	}

	return names
}

// gateVMs holds each VM list's minimega read until release, signaling entered
// as one starts waiting, and answers with vms.
func gateVMs(t *testing.T, vms ...mm.VM) (<-chan struct{}, func()) {
	t.Helper()

	var (
		entered = make(chan struct{}, 1)
		gate    = make(chan struct{})
		release = sync.OnceFunc(func() { close(gate) })
	)

	t.Cleanup(release)

	deps.vms = func(context.Context, *types.Experiment) ([]mm.VM, freshness.Observation) {
		select {
		case entered <- struct{}{}:
		default:
		}

		<-gate

		return slices.Clone(vms), freshness.Observed(time.Now())
	}

	return entered, release
}

// Of the VM lists asked for while one is answered, however many, only the
// newest is answered next.
//
//nolint:paralleltest // replaces the store and the client's dependencies
func TestListRequestsAreLatestWins(t *testing.T) {
	useExperiment(t)
	withDeps(t)

	var (
		mu    sync.Mutex
		asked []string
	)

	deps.experiment = func(name string) (*types.Experiment, error) {
		mu.Lock()
		asked = append(asked, name)
		mu.Unlock()

		return experiment.Get("exp")
	}

	entered, release := gateVMs(t)

	conn := newFakeConn(t)
	c := startFakeClient(t, conn, roleAllowing("vms", "list"))

	conn.reads <- listRequest("first")

	polltest.Within(t, "the first list asking minimega", func() { <-entered })

	// the client keeps reading while the list waits
	conn.reads <- []byte(`{"resource": {"type": "log", "name": "phenix", "action": "subscribe"}}`)

	polltest.Until(t, "the logs subscription is read", c.logsSubscribed.Load)

	// more than the worker's queue would hold
	for i := range 2 * clientJobQueue {
		conn.reads <- listRequest(strconv.Itoa(i))
	}

	conn.reads <- listRequest("last")

	polltest.Until(t, "the last list is queued", func() bool { return nextListName(c) == "last" })

	release()

	var got []string

	polltest.Until(t, "two list replies", func() bool {
		got = append(got, vmListReplies(conn)...)

		return len(got) >= 2
	})

	mu.Lock()
	defer mu.Unlock()

	want := []string{"first", "last"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("VM list replies mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, asked); diff != "" {
		t.Errorf("experiments the VM lists read mismatch (-want +got):\n%s", diff)
	}
}

// nextListName returns the experiment of the VM list c answers next, if any.
func nextListName(c *Client) string {
	c.vmMu.RLock()
	defer c.vmMu.RUnlock()

	if c.nextList == nil {
		return ""
	}

	return c.nextList.req.Resource.Name
}

// A VM list the client leaves its VM table during, or before it is answered,
// puts no VMs back in view, so they are not screenshotted.
//
//nolint:paralleltest // replaces the store and the client's dependencies
func TestUnsubscribeOutlastsPendingLists(t *testing.T) {
	useExperiment(t, map[string]any{"general": map[string]any{"hostname": "vm1"}})
	withDeps(t)

	var (
		mu    sync.Mutex
		asked []string
	)

	deps.experiment = func(name string) (*types.Experiment, error) {
		mu.Lock()
		asked = append(asked, name)
		mu.Unlock()

		return experiment.Get("exp")
	}

	entered, release := gateVMs(t, mm.VM{Name: "vm1", Running: true})

	shots := make(chan string, 8)

	deps.screenshot = func(_ context.Context, exp, vm, _ string) ([]byte, error) {
		shots <- exp + "/" + vm

		return []byte("png"), nil
	}

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms"}, ResourceNames: []string{"*/*"}, Verbs: []string{"list"}},
	}}}

	conn := newFakeConn(t)
	c := startFakeClient(t, conn, role)

	conn.reads <- listRequest("running")

	polltest.Within(t, "the first list asking minimega", func() { <-entered })

	conn.reads <- listRequest("queued")

	polltest.Until(t, "the second list is queued", func() bool { return nextListName(c) == "queued" })

	conn.reads <- []byte(`{"resource": {"type": "experiment/vms", "name": "exp", "action": "unsubscribe"}}`)

	polltest.Until(t, "the unsubscribe is read", func() bool { return nextListName(c) == "" })

	release()

	// a list asked for after the unsubscribe is answered as usual, and shows
	// the worker is done with the ones before it
	conn.reads <- listRequest("after")

	var got []string

	polltest.Until(t, "the list after the unsubscribe", func() bool {
		got = append(got, vmListReplies(conn)...)

		return len(got) > 0
	})

	mu.Lock()
	defer mu.Unlock()

	if diff := cmp.Diff([]string{"after"}, got); diff != "" {
		t.Errorf("VM list replies mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"running", "after"}, asked); diff != "" {
		t.Errorf("experiments the VM lists read mismatch (-want +got):\n%s", diff)
	}

	want := []vmScope{{exp: "after", name: "vm1", running: true}}
	if diff := cmp.Diff(want, vmsInView(c), cmp.AllowUnexported(vmScope{})); diff != "" {
		t.Errorf("VMs in view mismatch (-want +got):\n%s", diff)
	}

	var shot string

	polltest.Within(t, "the screenshot of the list's VM", func() { shot = <-shots })

	if shot != "after/vm1" || len(shots) != 0 {
		t.Errorf("screenshots asked for = %s and %d more, want after/vm1 only", shot, len(shots))
	}
}

// A client that leaves stops waiting on a screenshot minimega is still
// taking.
func TestScreenshotWaitEndsWithTheClient(t *testing.T) {
	withDeps(t)

	var (
		entered = make(chan struct{}, 1)
		gate    = make(chan struct{})
	)

	t.Cleanup(func() { close(gate) })

	deps.screenshot = func(ctx context.Context, _, _, _ string) ([]byte, error) {
		entered <- struct{}{}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-gate:
			return []byte("png"), nil
		}
	}

	c := newFakeClient(newFakeConn(t))
	setVMs(c, vmScope{exp: "exp", name: "vm1", running: true}, vmScope{exp: "exp", name: "vm2", running: true})

	pushed := make(chan struct{})

	go func() {
		defer close(pushed)

		c.pushScreenshots()
	}()

	polltest.Within(t, "the first screenshot", func() { <-entered })

	c.shutdown(websocket.CloseNormalClosure, "")

	polltest.Within(t, "the screenshots giving up", func() { <-pushed })

	if len(entered) != 0 {
		t.Error("pushScreenshots() asked for the second screenshot after the client left, want it to stop")
	}
}

// A VM list's seq is taken before the minimega read, so a broadcast during it
// orders after the list.
//
//nolint:paralleltest // replaces the store and the client's dependencies
func TestListSeqPrecedesBroadcastDuringRead(t *testing.T) {
	useExperiment(t)
	withDeps(t)

	entered, release := gateVMs(t)

	conn := newFakeConn(t)
	startFakeClient(t, conn, roleAllowing("vms", "list"))

	conn.reads <- listRequest("exp")

	polltest.Within(t, "the list asking minimega", func() { <-entered })

	Broadcast(nil, bt.NewResource("experiment/vm", "exp/vm1", "stop"), json.RawMessage(`{"name":"vm1"}`))

	stop := <-broadcast

	release()

	var list bt.Publish

	polltest.Until(t, "the list reply", func() bool {
		for _, msg := range conn.messages() {
			if json.Unmarshal(msg, &list) == nil && list.Resource != nil && list.Resource.Type == "experiment/vms" {
				return true
			}
		}

		return false
	})

	if list.Seq == 0 || list.Seq >= stop.pub.Seq {
		t.Errorf("VM list seq = %d, want nonzero and below the broadcast's %d", list.Seq, stop.pub.Seq)
	}
}
