package livecapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// fakeCluster fakes minimega and the store for "exp" capturing "vm" interfaces 0 and 1 into dir; compute1
// reports those files under /phenix/images/exp/files, and they are read from dir through a fake minimega.
type fakeCluster struct {
	mm.MM

	dir       string
	host      string
	running   atomic.Bool
	capturing atomic.Bool
	lookups   atomic.Int32 // of the VM's captures
	nsLookups atomic.Int32 // of the experiment captures
	nsFails   atomic.Int32 // the experiment capture listings still to fail

	// the state minimega lists the VM's captures in, "" for an older minimega
	vmState atomic.Value

	mu      sync.Mutex
	release chan struct{} // while set, mesh reads wait for it to close
	waiting chan struct{} // closed once a mesh read waits on release
}

func (c *fakeCluster) GetVMCaptures(...mm.Option) []mm.Capture {
	c.lookups.Add(1)

	if !c.capturing.Load() {
		return nil
	}

	state, _ := c.vmState.Load().(string)

	return []mm.Capture{
		{VM: "vm", Interface: 0, Filepath: c.capturePath(0), State: state},
		{VM: "vm", Interface: 1, Filepath: c.capturePath(1), State: state},
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

// meshReadArgs matches the file, offset and count in a meshReadScript script.
var meshReadArgs = regexp.MustCompile(`dd if=(\S+) iflag=skip_bytes,count_bytes skip=(\d+) count=(\d+) `)

// meshRead answers a mesh read of compute1's file, as a meshReadScript script would.
func (c *fakeCluster) meshRead(cmd mmtest.Command) []*minicli.Response {
	fields := meshReadArgs.FindStringSubmatch(cmd.Base)

	if !strings.HasPrefix(cmd.Base, "mesh send compute1 shell sh -c ") || fields == nil {
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

	path := filepath.Join(c.dir, filepath.Base(fields[1]))
	off, _ := strconv.Atoi(fields[2])
	n, _ := strconv.Atoi(fields[3])

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

// useFakeCluster fakes a cluster capturing on the headnode or another node, with short timings. An unused
// Tap is kept until the test ends; set idleGrace before a Release to expire it sooner.
func useFakeCluster(t *testing.T, remote bool) *fakeCluster {
	t.Helper()

	c := &fakeCluster{dir: t.TempDir(), host: "headnode"}
	if remote {
		c.host = "compute1"
	}

	for iface := range 2 {
		if err := os.WriteFile(c.path(iface), nil, 0o600); err != nil {
			t.Fatalf("creating the capture file of interface %d: %v", iface, err)
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
		t.Fatalf("opening the capture file: %v", err)
	}

	defer f.Close()

	if _, err := f.Write(b); err != nil {
		t.Fatalf("appending to the capture file: %v", err)
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
			t.Fatalf("Tap.State() = %+v after 5s, want %s", st, what)
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
				t.Fatalf("Open(exp, vm, 0) error = %v", err)
			}

			defer tap.Release()

			waitFor(t, tap, "the first record", func(st State) bool { return st.CaughtUp })

			// only whole records are readable
			if got, want := tap.State().Size, int64(len(header)+len(one)); got != want {
				t.Errorf("Tap.State().Size = %d, want %d", got, want)
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
					t.Errorf("Stream(fromNow false) after the capture stops error = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Stream(fromNow false) still running 5s after the capture stopped")
			}

			want := bytes.Join([][]byte{header, one, two}, nil)
			if diff := cmp.Diff(want, s.out.Bytes()); diff != "" {
				t.Errorf("Stream(fromNow false) bytes mismatch (-want +got):\n%s", diff)
			}

			// the file header is sent alone
			if len(s.sends) == 0 || s.sends[0] != fileHeaderLen {
				t.Errorf("Stream(fromNow false) send sizes = %v, want the first to be %d", s.sends, fileHeaderLen)
			}

			whole, err := io.ReadAll(io.NewSectionReader(tap, 0, tap.State().Size))
			if err != nil {
				t.Errorf("reading the whole Tap error = %v", err)
			}

			if diff := cmp.Diff(want, whole); diff != "" {
				t.Errorf("reading the whole Tap mismatch (-want +got):\n%s", diff)
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
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer tap.Release()

	// what was captured before the stream starts is skipped, what comes after
	// is streamed
	waitFor(t, tap, "the backlog", func(st State) bool { return st.CaughtUp })

	s := collect(tap, true)

	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Stream(fromNow true) sent no file header within 5s")
	}

	cluster.write(t, fresh)
	waitFor(t, tap, "the new record", func(st State) bool {
		return st.Size == int64(len(header)+len(old)+len(fresh))
	})

	cluster.capturing.Store(false)

	if err := <-s.done; err != nil {
		t.Errorf("Stream(fromNow true) after the capture stops error = %v, want nil", err)
	}

	// the header and the new record
	if diff := cmp.Diff(append(append([]byte{}, header...), fresh...), s.out.Bytes()); diff != "" {
		t.Errorf("Stream(fromNow true) bytes mismatch (-want +got):\n%s", diff)
	}
}

func TestTapStopsOnCorruptCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)
	cluster.write(t, bytes.Repeat([]byte{0x0a}, 64))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer tap.Release()

	waitFor(t, tap, "the Tap to give up", func(st State) bool { return st.Done })

	if err := <-collect(tap, false).done; !errors.Is(err, errNotPcap) {
		t.Errorf("Stream(fromNow false) of a corrupt capture error = %v, want %v", err, errNotPcap)
	}
}

func TestOpenSharesOneTapPerRunningCapture(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, true)

	// no check of a running capture's state, which would ask minimega too
	activeCheck = time.Hour

	// a stopped experiment has no captures, and asking minimega about one
	// would recreate its namespace
	cluster.running.Store(false)

	if _, err := Open("exp", "vm", 0); !errors.Is(err, ErrNotCapturing) {
		t.Errorf("Open(exp, vm, 0) in a stopped experiment error = %v, want %v", err, ErrNotCapturing)
	}

	if n := cluster.lookups.Load(); n != 0 {
		t.Errorf("GetVMCaptures calls after Open() in a stopped experiment = %d, want 0", n)
	}

	cluster.running.Store(true)

	first, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer first.Release()

	second, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("second Open(exp, vm, 0) error = %v", err)
	}

	defer second.Release()

	if first != second {
		t.Errorf("second Open(exp, vm, 0) = %p, want the first Tap %p", second, first)
	}

	if n := cluster.lookups.Load(); n != 1 {
		t.Errorf("GetVMCaptures calls after two Open(exp, vm, 0) = %d, want 1", n)
	}

	other, err := Open("exp", "vm", 1)
	if err != nil {
		t.Fatalf("Open(exp, vm, 1) error = %v", err)
	}

	defer other.Release()

	if other == first {
		t.Errorf("Open(exp, vm, 1) = %p, the Tap of interface 0; want a Tap of its own", other)
	}

	if _, err := Open("exp", "vm", 2); !errors.Is(err, ErrNotCapturing) {
		t.Errorf("Open(exp, vm, 2) of an interface not captured error = %v, want %v", err, ErrNotCapturing)
	}
}

func TestOpenKeepsAStoppedCaptureUntilItExpires(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, true)
	cluster.write(t, pcapFile(binary.LittleEndian, 0xa1b2c3d4))
	cluster.write(t, record(binary.LittleEndian, 42))

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	waitFor(t, tap, "the record", func(st State) bool { return st.CaughtUp && st.Size > fileHeaderLen })

	spool := tap.spool
	if _, err := os.Stat(spool); err != nil {
		t.Errorf("os.Stat(spool of a remote capture) error = %v", err)
	}

	// the capture stops: the finished Tap still serves all of it
	cluster.capturing.Store(false)
	waitFor(t, tap, "the capture to stop", func(st State) bool { return st.Done })

	tap.Release()

	again, err := Open("exp", "vm", 0)
	if err != nil || again != tap {
		t.Fatalf("Open(exp, vm, 0) of a stopped capture = %p, %v; want the finished Tap %p, nil", again, err, tap)
	}

	idleGrace = 50 * time.Millisecond

	again.Release()

	// expiry drops the Tap from the registry before removing its spool, so wait for both
	polltest.Until(t, "the unused Tap expires and its spool is removed", func() bool {
		registryMu.Lock()
		_, held := registry["exp/vm/0"]
		registryMu.Unlock()

		_, err := os.Stat(spool)

		return !held && os.IsNotExist(err)
	})

	if _, err := Open("exp", "vm", 0); !errors.Is(err, ErrNotCapturing) {
		t.Errorf("Open(exp, vm, 0) after the Tap expires error = %v, want %v", err, ErrNotCapturing)
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
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
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
		t.Fatal("Tap made no mesh read within 5s, want it to keep reading its capture")
	}

	closed := make(chan struct{})

	go func() {
		CloseAll()
		close(closed)
	}()

	// the spool goes at once, without waiting for that read
	polltest.Until(t, "the spool is removed while a read is in flight", func() bool {
		_, err := os.Stat(tap.spool)

		return os.IsNotExist(err)
	})

	release()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll() still running 5s after the mesh read returned")
	}

	// a reader still holding the Tap gets what was read, then its stream ends
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Stream(fromNow false) after CloseAll() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream(fromNow false) still running 5s after CloseAll()")
	}

	// what was read before closing
	if diff := cmp.Diff(append(append([]byte{}, header...), one...), s.out.Bytes()); diff != "" {
		t.Errorf("Stream(fromNow false) bytes mismatch (-want +got):\n%s", diff)
	}

	// the capture is still running: the next reader follows it afresh
	again, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) after CloseAll() error = %v", err)
	}

	defer again.Release()

	if again == tap {
		t.Errorf("Open(exp, vm, 0) after CloseAll() = %p, the closed Tap; want a new one", again)
	}
}

// RemoveStaleSpools deletes the capture spools last written before the time
// given, leaving newer spools and other temporary files.
func TestRemoveStaleSpools(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	started := time.Now()

	for name, age := range map[string]time.Duration{
		"phenix-capture-1.pcap": time.Hour,
		"phenix-capture-2.pcap": -time.Minute,
		"other-1.pcap":          time.Hour,
	} {
		path := filepath.Join(tmp, name)
		if err := os.WriteFile(path, []byte("spooled"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}

		at := started.Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("setting the times of %s: %v", name, err)
		}
	}

	removed, err := RemoveStaleSpools(started)
	if err != nil || removed != 1 {
		t.Errorf("RemoveStaleSpools(start time) = %d, %v; want 1, nil", removed, err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("os.ReadDir(TMPDIR) error = %v", err)
	}

	left := make([]string, 0, len(entries))
	for _, e := range entries {
		left = append(left, e.Name())
	}

	if diff := cmp.Diff([]string{"other-1.pcap", "phenix-capture-2.pcap"}, left); diff != "" {
		t.Errorf("files left after RemoveStaleSpools(start time) mismatch (-want +got):\n%s", diff)
	}
}

// tapFakeSource serves data as a capture file. Each read returns the next
// of errs, while there are any, and calls onRead, if set, with its number.
type tapFakeSource struct {
	mu     sync.Mutex
	data   []byte
	errs   []error
	reads  int
	onRead func(reads int)
	closed bool
}

func (s *tapFakeSource) Read(off int64, maxLen int) ([]byte, error) {
	s.mu.Lock()
	s.reads++

	var (
		reads, onRead = s.reads, s.onRead
		err           error
		data          = s.data[min(int(off), len(s.data)):min(int(off)+maxLen, len(s.data))]
	)

	if len(s.errs) > 0 {
		err, s.errs = s.errs[0], s.errs[1:]
	}
	s.mu.Unlock()

	if onRead != nil {
		onRead(reads)
	}

	if err != nil {
		return nil, err
	}

	return data, nil
}

func (s *tapFakeSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

// state returns how many reads the source answered and whether it is closed.
func (s *tapFakeSource) state() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reads, s.closed
}

// tapNew returns a Tap of src that has not started following, whose store is
// a file holding content, opened read-only when readOnly is set.
func tapNew(t *testing.T, src source, local bool, content []byte, readOnly bool) *Tap {
	t.Helper()

	path := filepath.Join(t.TempDir(), "store.pcap")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing the store: %v", err)
	}

	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}

	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}

	t.Cleanup(func() { _ = f.Close() })

	return &Tap{
		capture: Capture{Exp: "exp", VM: "vm", Interface: 0, Path: "/phenix/images/exp/files/vm-0.pcap", Host: "compute1"},
		src:     src,
		local:   local,
		store:   f,
		stop:    make(chan struct{}),
		exited:  make(chan struct{}),
		changed: make(chan struct{}),
	}
}

// tapRecords returns a pcap file header followed by n records of size bytes.
func tapRecords(n, size int) []byte {
	b := pcapFile(binary.LittleEndian, 0xa1b2c3d4)

	for range n {
		b = append(b, record(binary.LittleEndian, size)...)
	}

	return b
}

func TestTapPoll(t *testing.T) {
	t.Parallel()

	var (
		errRead = errors.New("node unreachable")
		two     = tapRecords(2, 100)
		corrupt = func() []byte {
			b := tapRecords(1, 10)
			binary.LittleEndian.PutUint32(b[fileHeaderLen+8:], maxRecordLen+1)

			return b
		}()
		large = tapRecords(800, 1500) // more than one remote chunk

		le      = binary.LittleEndian
		shb     = ngSHB(le, ngOpt(4, "minimega 3.4.0"))
		ngWhole = ngFile(shb, ngIDB(le), ngEPB(le, 0, 60))
		ngLarge = ngFile(shb, ngEPB(le, 0, remoteChunk+remoteChunk/2))
		badEnd  = ngEPB(le, 0, 60)
	)

	le.PutUint32(badEnd[len(badEnd)-4:], 8)

	tests := []struct {
		name     string
		data     []byte
		errs     []error
		local    bool
		readOnly bool
		size     int64 // already published, with the header parsed
		ng       bool  // the header parsed is a pcapng one
		wantMore bool
		wantSize int64
		wantErr  error
		wantText string // in the error
	}{
		{name: "source fails", data: two, errs: []error{errRead}, wantErr: errRead},
		{name: "partial header", data: two[:fileHeaderLen-1], wantSize: 0},
		{name: "not pcap", data: bytes.Repeat([]byte{0x0a}, 64), wantErr: errNotPcap, wantText: "vm-0.pcap"},
		{name: "corrupt record", data: corrupt, wantErr: errCorrupt, wantText: "at byte 24"},
		{name: "header and part of a record", data: two[:fileHeaderLen+50], wantSize: fileHeaderLen},
		{name: "whole records from a node", data: two, wantSize: int64(len(two))},
		{name: "whole records on the headnode", data: two, local: true, wantSize: int64(len(two))},
		{name: "nothing new", data: two, size: int64(len(two)), wantSize: int64(len(two))},
		{
			name:     "more than a chunk",
			data:     large,
			wantMore: true,
			wantSize: fileHeaderLen + (remoteChunk-fileHeaderLen)/(recordHeaderLen+1500)*(recordHeaderLen+1500),
		},
		{name: "spool not writable", data: two, readOnly: true, wantText: "spooling capture"},
		{name: "pcapng section header partly written", data: shb[:fileHeaderLen+2], wantSize: 0},
		{name: "pcapng whole blocks", data: append(bytes.Clone(ngWhole), shb[:20]...), wantSize: int64(len(ngWhole))},
		{name: "pcapng whole blocks on the headnode", data: ngWhole, local: true, wantSize: int64(len(ngWhole))},
		{
			name:     "pcapng corrupt block",
			data:     ngFile(shb, badEnd),
			wantErr:  errCorrupt,
			wantText: "at byte " + strconv.Itoa(len(shb)),
		},
		{name: "pcapng section header without a byte-order magic", data: pcapFile(le, blockSHB), wantErr: errNotPcap},
		{name: "pcapng block after a whole one", data: ngLarge, wantMore: true, wantSize: int64(len(shb))},
		{
			name:     "pcapng block larger than a chunk",
			data:     ngLarge,
			size:     int64(len(shb)),
			ng:       true,
			wantMore: true,
			wantSize: int64(len(ngLarge)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			src := &tapFakeSource{data: test.data, errs: test.errs}
			tap := tapNew(t, src, test.local, nil, test.readOnly)
			tap.size, tap.format = test.size, format{ng: test.ng, order: binary.LittleEndian}
			before := tap.State()

			more, err := tap.poll()

			wantFail := test.wantErr != nil || test.wantText != ""
			if (err != nil) != wantFail || (test.wantErr != nil && !errors.Is(err, test.wantErr)) ||
				!strings.Contains(fmt.Sprint(err), test.wantText) {
				t.Errorf("poll() error = %v, want %v containing %q", err, test.wantErr, test.wantText)
			}

			if err != nil {
				return
			}

			after := tap.State()
			if more != test.wantMore || after.Size != test.wantSize {
				t.Errorf("poll() = %t with %d bytes published, want %t with %d", more, after.Size, test.wantMore, test.wantSize)
			}

			select {
			case <-before.Changed:
				if after.Size == before.Size || after.Modified.IsZero() {
					t.Errorf("poll() notified readers of %d bytes, modified %v, without publishing any", after.Size, after.Modified)
				}
			default:
				if after.Size != before.Size {
					t.Errorf("poll() published %d bytes without notifying readers", after.Size-before.Size)
				}
			}

			// a spool holds what was published; on the headnode the store is the capture file
			if !test.local && after.Size > test.size {
				spooled := make([]byte, after.Size-test.size)
				if _, err := tap.store.ReadAt(spooled, test.size); err != nil {
					t.Fatalf("reading the spool error = %v", err)
				}

				if diff := cmp.Diff(test.data[test.size:after.Size], spooled); diff != "" {
					t.Errorf("spool after poll() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestTapReadAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		off     int64
		n       int
		want    string
		wantErr error
	}{
		{name: "within the size", off: 2, n: 2, want: "23"},
		{name: "up to the size", off: 2, n: 4, want: "2345"},
		{name: "past the size", off: 4, n: 4, want: "45", wantErr: io.EOF},
		{name: "at the size", off: 6, n: 4, wantErr: io.EOF},
		{name: "written but not published", off: 8, n: 2, wantErr: io.EOF},
		{name: "nothing asked", off: 0, n: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tap := tapNew(t, &tapFakeSource{}, true, []byte("0123456789"), true)
			tap.size = 6

			p := make([]byte, test.n)

			n, err := tap.ReadAt(p, test.off)
			if string(p[:n]) != test.want || !errors.Is(err, test.wantErr) {
				t.Errorf("ReadAt(%d bytes, %d) = %q, %v; want %q, %v", test.n, test.off, p[:n], err, test.want, test.wantErr)
			}
		})
	}

	tap := tapNew(t, &tapFakeSource{}, true, []byte("0123456789"), true)
	tap.size = 6
	_ = tap.store.Close()

	if n, err := tap.ReadAt(make([]byte, 2), 0); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Errorf("ReadAt() of a closed store = %d, %v; want 0, %v", n, err, os.ErrClosed)
	}
}

func TestTapStreamEnds(t *testing.T) {
	t.Parallel()

	var (
		errSend = errors.New("client went away")
		errRead = errors.New("node unreachable")
		header  = pcapFile(binary.LittleEndian, 0xa1b2c3d4)
		whole   = tapRecords(2, 100)
		corrupt = append(slices.Clone(header), bytes.Repeat([]byte{0xff}, recordHeaderLen)...)
	)

	type sendFunc func(call int, tap *Tap, cancel context.CancelFunc) error

	tests := []struct {
		name      string
		content   []byte
		done      bool
		err       error
		fromNow   bool
		closed    bool // the store is closed before the stream starts
		cancelled bool // the context ends before the stream starts
		send      sendFunc
		want      []byte
		wantErr   error
		wantText  string
	}{
		{name: "nothing captured", done: true, want: nil},
		{name: "whole capture", content: whole, done: true, want: whole},
		{name: "following failed", content: whole, done: true, err: errRead, want: whole, wantErr: errRead},
		{name: "from now on a finished capture", content: whole, done: true, fromNow: true, want: header},
		{
			name:    "header not sent",
			content: whole,
			done:    true,
			send:    func(int, *Tap, context.CancelFunc) error { return errSend },
			want:    header,
			wantErr: errSend,
		},
		{
			name:    "records not sent",
			content: whole,
			done:    true,
			send: func(call int, _ *Tap, _ context.CancelFunc) error {
				if call == 2 {
					return errSend
				}

				return nil
			},
			want:    whole,
			wantErr: errSend,
		},
		{name: "header unreadable", content: whole, done: true, closed: true, wantErr: os.ErrClosed, wantText: "reading capture header"},
		{
			name:    "records unreadable",
			content: whole,
			done:    true,
			send: func(_ int, tap *Tap, _ context.CancelFunc) error {
				return tap.store.Close()
			},
			want:     header,
			wantErr:  os.ErrClosed,
			wantText: "reading capture:",
		},
		{name: "corrupt record", content: corrupt, done: true, want: header, wantErr: errCorrupt, wantText: "at byte 24"},
		{name: "context ends while waiting", content: header, cancelled: true, want: header, wantErr: context.Canceled},
		{
			name:    "context ends after a send",
			content: whole,
			send: func(call int, _ *Tap, cancel context.CancelFunc) error {
				if call == 2 {
					cancel()
				}

				return nil
			},
			want:    whole,
			wantErr: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tap := tapNew(t, &tapFakeSource{}, true, test.content, true)
			tap.format = format{ng: false, order: binary.LittleEndian}
			tap.size, tap.caughtUp, tap.done, tap.err = int64(len(test.content)), true, test.done, test.err

			if test.closed {
				_ = tap.store.Close()
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if test.cancelled {
				cancel()
			}

			var (
				got   []byte
				calls int
			)

			err := tap.Stream(ctx, test.fromNow, func(b []byte) error {
				calls++
				got = append(got, b...)

				if test.send == nil {
					return nil
				}

				return test.send(calls, tap, cancel)
			})

			if !errors.Is(err, test.wantErr) || (test.wantText != "" && !strings.Contains(fmt.Sprint(err), test.wantText)) {
				t.Errorf("Stream() error = %v, want %v containing %q", err, test.wantErr, test.wantText)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Stream() bytes sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTapCapture(t *testing.T) {
	t.Parallel()

	tap := tapNew(t, &tapFakeSource{}, false, nil, false)

	want := Capture{Exp: "exp", VM: "vm", Interface: 0, Path: "/phenix/images/exp/files/vm-0.pcap", Host: "compute1"}
	if diff := cmp.Diff(want, tap.Capture()); diff != "" {
		t.Errorf("Capture() mismatch (-want +got):\n%s", diff)
	}

	if got := tap.Capture().key(); got != "exp/vm/0" {
		t.Errorf("Capture().key() = %q, want %q", got, "exp/vm/0")
	}
}

// discard closes what prepare opened and removes a spool.
func TestTapDiscard(t *testing.T) {
	t.Parallel()

	for _, remote := range []bool{false, true} {
		t.Run("remote "+strconv.FormatBool(remote), func(t *testing.T) {
			t.Parallel()

			src := &tapFakeSource{}
			tap := tapNew(t, src, !remote, []byte("spooled"), false)

			if remote {
				tap.spool = tap.store.Name()
			}

			tap.discard()

			if _, closed := src.state(); !closed {
				t.Error("source after discard() is open, want closed")
			}

			if _, err := tap.store.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
				t.Errorf("reading the store after discard() error = %v, want %v", err, os.ErrClosed)
			}

			if _, err := os.Stat(tap.store.Name()); remote != errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Stat(store) after discard() error = %v, want it removed only when it is a spool", err)
			}
		})
	}
}

// A Tap taken again before its idle timer fires is kept.
func TestTapExpireWhileHeld(t *testing.T) {
	t.Parallel()

	tap := tapNew(t, &tapFakeSource{}, false, []byte("spooled"), false)
	tap.spool = tap.store.Name()
	tap.refs = 1

	tap.expire()

	if _, err := os.Stat(tap.spool); err != nil {
		t.Errorf("os.Stat(spool) after expire() of a held Tap error = %v, want it kept", err)
	}

	if _, err := tap.store.ReadAt(make([]byte, 1), 0); err != nil {
		t.Errorf("reading the store after expire() of a held Tap error = %v, want it open", err)
	}

	select {
	case <-tap.stop:
		t.Error("expire() of a held Tap stopped it, want it following")
	default:
	}
}

// A Tap that expires after another took its place in the registry leaves
// the newer one there.
func TestTapExpireReplaced(t *testing.T) { //nolint:paralleltest // uses the registry
	old := tapNew(t, &tapFakeSource{}, false, nil, false)
	old.spool = old.store.Name()
	close(old.exited)

	current := tapNew(t, &tapFakeSource{}, false, nil, false)

	registryMu.Lock()
	registry["exp/vm/0"] = current
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		delete(registry, "exp/vm/0")
		registryMu.Unlock()
	})

	old.expire()

	registryMu.Lock()
	got := registry["exp/vm/0"]
	registryMu.Unlock()

	if got != current {
		t.Errorf("registry after expire() of a replaced Tap = %p, want the current Tap %p", got, current)
	}

	if _, err := os.Stat(old.spool); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("os.Stat(spool of the expired Tap) error = %v, want %v", err, fs.ErrNotExist)
	}
}

// tapUseTimings polls without delay and never asks minimega whether the
// capture is active, until the test ends.
func tapUseTimings(t *testing.T) {
	t.Helper()

	saved := [3]time.Duration{localPoll, remotePoll, activeCheck}
	localPoll, remotePoll, activeCheck = time.Millisecond, time.Millisecond, time.Hour

	t.Cleanup(func() { localPoll, remotePoll, activeCheck = saved[0], saved[1], saved[2] })
}

// tapAwaitExit waits for the Tap to stop following.
func tapAwaitExit(t *testing.T, tap *Tap) {
	t.Helper()

	select {
	case <-tap.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("Tap still following 5s later, want it stopped")
	}
}

func TestTapFollowGivesUpAfterReadErrors(t *testing.T) { //nolint:paralleltest // shortens the package's timings
	tapUseTimings(t)

	errRead := errors.New("node unreachable")
	src := &tapFakeSource{data: tapRecords(1, 10), errs: slices.Repeat([]error{errRead}, maxReadErrors+1)}
	tap := tapNew(t, src, false, nil, false)

	go tap.follow()

	t.Cleanup(tap.halt)
	tapAwaitExit(t, tap)

	if st := tap.State(); !st.Done || !errors.Is(st.Err, errRead) {
		t.Errorf("State() after %d failed reads = done %t, %v; want done, %v", maxReadErrors, st.Done, st.Err, errRead)
	}

	if reads, closed := src.state(); reads != maxReadErrors || !closed {
		t.Errorf("source after giving up = %d reads, closed %t; want %d, closed", reads, closed, maxReadErrors)
	}
}

// Fewer failed reads in a row than maxReadErrors do not stop a Tap.
func TestTapFollowRecoversFromReadErrors(t *testing.T) { //nolint:paralleltest // shortens the package's timings
	tapUseTimings(t)

	var (
		errRead = errors.New("node unreachable")
		data    = tapRecords(1, 10)
		errs    = []error{errRead, errRead, nil, errRead, errRead}
		src     = &tapFakeSource{data: data, errs: errs}
		tap     = tapNew(t, src, false, nil, false)
	)

	go tap.follow()

	t.Cleanup(tap.halt)

	polltest.Until(t, "the record is read after the failed reads", func() bool {
		reads, _ := src.state()

		return tap.State().Size == int64(len(data)) && reads > len(errs)
	})

	tap.halt()

	if st := tap.State(); st.Done || st.Err != nil {
		t.Errorf("State() after recovered reads = done %t, %v; want following, nil", st.Done, st.Err)
	}

	if _, closed := src.state(); !closed {
		t.Error("source after halt() is open, want closed")
	}
}

// A Tap reads chunk after chunk without waiting, and one told to stop while
// there is more to read stops without reading it.
func TestTapFollowStopsWithMoreToRead(t *testing.T) { //nolint:paralleltest // shortens the package's timings
	tapUseTimings(t)

	src := &tapFakeSource{data: tapRecords(2000, 1500)}
	tap := tapNew(t, src, false, nil, false)

	src.onRead = func(reads int) {
		if reads == 2 {
			tap.stopOnce.Do(func() { close(tap.stop) })
		}
	}

	go tap.follow()

	t.Cleanup(tap.halt)
	tapAwaitExit(t, tap)

	if reads, _ := src.state(); reads != 2 {
		t.Errorf("source reads after a stop during the second = %d, want 2", reads)
	}

	if st := tap.State(); st.Done || st.Size <= remoteChunk || st.CaughtUp {
		t.Errorf("State() after a stop with more to read = %+v, want two chunks, not done or caught up", st)
	}
}

// tapRacingCluster runs a second Open, once, from inside the first one's
// lookup of the VM's captures or of its host's node, and can fail host lookups.
type tapRacingCluster struct {
	*fakeCluster

	hookCaptures, hookHeadnode bool
	hostErr                    error

	opened   atomic.Bool
	inner    *Tap
	innerErr error
}

func (c *tapRacingCluster) openInner() {
	if c.opened.CompareAndSwap(false, true) {
		c.inner, c.innerErr = Open("exp", "vm", 0)
	}
}

func (c *tapRacingCluster) GetVMCaptures(opts ...mm.Option) []mm.Capture {
	if c.hookCaptures {
		c.openInner()
	}

	return c.fakeCluster.GetVMCaptures(opts...)
}

func (c *tapRacingCluster) IsHeadnode(host string) bool {
	if c.hookHeadnode {
		c.openInner()
	}

	return c.fakeCluster.IsHeadnode(host)
}

func (c *tapRacingCluster) GetVMHost(opts ...mm.Option) (string, error) {
	if c.hostErr != nil {
		return "", c.hostErr
	}

	return c.fakeCluster.GetVMHost(opts...)
}

// tapSpools lists the capture spools in dir.
func tapSpools(t *testing.T, dir string) []string {
	t.Helper()

	spools, err := filepath.Glob(filepath.Join(dir, spoolPattern))
	if err != nil {
		t.Fatalf("listing spools: %v", err)
	}

	return spools
}

// An Open racing another for the same capture shares its Tap, and drops
// the one it prepared meanwhile.
func TestOpenRacingOpen(t *testing.T) { //nolint:paralleltest // replaces package state
	for _, during := range []string{"find", "prepare"} {
		t.Run("during "+during, func(t *testing.T) {
			cluster := &tapRacingCluster{fakeCluster: useFakeCluster(t, true)}
			cluster.hookCaptures, cluster.hookHeadnode = during == "find", during == "prepare"
			mm.DefaultMM = cluster //nolint:reassign // useFakeCluster restores it

			// after useFakeCluster, which may start the fake minimega under TMPDIR
			spoolDir := t.TempDir()
			t.Setenv("TMPDIR", spoolDir)

			tap, err := Open("exp", "vm", 0)
			if err != nil {
				t.Fatalf("Open(exp, vm, 0) error = %v", err)
			}

			defer tap.Release()

			if cluster.innerErr != nil {
				t.Fatalf("racing Open(exp, vm, 0) error = %v", cluster.innerErr)
			}

			defer cluster.inner.Release()

			if tap != cluster.inner {
				t.Errorf("Open(exp, vm, 0) = %p, want the racing Open's Tap %p", tap, cluster.inner)
			}

			polltest.Until(t, "only the shared Tap's spool is left", func() bool {
				spools := tapSpools(t, spoolDir)

				return len(spools) == 1 && spools[0] == tap.spool
			})
		})
	}
}

func TestOpenErrors(t *testing.T) { //nolint:paralleltest // replaces package state
	errHost := errors.New("no such VM")

	tests := []struct {
		name     string
		remote   bool
		setup    func(t *testing.T, c *tapRacingCluster)
		wantErr  error
		wantText string
	}{
		{
			name:     "host lookup fails",
			remote:   true,
			setup:    func(_ *testing.T, c *tapRacingCluster) { c.hostErr = errHost },
			wantErr:  errHost,
			wantText: "finding the host of VM vm",
		},
		{
			name:    "unsafe host",
			remote:  true,
			setup:   func(_ *testing.T, c *tapRacingCluster) { c.host = "compute1 shell reboot" },
			wantErr: ErrUnsafePath,
		},
		{
			name: "capture file missing on the headnode",
			setup: func(t *testing.T, c *tapRacingCluster) {
				t.Helper()

				if err := os.Remove(c.path(0)); err != nil {
					t.Fatalf("removing the capture file: %v", err)
				}
			},
			wantErr:  fs.ErrNotExist,
			wantText: "opening store for capture",
		},
		{
			name:   "no directory for the spool",
			remote: true,
			setup: func(t *testing.T, _ *tapRacingCluster) {
				t.Helper()
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			},
			wantErr:  fs.ErrNotExist,
			wantText: "opening store for capture",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := &tapRacingCluster{fakeCluster: useFakeCluster(t, test.remote)}
			mm.DefaultMM = cluster //nolint:reassign // useFakeCluster restores it

			test.setup(t, cluster)

			tap, err := Open("exp", "vm", 0)
			if tap != nil || !errors.Is(err, test.wantErr) || !strings.Contains(fmt.Sprint(err), test.wantText) {
				t.Errorf("Open(exp, vm, 0) = %p, %v; want nil, %v containing %q", tap, err, test.wantErr, test.wantText)
			}

			registryMu.Lock()
			held := registry["exp/vm/0"]
			registryMu.Unlock()

			if held != nil {
				t.Errorf("registry after a failed Open() holds %p, want nothing", held)
			}
		})
	}
}

// Readers streaming one Tap at once each get the whole capture.
func TestTapConcurrentStreams(t *testing.T) { //nolint:paralleltest // replaces package state
	cluster := useFakeCluster(t, false)
	header := pcapFile(binary.LittleEndian, 0xa1b2c3d4)
	cluster.write(t, header)

	tap, err := Open("exp", "vm", 0)
	if err != nil {
		t.Fatalf("Open(exp, vm, 0) error = %v", err)
	}

	defer tap.Release()

	streams := make([]*stream, 4)
	for i := range streams {
		streams[i] = collect(tap, false)
	}

	want := slices.Clone(header)

	for i := range 5 {
		r := record(binary.LittleEndian, 100*(i+1))
		cluster.write(t, r)
		want = append(want, r...)

		waitFor(t, tap, "the record", func(st State) bool { return st.Size == int64(len(want)) })
	}

	cluster.capturing.Store(false)

	for i, s := range streams {
		select {
		case err := <-s.done:
			if err != nil {
				t.Errorf("stream %d error = %v, want nil", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("stream %d still running 5s after the capture stopped", i)
		}

		if diff := cmp.Diff(want, s.out.Bytes()); diff != "" {
			t.Errorf("stream %d bytes mismatch (-want +got):\n%s", i, diff)
		}
	}
}

// Only regular files are removed: a directory or link named like a spool
// stays, as does what the link points to.
func TestRemoveStaleSpoolsSkipsOtherFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("writing the link target: %v", err)
	}

	if err := os.Mkdir(filepath.Join(tmp, "phenix-capture-dir.pcap"), 0o700); err != nil {
		t.Fatalf("creating a directory: %v", err)
	}

	if err := os.Symlink(target, filepath.Join(tmp, "phenix-capture-link.pcap")); err != nil {
		t.Fatalf("creating a link: %v", err)
	}

	if removed, err := RemoveStaleSpools(time.Now().Add(time.Hour)); removed != 0 || err != nil {
		t.Errorf("RemoveStaleSpools(an hour from now) = %d, %v; want 0, nil", removed, err)
	}

	for _, path := range []string{target, filepath.Join(tmp, "phenix-capture-dir.pcap"), filepath.Join(tmp, "phenix-capture-link.pcap")} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("os.Lstat(%s) after RemoveStaleSpools() error = %v, want it kept", path, err)
		}
	}
}

func TestRemoveStaleSpoolsErrors(t *testing.T) {
	t.Run("temporary directory not a pattern", func(t *testing.T) {
		tmp := filepath.Join(t.TempDir(), "a[b")
		if err := os.Mkdir(tmp, 0o700); err != nil {
			t.Fatalf("creating %s: %v", tmp, err)
		}

		t.Setenv("TMPDIR", tmp)

		removed, err := RemoveStaleSpools(time.Now())
		if removed != 0 || !errors.Is(err, filepath.ErrBadPattern) || !strings.Contains(err.Error(), "listing capture spools") {
			t.Errorf("RemoveStaleSpools() under %s = %d, %v; want 0, %v", tmp, removed, err, filepath.ErrBadPattern)
		}
	})

	t.Run("spool in a read-only directory", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)

		spool := filepath.Join(tmp, "phenix-capture-1.pcap")
		if err := os.WriteFile(spool, nil, 0o600); err != nil {
			t.Fatalf("writing the spool: %v", err)
		}

		if err := os.Chmod(tmp, 0o500); err != nil {
			t.Fatalf("making %s read-only: %v", tmp, err)
		}

		t.Cleanup(func() { _ = os.Chmod(tmp, 0o700) })

		removed, err := RemoveStaleSpools(time.Now().Add(time.Hour))

		// root ignores the directory's permissions
		if os.Geteuid() == 0 {
			if removed != 1 || err != nil {
				t.Errorf("RemoveStaleSpools() as root = %d, %v; want 1, nil", removed, err)
			}

			return
		}

		if removed != 0 || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("RemoveStaleSpools() of a spool it cannot remove = %d, %v; want 0, %v", removed, err, fs.ErrPermission)
		}
	})
}

// ngWholeSends returns an error unless each send of out ends at a block
// boundary, and each send larger than streamChunk is one block.
func ngWholeSends(out []byte, sends []int) error {
	var end int

	for _, n := range sends {
		if _, err := readNg(out[:end+n]); err != nil {
			return fmt.Errorf("send of bytes %d to %d: %w", end, end+n, err)
		}

		if n > streamChunk {
			if lead := int(binary.LittleEndian.Uint32(out[end+4:])); lead != n && int(binary.BigEndian.Uint32(out[end+4:])) != n {
				return fmt.Errorf("send of bytes %d to %d is larger than %d and more than one block", end, end+n, streamChunk)
			}
		}

		end += n
	}

	return nil
}

func TestTapFollowsPcapng(t *testing.T) { //nolint:paralleltest // replaces package state
	for _, remote := range []bool{false, true} {
		name := "headnode"
		if remote {
			name = "remote node"
		}

		t.Run(name, func(t *testing.T) {
			cluster := useFakeCluster(t, remote)

			var (
				le    = binary.LittleEndian
				shb   = ngSHB(le, ngOpt(4, "minimega 3.4.0"))
				idb0  = ngIDB(le, ngOpt(2, "compute1/mega_bridge_exp"))
				one   = ngEPB(le, 0, 60)
				two   = ngEPB(le, 0, 1500, ngOpt(1, "late"))
				idb1  = ngIDB(le, ngOpt(2, "compute2/mega_bridge_exp"))
				three = ngEPB(le, 1, 100)
				isb   = ngISB(le, 1)
			)

			// minimega has written the section header, an interface, one packet and part of the next
			cluster.write(t, ngFile(shb, idb0, one, two[:700]))

			tap, err := Open("exp", "vm", 0)
			if err != nil {
				t.Fatalf("Open(exp, vm, 0) error = %v", err)
			}

			defer tap.Release()

			waitFor(t, tap, "the first packet", func(st State) bool { return st.CaughtUp })

			if got, want := tap.State().Size, int64(len(shb)+len(idb0)+len(one)); got != want {
				t.Errorf("Tap.State().Size = %d, want %d", got, want)
			}

			s := collect(tap, false)

			// the rest of the packet, then an interface that joins later and its packet
			want := ngFile(shb, idb0, one, two, idb1, three, isb)

			cluster.write(t, ngFile(two[700:], idb1, three, isb))
			waitFor(t, tap, "the new interface's statistics", func(st State) bool { return st.Size == int64(len(want)) })

			cluster.capturing.Store(false)

			select {
			case err := <-s.done:
				if err != nil {
					t.Errorf("Stream(fromNow false) after the capture stops error = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Stream(fromNow false) still running 5s after the capture stopped")
			}

			if diff := cmp.Diff(want, s.out.Bytes()); diff != "" {
				t.Errorf("Stream(fromNow false) bytes mismatch (-want +got):\n%s", diff)
			}

			if err := ngWholeSends(s.out.Bytes(), s.sends); err != nil {
				t.Errorf("Stream(fromNow false) sends %v: %v", s.sends, err)
			}
		})
	}
}

func TestTapStreamFromNowPcapng(t *testing.T) { //nolint:paralleltest // replaces package state
	var (
		le, be = binary.LittleEndian, binary.BigEndian
		shb    = ngSHB(le, ngOpt(4, "minimega 3.4.0"))
		idb0   = ngIDB(le, ngOpt(2, "compute1/mega_bridge_exp"))
		idb1   = ngIDB(le, ngOpt(2, "compute1/mega_bridge_exp/mega_tap3"))
		idb2   = ngIDB(le, ngOpt(2, "compute2/mega_bridge_exp"))
		nrb    = ngNRB(le, "web01")
		shbBE  = ngSHB(be, ngOpt(3, "Linux"))
		idbBE  = ngIDB(be)
		nrbBE  = ngNRB(be, "db01", "db01.local")
	)

	tests := []struct {
		name   string
		before []byte // captured before the stream starts
		after  []byte
		want   []byte
	}{
		{
			name:   "one section",
			before: ngFile(shb, idb0, nrb, ngEPB(le, 0, 60), ngEPB(le, 0, 70), ngISB(le, 0), idb1, ngEPB(le, 1, 80)),
			after:  ngFile(ngEPB(le, 1, 90), idb2, ngEPB(le, 2, 100)),
			want:   ngFile(shb, idb0, nrb, idb1, ngEPB(le, 1, 90), idb2, ngEPB(le, 2, 100)),
		},
		{
			name:   "nothing before the stream but the section header",
			before: shb,
			after:  ngFile(idb0, ngEPB(le, 0, 60)),
			want:   ngFile(shb, idb0, ngEPB(le, 0, 60)),
		},
		{
			name:   "a second section in the other byte order",
			before: ngFile(shb, idb0, idb1, ngEPB(le, 1, 60), shbBE, idbBE, nrbBE, ngEPB(be, 0, 70)),
			after:  ngFile(ngEPB(be, 0, 80), ngISB(be, 0)),
			want:   ngFile(shbBE, idbBE, nrbBE, ngEPB(be, 0, 80), ngISB(be, 0)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := useFakeCluster(t, false)
			cluster.write(t, test.before)

			tap, err := Open("exp", "vm", 0)
			if err != nil {
				t.Fatalf("Open(exp, vm, 0) error = %v", err)
			}

			defer tap.Release()

			waitFor(t, tap, "the backlog", func(st State) bool { return st.CaughtUp })

			s := collect(tap, true)

			select {
			case <-s.started:
			case <-time.After(5 * time.Second):
				t.Fatal("Stream(fromNow true) sent no preamble within 5s")
			}

			cluster.write(t, test.after)
			waitFor(t, tap, "the new blocks", func(st State) bool {
				return st.Size == int64(len(test.before)+len(test.after))
			})

			cluster.capturing.Store(false)

			if err := <-s.done; err != nil {
				t.Errorf("Stream(fromNow true) after the capture stops error = %v, want nil", err)
			}

			if diff := cmp.Diff(test.want, s.out.Bytes()); diff != "" {
				t.Errorf("Stream(fromNow true) bytes mismatch (-want +got):\n%s", diff)
			}

			// each packet comes after the description of its interface
			if _, err := readNg(s.out.Bytes()); err != nil {
				t.Errorf("reading what Stream(fromNow true) sent error = %v", err)
			}

			if err := ngWholeSends(s.out.Bytes(), s.sends); err != nil {
				t.Errorf("Stream(fromNow true) sends %v: %v", s.sends, err)
			}
		})
	}
}

func TestTapPcapngPreamble(t *testing.T) {
	t.Parallel()

	var (
		le, be = binary.LittleEndian, binary.BigEndian
		shb    = ngSHB(le, ngOpt(4, "minimega 3.4.0"))
		idb    = ngIDB(le, ngOpt(2, "compute1/mega_bridge_exp"))
		nrb    = ngNRB(le, "web01")
		shbBE  = ngSHB(be)
		idbBE  = ngIDB(be)
		// larger than a remote chunk and than one send of a Stream
		bigIdb = ngIDB(le, slices.Repeat([]ngOption{ngOpt(1, strings.Repeat("x", 0xfffc))}, 24)...)
		bigEpb = ngEPB(le, 0, 2*streamChunk)
	)

	if len(bigIdb) <= max(remoteChunk, streamChunk) {
		t.Fatalf("len(large IDB) = %d, want more than %d", len(bigIdb), max(remoteChunk, streamChunk))
	}

	// spans returns the byte ranges of blocks laid out from off.
	spans := func(off int, blocks ...[]byte) []span {
		s := make([]span, 0, len(blocks))

		for _, b := range blocks {
			s = append(s, span{start: int64(off), end: int64(off + len(b))})
			off += len(b)
		}

		return s
	}

	tests := []struct {
		name         string
		data         []byte
		wantPreamble []span
		wantFromNow  []byte
	}{
		{
			name: "one section",
			data: ngFile(shb, idb, ngEPB(le, 0, 60), nrb, ngEPB(le, 0, 70), idb, ngISB(le, 1), ngEPB(le, 1, 80)),
			wantPreamble: slices.Concat(
				spans(0, shb, idb),
				spans(len(shb)+len(idb)+len(ngEPB(le, 0, 60)), nrb),
				spans(len(shb)+len(idb)+len(ngEPB(le, 0, 60))+len(nrb)+len(ngEPB(le, 0, 70)), idb),
			),
			wantFromNow: ngFile(shb, idb, nrb, idb),
		},
		{
			name:         "a second section",
			data:         ngFile(shb, idb, ngEPB(le, 0, 60), shbBE, idbBE, ngEPB(be, 0, 70)),
			wantPreamble: spans(len(shb)+len(idb)+len(ngEPB(le, 0, 60)), shbBE, idbBE),
			wantFromNow:  ngFile(shbBE, idbBE),
		},
		{
			name:         "blocks larger than a chunk",
			data:         ngFile(shb, bigIdb, bigEpb, ngEPB(le, 0, 60)),
			wantPreamble: spans(0, shb, bigIdb),
			wantFromNow:  ngFile(shb, bigIdb),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tap := tapNew(t, &tapFakeSource{data: test.data}, false, nil, false)

			if err := tap.drain(); err != nil {
				t.Fatalf("drain() error = %v", err)
			}

			tap.finish(nil)

			if got, want := tap.State().Size, int64(len(test.data)); got != want {
				t.Errorf("Tap.State().Size after drain() = %d, want %d", got, want)
			}

			_, preamble, _ := tap.cut()
			if diff := cmp.Diff(test.wantPreamble, preamble, cmp.AllowUnexported(span{})); diff != "" {
				t.Errorf("preamble after drain() mismatch (-want +got):\n%s", diff)
			}

			for _, fromNow := range []bool{false, true} {
				var (
					out   []byte
					sends []int
				)

				err := tap.Stream(t.Context(), fromNow, func(b []byte) error {
					out = append(out, b...)
					sends = append(sends, len(b))

					return nil
				})
				if err != nil {
					t.Errorf("Stream(fromNow %t) error = %v, want nil", fromNow, err)
				}

				want := test.data
				if fromNow {
					want = test.wantFromNow
				}

				// cmp.Diff is slow on megabytes
				if !bytes.Equal(want, out) {
					t.Errorf("Stream(fromNow %t) sent %d bytes that differ from the %d wanted", fromNow, len(out), len(want))
				}

				if err := ngWholeSends(out, sends); err != nil {
					t.Errorf("Stream(fromNow %t) sends %v: %v", fromNow, sends, err)
				}
			}
		})
	}
}

// A pcapng stream from now ends with the error of reading or sending its preamble, having sent no packet.
func TestTapPcapngPreambleFails(t *testing.T) {
	t.Parallel()

	var (
		le      = binary.LittleEndian
		shb     = ngSHB(le)
		idb     = ngIDB(le)
		data    = ngFile(shb, idb, ngEPB(le, 0, 60))
		errSend = errors.New("client went away")
	)

	tests := []struct {
		name     string
		closed   bool // the store is closed before the stream starts
		send     error
		want     []byte
		wantErr  error
		wantText string
	}{
		{name: "preamble not sent", send: errSend, want: ngFile(shb, idb), wantErr: errSend},
		{name: "preamble unreadable", closed: true, wantErr: os.ErrClosed, wantText: "reading capture preamble"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tap := tapNew(t, &tapFakeSource{data: data}, false, nil, false)

			if err := tap.drain(); err != nil {
				t.Fatalf("drain() error = %v", err)
			}

			tap.finish(nil)

			if test.closed {
				_ = tap.store.Close()
			}

			var got []byte

			err := tap.Stream(t.Context(), true, func(b []byte) error {
				got = append(got, b...)

				return test.send
			})

			if !errors.Is(err, test.wantErr) || (test.wantText != "" && !strings.Contains(fmt.Sprint(err), test.wantText)) {
				t.Errorf("Stream(fromNow true) error = %v, want %v containing %q", err, test.wantErr, test.wantText)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Stream(fromNow true) bytes sent mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
