package livecapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/golang/mock/gomock"

	"phenix/store"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// fakeCluster stands in for minimega, and the store, for a running
// experiment "exp" whose VM "vm" has interfaces 0 and 1 captured (while
// capturing is set), on the headnode or on node compute1. The capture files
// are in dir; compute1 reports them under /phenix/images/exp/files, and they
// are read from dir through a fake minimega.
type fakeCluster struct {
	mm.MM

	dir       string
	host      string
	running   atomic.Bool
	capturing atomic.Bool
	lookups   atomic.Int32 // of the VM's captures

	mu      sync.Mutex
	release chan struct{} // while set, mesh reads wait for it to close
	waiting chan struct{} // closed once a mesh read waits on release
}

func (c *fakeCluster) GetVMCaptures(...mm.Option) []mm.Capture {
	c.lookups.Add(1)

	if !c.capturing.Load() {
		return nil
	}

	return []mm.Capture{
		{VM: "vm", Interface: 0, Filepath: c.capturePath(0)},
		{VM: "vm", Interface: 1, Filepath: c.capturePath(1)},
	}
}

func (c *fakeCluster) GetVMHost(...mm.Option) (string, error) {
	return c.host, nil
}

func (c *fakeCluster) IsHeadnode(host string) bool {
	return host == "headnode"
}

// path is where the interface's capture file is.
func (c *fakeCluster) path(iface int) string {
	return filepath.Join(c.dir, fmt.Sprintf("vm-%d.pcap", iface))
}

// capturePath is where minimega reports the interface's capture file.
func (c *fakeCluster) capturePath(iface int) string {
	if c.host == "headnode" {
		return c.path(iface)
	}

	return fmt.Sprintf("/phenix/images/exp/files/vm-%d.pcap", iface)
}

// meshRead answers a mesh read of compute1's file, as meshReadScript would.
func (c *fakeCluster) meshRead(cmd mmtest.Command) []*minicli.Response {
	fields := strings.Fields(cmd.Base)

	if !strings.HasPrefix(cmd.Base, "mesh send compute1 shell sh -c ") || len(fields) < 3 {
		return []*minicli.Response{{Host: "head", Error: "unexpected command " + cmd.Base}}
	}

	c.mu.Lock()
	release := c.release

	if c.waiting != nil {
		close(c.waiting)
		c.waiting = nil
	}
	c.mu.Unlock()

	if release != nil {
		<-release
	}

	path := filepath.Join(c.dir, filepath.Base(fields[len(fields)-3]))
	off, _ := strconv.Atoi(fields[len(fields)-2])
	n, _ := strconv.Atoi(fields[len(fields)-1])

	data, err := os.ReadFile(path)
	if err != nil {
		return []*minicli.Response{{Host: "compute1", Error: err.Error()}}
	}

	data = data[min(off, len(data)):min(off+n, len(data))]

	return []*minicli.Response{mmtest.Text("compute1", base64.StdEncoding.EncodeToString(data))}
}

// stall makes mesh reads wait, as a read of a node that does not answer
// does, until release is called. waiting is closed once a read is waiting.
func (c *fakeCluster) stall() (<-chan struct{}, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.release, c.waiting = make(chan struct{}), make(chan struct{})

	return c.waiting, sync.OnceFunc(func() { close(c.release) })
}

// useFakeCluster points the package at a fake cluster, with its captures on
// the headnode or on another node, and shortens its timings for the test. An
// unused Tap is kept until the test ends; set idleGrace before a Release to
// have it expire sooner.
func useFakeCluster(t *testing.T, remote bool) *fakeCluster {
	t.Helper()

	c := &fakeCluster{dir: t.TempDir(), host: "headnode"}
	if remote {
		c.host = "compute1"
	}

	for iface := range 2 {
		if err := os.WriteFile(c.path(iface), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	c.running.Store(true)
	c.capturing.Store(true)

	ctrl := gomock.NewController(t)

	st := store.NewMockStore(ctrl)
	st.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		status := map[string]any{}
		if c.running.Load() {
			status["startTime"] = "2024-01-01T00:00:00Z"
		}

		*cfg = store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: map[string]any{
				"experimentName": "exp",
				"topology":       map[string]any{"nodes": []map[string]any{}},
			},
			Status: status,
		}

		return nil
	}).AnyTimes()

	var (
		originalStore = store.DefaultStore
		originalMM    = mm.DefaultMM
		polls         = [4]time.Duration{localPoll, remotePoll, activeCheck, idleGrace}
	)

	store.DefaultStore = st //nolint:reassign // install test double
	mm.DefaultMM = c        //nolint:reassign // install test double

	mmtest.Use(t, c.meshRead)

	t.Cleanup(func() {
		// stop following before what it reads is put back
		CloseAll()

		store.DefaultStore = originalStore //nolint:reassign // restore default
		mm.DefaultMM = originalMM          //nolint:reassign // restore default
		localPoll, remotePoll, activeCheck, idleGrace = polls[0], polls[1], polls[2], polls[3]
	})

	localPoll, remotePoll, activeCheck, idleGrace = 5*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, time.Hour

	return c
}

// write appends to the file minimega captures interface 0 to.
func (c *fakeCluster) write(t *testing.T, b []byte) {
	t.Helper()

	f, err := os.OpenFile(c.path(0), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

// waitFor waits for the Tap to reach a state.
func waitFor(t *testing.T, tap *Tap, what string, ok func(State) bool) {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		st := tap.State()
		if ok(st) {
			return
		}

		select {
		case <-st.Changed:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; state %+v", what, st)
		}
	}
}

// stream is a Stream of a Tap into a buffer. out and sends are only read
// once done has returned.
type stream struct {
	done    chan error    // what Stream returned
	started chan struct{} // closed once the file header has been sent
	out     bytes.Buffer
	sends   []int // the length of each send
}

// collect streams the Tap into a buffer until it returns.
func collect(tap *Tap, fromNow bool) *stream {
	s := &stream{done: make(chan error, 1), started: make(chan struct{})}

	go func() {
		s.done <- tap.Stream(context.Background(), fromNow, func(b []byte) error {
			s.out.Write(b)
			s.sends = append(s.sends, len(b))

			if len(s.sends) == 1 {
				close(s.started)
			}

			return nil
		})
	}()

	return s
}

func TestTapFollowsCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	for _, remote := range []bool{false, true} {
		name := "headnode"
		if remote {
			name = "remote node"
		}

		t.Run(name, func(t *testing.T) {
			cluster := useFakeCluster(t, remote)

			var (
				header = pcapFile(binary.LittleEndian, 0xa1b2c3d4)
				one    = record(binary.LittleEndian, 60)
				two    = record(binary.LittleEndian, 1500)
			)

			// minimega has written the header, one record and part of the next
			cluster.write(t, header)
			cluster.write(t, one)
			cluster.write(t, two[:700])

			tap, err := Open("exp", "vm", 0)
			if err != nil {
				t.Fatal(err)
			}

			defer tap.Release()

			waitFor(t, tap, "the first record", func(st State) bool { return st.CaughtUp })

			if got, want := tap.State().Size, int64(len(header)+len(one)); got != want {
				t.Fatalf("got %d readable bytes, want %d: only whole records", got, want)
			}

			s := collect(tap, false)

			cluster.write(t, two[700:])
			waitFor(t, tap, "the second record", func(st State) bool {
				return st.Size == int64(len(header)+len(one)+len(two))
			})

			cluster.capturing.Store(false)

			select {
			case err := <-s.done:
				if err != nil {
					t.Fatalf("stream ended with %v, want nil once the capture stops", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not end when the capture stopped")
			}

			want := bytes.Join([][]byte{header, one, two}, nil)
			if !bytes.Equal(s.out.Bytes(), want) {
				t.Fatalf("streamed %d bytes, want the %d of the whole capture", s.out.Len(), len(want))
			}

			if s.sends[0] != fileHeaderLen {
				t.Fatalf("first send is %d bytes, want the file header alone", s.sends[0])
			}

			whole, err := io.ReadAll(io.NewSectionReader(tap, 0, tap.State().Size))
			if err != nil || !bytes.Equal(whole, want) {
				t.Fatalf("reading the Tap: %v; equal %v", err, bytes.Equal(whole, want))
			}
		})
	}
}

func TestTapStreamFromNow(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)

	var (
		header = pcapFile(binary.BigEndian, 0xa1b2c3d4)
		old    = record(binary.BigEndian, 100)
		fresh  = record(binary.BigEndian, 200)
	)

	cluster.write(t, append(header, old...))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer tap.Release()

	// what was captured before the stream starts is skipped, what comes after
	// is streamed
	waitFor(t, tap, "the backlog", func(st State) bool { return st.CaughtUp })

	s := collect(tap, true)

	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
		t.Fatal("stream sent no file header")
	}

	cluster.write(t, fresh)
	waitFor(t, tap, "the new record", func(st State) bool {
		return st.Size == int64(len(header)+len(old)+len(fresh))
	})

	cluster.capturing.Store(false)

	if err := <-s.done; err != nil {
		t.Fatal(err)
	}

	if want := append(append([]byte{}, header...), fresh...); !bytes.Equal(s.out.Bytes(), want) {
		t.Fatalf("streamed %d bytes, want the header and the new record (%d)", s.out.Len(), len(want))
	}
}

func TestTapStopsOnCorruptCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)
	cluster.write(t, bytes.Repeat([]byte{0x0a}, 64))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer tap.Release()

	waitFor(t, tap, "the Tap to give up", func(st State) bool { return st.Done })

	if err := <-collect(tap, false).done; !errors.Is(err, errNotPcap) {
		t.Fatalf("stream ended with %v, want errNotPcap", err)
	}
}

func TestOpenSharesOneTapPerRunningCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, true)

	// no check of a running capture's state, which would ask minimega too
	activeCheck = time.Hour

	// a stopped experiment has no captures, and asking minimega about one
	// would recreate its namespace
	cluster.running.Store(false)

	if _, err := Open("exp", "vm", 0); !errors.Is(err, ErrNotCapturing) || cluster.lookups.Load() != 0 {
		t.Fatalf("Open in a stopped experiment = %v after %d lookups; want ErrNotCapturing, minimega not asked",
			err, cluster.lookups.Load())
	}

	cluster.running.Store(true)

	first, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer first.Release()

	second, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer second.Release()

	if first != second {
		t.Fatal("a second reader of the same capture got a Tap of its own")
	}

	if n := cluster.lookups.Load(); n != 1 {
		t.Fatalf("asked minimega for the capture %d times, want once", n)
	}

	other, err := Open("exp", "vm", 1)
	if err != nil {
		t.Fatalf("another interface: %v", err)
	}

	defer other.Release()

	if other == first {
		t.Fatal("another interface shares the first interface's Tap")
	}

	if _, err := Open("exp", "vm", 2); !errors.Is(err, ErrNotCapturing) {
		t.Fatalf("Open of an interface not captured = %v, want ErrNotCapturing", err)
	}
}

func TestOpenKeepsAStoppedCaptureUntilItExpires(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, true)
	cluster.write(t, pcapFile(binary.LittleEndian, 0xa1b2c3d4))
	cluster.write(t, record(binary.LittleEndian, 42))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, tap, "the record", func(st State) bool { return st.CaughtUp && st.Size > fileHeaderLen })

	spool := tap.spool
	if _, err := os.Stat(spool); err != nil {
		t.Fatalf("remote capture has no spool: %v", err)
	}

	// the capture stops: the finished Tap still serves all of it
	cluster.capturing.Store(false)
	waitFor(t, tap, "the capture to stop", func(st State) bool { return st.Done })

	tap.Release()

	again, err := Open("exp", "vm", 0)
	if err != nil || again != tap {
		t.Fatalf("Open of a stopped capture = %p, %v; want the finished Tap %p", again, err, tap)
	}

	idleGrace = 50 * time.Millisecond

	again.Release()

	// once nobody has used it for the grace period, it is gone. Expiry drops
	// the Tap from the registry before it stops following and removes the
	// spool, so wait for both rather than only the registry.
	deadline := time.Now().Add(5 * time.Second)

	for {
		registryMu.Lock()
		_, held := registry["exp/vm/0"]
		registryMu.Unlock()

		_, statErr := os.Stat(spool)

		if !held && os.IsNotExist(statErr) {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("unused Tap never expired: in registry %t, spool %v", held, statErr)
		}

		time.Sleep(10 * time.Millisecond)
	}

	if _, err := Open("exp", "vm", 0); !errors.Is(err, ErrNotCapturing) {
		t.Fatalf("Open after expiry = %v, want ErrNotCapturing", err)
	}
}

func TestCloseAllStopsEveryTap(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, true)

	var (
		header = pcapFile(binary.LittleEndian, 0xa1b2c3d4)
		one    = record(binary.LittleEndian, 42)
	)

	cluster.write(t, append(header, one...))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer tap.Release()

	waitFor(t, tap, "the record", func(st State) bool { return st.CaughtUp })

	s := collect(tap, false)

	// the node stops answering while the Tap is reading from it
	waiting, release := cluster.stall()
	defer release()

	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("the Tap stopped reading its capture")
	}

	closed := make(chan struct{})

	go func() {
		CloseAll()
		close(closed)
	}()

	// the spool goes at once, without waiting for that read
	deadline := time.Now().Add(5 * time.Second)

	for {
		_, err := os.Stat(tap.spool)
		if os.IsNotExist(err) {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("spool still there while a read is in flight: %v", err)
		}

		time.Sleep(time.Millisecond)
	}

	release()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll did not return once the read did")
	}

	// a reader still holding the Tap gets what was read, then its stream ends
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("stream ended with %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end")
	}

	if want := append(append([]byte{}, header...), one...); !bytes.Equal(s.out.Bytes(), want) {
		t.Fatalf("streamed %d bytes, want the %d read before closing", s.out.Len(), len(want))
	}

	// the capture is still running: the next reader follows it afresh
	again, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatal(err)
	}

	defer again.Release()

	if again == tap {
		t.Fatal("Open after CloseAll returned the closed Tap")
	}
}
