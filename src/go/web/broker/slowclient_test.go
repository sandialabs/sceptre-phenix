package broker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"phenix/api/experiment"
	"phenix/types"
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
		within(t, "broadcasting a batch", func() {
			for range batch {
				Broadcast(nil, bt.NewResource("experiment", strconv.Itoa(sent), "update"), nil)
				sent++
			}
		})

		within(t, "the fast client receiving its batch", func() {
			for len(got) < sent {
				got = append(got, fastConn.messages()...)

				time.Sleep(time.Millisecond)
			}
		})
	}

	for i, msg := range got {
		if !strings.Contains(string(msg), fmt.Sprintf(`"name":"%d"`, i)) {
			t.Fatalf("message %d = %s, want message %d", i, msg, i)
		}
	}

	within(t, "dropping the slow client", func() { <-slow.done })
	within(t, "closing the slow client's connection", func() { <-slowConn.closed })

	select {
	case f := <-slowConn.controls:
		if f.kind != websocket.CloseMessage || binary.BigEndian.Uint16(f.data) != websocket.CloseTryAgainLater {
			t.Errorf("slow client got control frame %d %q, want close %d",
				f.kind, f.data, websocket.CloseTryAgainLater)
		}
	default:
		t.Error("slow client was sent no close frame")
	}

	if n := slowDropped.Load() - dropped; n != 1 {
		t.Errorf("slow clients dropped = %d, want 1", n)
	}

	if isClosed(fast.done) {
		t.Error("the fast client was dropped")
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

	within(t, "the write starting", func() { <-conn.writing })

	start := time.Now()

	drop(cli)

	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("drop took %v", d)
	}

	within(t, "the stuck write ending", func() { <-wrote })
}

// Log lines never wait on the broker: with its loop stopped, logging goes on,
// and the lines that did not fit are counted, then announced to the clients
// showing logs once the loop runs again.
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

	within(t, "logging with the broker stopped", func() {
		for i := range lines {
			plog.Warn(plog.TypeSystem, "line", "n", i)
		}
	})

	plog.RemoveHandler("broker-test")

	if n := logsDropped.Load() - dropped; n != lines-logBufferSize {
		t.Errorf("log lines dropped = %d, want %d", n, lines-logBufferSize)
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

	within(t, "the reader receiving the lines", func() {
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
		t.Fatalf("reader got %d log lines and %d state messages, want %d and 1", len(got), state, logBufferSize+2)
	}

	var skipped plog.LogEntry
	if err := json.Unmarshal(got[logBufferSize].pub.Result, &skipped); err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprintf("%d log lines skipped", lines-logBufferSize)
	if !strings.HasPrefix(skipped.Message, want) || skipped.Level != slog.LevelWarn.String() {
		t.Errorf("skipped note = %s %q, want WARN %q...", skipped.Level, skipped.Message, want)
	}

	if string(got[logBufferSize+1].pub.Result) != `{"msg": "after"}` {
		t.Errorf("line after the note = %s", got[logBufferSize+1].pub.Result)
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

		within(t, "a ping", func() {
			for f := range conn.controls {
				if f.kind == websocket.PingMessage {
					return
				}
			}
		})

		cli.shutdown(websocket.CloseNormalClosure, "")

		within(t, "the write loop ending", func() { <-wrote })
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

// While a VM list waits on minimega, the client's other requests are still
// read, and so are its pongs.
//
//nolint:paralleltest // replaces the store and the client's dependencies
func TestReadKeepsReadingWhileAListIsSlow(t *testing.T) {
	useExperiment(t)
	withDeps(t)

	var (
		entered = make(chan struct{}, 1)
		gate    = make(chan struct{})
		release = sync.OnceFunc(func() { close(gate) })
	)

	t.Cleanup(release)

	deps.vms = func(*types.Experiment) []mm.VM {
		entered <- struct{}{}
		<-gate

		return nil
	}

	conn := newFakeConn(t)
	c := startFakeClient(t, conn, roleAllowing("vms", "list"))

	conn.reads <- listRequest("exp")

	within(t, "the list asking minimega", func() { <-entered })

	conn.reads <- []byte(`{"resource": {"type": "log", "name": "phenix", "action": "subscribe"}}`)

	polltest.Until(t, "the logs subscription is read", c.logsSubscribed.Load)

	release()

	var got []string

	polltest.Until(t, "the list reply", func() bool {
		got = append(got, vmListReplies(conn)...)

		return len(got) > 0
	})

	if !slices.Equal(got, []string{"exp"}) {
		t.Errorf("VM list replies = %v, want [exp]", got)
	}
}

// Of the VM lists asked for while one is answered, only the newest is
// answered next.
//
//nolint:paralleltest // replaces the store and the client's dependencies
func TestListRequestsAreLatestWins(t *testing.T) {
	useExperiment(t)
	withDeps(t)

	var (
		entered = make(chan struct{}, 1)
		gate    = make(chan struct{})
		release = sync.OnceFunc(func() { close(gate) })
		mu      sync.Mutex
		asked   []string
	)

	t.Cleanup(release)

	deps.experiment = func(name string) (*types.Experiment, error) {
		mu.Lock()
		asked = append(asked, name)
		mu.Unlock()

		return experiment.Get("exp")
	}

	first := true

	deps.vms = func(*types.Experiment) []mm.VM {
		if first {
			first = false
			entered <- struct{}{}
			<-gate
		}

		return nil
	}

	conn := newFakeConn(t)
	c := startFakeClient(t, conn, roleAllowing("vms", "list"))

	conn.reads <- listRequest("first")

	within(t, "the first list asking minimega", func() { <-entered })

	for _, name := range []string{"second", "third", "last"} {
		conn.reads <- listRequest(name)
	}

	polltest.Until(t, "the lists are queued", func() bool { return len(c.jobs) == 3 })

	release()

	var got []string

	polltest.Until(t, "two list replies", func() bool {
		got = append(got, vmListReplies(conn)...)

		return len(got) >= 2
	})

	mu.Lock()
	defer mu.Unlock()

	if want := []string{"first", "last"}; !slices.Equal(got, want) || !slices.Equal(asked, want) {
		t.Errorf("lists answered = %v, store asked for %v; want %v", got, asked, want)
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

	within(t, "the first screenshot", func() { <-entered })

	c.shutdown(websocket.CloseNormalClosure, "")

	within(t, "the screenshots giving up", func() { <-pushed })

	if len(entered) != 0 {
		t.Error("asked for the second screenshot after the client left")
	}
}
