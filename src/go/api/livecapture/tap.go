// Package livecapture follows pcap captures while minimega is still writing
// them, so their packets can be streamed as they arrive (to the web UI, the
// command line or a local Wireshark) instead of only once a capture stops.
//
// minimega writes each capture on the node running the VM. A Tap follows one
// capture: it reads the file as it grows (directly on the headnode, a byte
// range at a time over the mesh elsewhere) and only ever exposes whole
// records, so every reader sees a valid pcap file however far it has grown.
// Any number of readers share a Tap, which stops following a while after the
// last one lets go.
package livecapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
)

//nolint:gochecknoglobals // tests shorten them
var (
	localPoll  = 250 * time.Millisecond
	remotePoll = time.Second

	// how often a Tap asks minimega whether its capture is still running.
	activeCheck = 5 * time.Second

	// how long a Tap keeps following, and keeps what it read, after its last
	// reader lets go, so a reader that comes back soon (WebShark reloading a
	// capture, say) finds it ready.
	idleGrace = 2 * time.Minute
)

const (
	localChunk  = 4 << 20
	remoteChunk = 1 << 20

	// consecutive failed reads before a Tap gives up on its capture.
	maxReadErrors = 3

	// streamChunk bounds one send of a Stream; it holds whole records, which
	// are never larger than maxRecordLen.
	streamChunk = 1 << 20
)

var (
	// ErrNotCapturing is returned for a VM interface minimega is not
	// capturing.
	ErrNotCapturing = errors.New("not being captured")

	// errTruncated is returned when a capture file shrinks, which minimega
	// never does to a capture it is still writing.
	errTruncated = errors.New("capture file truncated")
)

// Capture is a running pcap capture of one VM interface.
type Capture struct {
	Exp       string
	VM        string
	Interface int

	// Path is where minimega writes the capture, on Host.
	Path string
	Host string
}

func (c Capture) key() string {
	return c.Exp + "/" + c.VM + "/" + strconv.Itoa(c.Interface)
}

// find returns the running capture of the VM's interface. A stopped
// experiment has no captures; asking minimega about one would recreate its
// namespace, so it is not asked.
func find(exp, vm string, iface int) (Capture, error) {
	missing := fmt.Errorf("%w: interface %d of VM %s in experiment %s", ErrNotCapturing, iface, vm, exp)

	if !experiment.Running(exp) {
		return Capture{}, missing
	}

	for _, c := range mm.GetVMCaptures(mm.NS(exp), mm.VMName(vm)) {
		if c.Interface != iface {
			continue
		}

		host, err := mm.GetVMHost(mm.NS(exp), mm.VMName(vm))
		if err != nil {
			return Capture{}, fmt.Errorf("finding the host of VM %s: %w", vm, err)
		}

		return Capture{Exp: exp, VM: vm, Interface: iface, Path: c.Filepath, Host: host}, nil
	}

	return Capture{}, missing
}

// active reports whether minimega is still writing the capture.
func active(c Capture) bool {
	found, err := find(c.Exp, c.VM, c.Interface)

	return err == nil && found.Path == c.Path
}

// newSource returns what reads the capture's file, and whether that is the
// file itself, on the headnode.
func newSource(c Capture) (source, bool, error) { //nolint:ireturn // the file itself or a mesh node, by host
	if mm.IsHeadnode(c.Host) {
		return &fileSource{path: c.Path, f: nil, buf: nil}, true, nil
	}

	src, err := newMeshSource(c.Host, c.Path)

	return src, false, err
}

// the captures this process follows, by Capture.key
//
//nolint:gochecknoglobals // one registry per process
var (
	registryMu sync.Mutex
	registry   = make(map[string]*Tap)
)

// Tap follows one capture. It is safe for concurrent use.
type Tap struct {
	capture Capture
	src     source
	local   bool

	// store holds the capture's whole records: the capture file itself on
	// the headnode, or a spool of what was read so far from another node
	store *os.File
	spool string

	stop     chan struct{} // closed to stop following
	stopOnce sync.Once
	exited   chan struct{} // closed once following has stopped

	mu       sync.Mutex
	header   pcapHeader
	size     int64 // bytes of store readers may read: the header and whole records
	modified time.Time
	caughtUp bool // has read everything written before it started
	done     bool
	err      error
	changed  chan struct{} // closed, and replaced, whenever the above change
	refs     int
	idle     *time.Timer
}

// Open returns a Tap following the running capture of the VM's interface,
// starting one if none is. A Tap whose capture has since stopped is returned
// as it is, holding the whole capture, until it expires. Call Release when
// done with it.
func Open(exp, vm string, iface int) (*Tap, error) {
	key := Capture{Exp: exp, VM: vm, Interface: iface, Path: "", Host: ""}.key()

	registryMu.Lock()

	existing := registry[key]
	if existing != nil && !existing.State().Done {
		existing.acquire()
		registryMu.Unlock()

		return existing, nil
	}

	registryMu.Unlock()

	c, err := find(exp, vm, iface)

	registryMu.Lock()
	defer registryMu.Unlock()

	current := registry[key]

	if err != nil {
		if errors.Is(err, ErrNotCapturing) && current != nil {
			current.acquire()

			return current, nil
		}

		return nil, err
	}

	// another Open started one meanwhile
	if current != nil && !current.State().Done {
		current.acquire()

		return current, nil
	}

	t, err := start(c)
	if err != nil {
		return nil, err
	}

	registry[key] = t

	t.acquire()

	return t, nil
}

func start(c Capture) (*Tap, error) {
	src, local, err := newSource(c)
	if err != nil {
		return nil, err
	}

	t := &Tap{ //nolint:exhaustruct // the rest is zero until it is read
		capture: c,
		src:     src,
		local:   local,
		stop:    make(chan struct{}),
		exited:  make(chan struct{}),
		changed: make(chan struct{}),
	}

	if local {
		t.store, err = os.Open(c.Path)
	} else {
		t.store, err = os.CreateTemp("", "phenix-capture-*.pcap")
		if t.store != nil {
			t.spool = t.store.Name()
		}
	}

	if err != nil {
		_ = src.Close()

		return nil, fmt.Errorf("opening store for capture %s: %w", c.Path, err)
	}

	go t.follow()

	return t, nil
}

// Capture returns the capture the Tap follows.
func (t *Tap) Capture() Capture {
	return t.capture
}

// State is a snapshot of a Tap. Changed is closed when it is out of date.
type State struct {
	// Size is how many bytes of the capture readers may read.
	Size     int64
	Modified time.Time
	CaughtUp bool
	// Done is whether the Tap has stopped following: its capture stopped,
	// and it holds all of it, or reading it failed.
	Done    bool
	Err     error
	Changed <-chan struct{}
}

// State returns the Tap's current state.
func (t *Tap) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()

	return State{
		Size:     t.size,
		Modified: t.modified,
		CaughtUp: t.caughtUp,
		Done:     t.done,
		Err:      t.err,
		Changed:  t.changed,
	}
}

// ReadAt reads the capture's bytes, which past its State's Size are not there yet.
func (t *Tap) ReadAt(p []byte, off int64) (int, error) {
	size := t.State().Size

	if off >= size {
		return 0, io.EOF
	}

	short := false
	if int64(len(p)) > size-off {
		p = p[:size-off]
		short = true
	}

	n, err := t.store.ReadAt(p, off)
	if err == nil && short {
		err = io.EOF
	}

	return n, err //nolint:wrapcheck // io.ReaderAt semantics
}

// Stream sends the capture as a pcap byte stream: the file header, then runs
// of whole records as they arrive, until the capture stops (returning nil,
// or why following it failed) or ctx ends. With fromNow, records captured
// before the call are skipped. send must not keep the slice it is given.
func (t *Tap) Stream(ctx context.Context, fromNow bool, send func([]byte) error) error {
	var (
		buf  = make([]byte, streamChunk)
		pos  int64
		skip = fromNow
	)

	for {
		st := t.State()

		if pos == 0 && st.Size >= fileHeaderLen {
			if _, err := t.ReadAt(buf[:fileHeaderLen], 0); err != nil {
				return fmt.Errorf("reading capture header: %w", err)
			}

			if err := send(buf[:fileHeaderLen]); err != nil {
				return err
			}

			pos = fileHeaderLen
		}

		if pos > 0 && skip && st.CaughtUp {
			pos, skip = max(pos, st.Size), false
		}

		for pos > 0 && !skip && pos < st.Size {
			n, err := t.ReadAt(buf[:min(st.Size-pos, streamChunk)], pos)
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading capture: %w", err)
			}

			whole, _ := t.header.complete(buf[:n])
			if whole == 0 {
				return fmt.Errorf("%w: at byte %d", errCorrupt, pos)
			}

			if err := send(buf[:whole]); err != nil {
				return err
			}

			pos += int64(whole)

			if ctx.Err() != nil {
				return ctx.Err() //nolint:wrapcheck // caller's context
			}
		}

		if st.Done && (skip || pos >= st.Size) {
			return st.Err
		}

		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // caller's context
		case <-st.Changed:
		}
	}
}

// Release lets go of the Tap. It keeps following for a while after its last
// reader lets go, then stops and removes what it read.
func (t *Tap) Release() {
	registryMu.Lock()
	defer registryMu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.refs--

	if t.refs == 0 {
		t.idle = time.AfterFunc(idleGrace, t.expire)
	}
}

// acquire takes a reference; the registry lock is held.
func (t *Tap) acquire() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.refs++

	if t.idle != nil {
		t.idle.Stop()
		t.idle = nil
	}
}

func (t *Tap) expire() {
	registryMu.Lock()

	t.mu.Lock()
	unused := t.refs == 0
	t.mu.Unlock()

	if !unused {
		registryMu.Unlock()

		return
	}

	if registry[t.capture.key()] == t {
		delete(registry, t.capture.key())
	}

	registryMu.Unlock()

	t.halt()

	_ = t.store.Close()

	if t.spool != "" {
		_ = os.Remove(t.spool)
	}
}

// CloseAll stops following every capture and removes what was read from
// other nodes, for a process about to exit: an idle Tap is otherwise only
// removed once its grace period has passed. A reader still holding a Tap sees
// it done.
//
// The spools go first: stopping waits for any read in flight, which on an
// unresponsive node takes until the read times out. Readers, and a read
// finishing meanwhile, keep using the open file.
func CloseAll() {
	registryMu.Lock()
	taps := slices.Collect(maps.Values(registry))
	clear(registry)
	registryMu.Unlock()

	for _, t := range taps {
		if t.spool != "" {
			_ = os.Remove(t.spool)
		}
	}

	for _, t := range taps {
		t.halt()
		t.finish(nil)
	}
}

// halt stops following and waits for it to stop.
func (t *Tap) halt() {
	t.stopOnce.Do(func() { close(t.stop) })
	<-t.exited
}

// follow reads the capture as it grows until it stops or the Tap expires.
func (t *Tap) follow() {
	defer close(t.exited)
	defer func() { _ = t.src.Close() }()

	var (
		interval  = remotePoll
		checked   = time.Now()
		readFails int
	)

	if t.local {
		interval = localPoll
	}

	for {
		more, err := t.poll()

		switch {
		case err == nil:
			readFails = 0
		case errors.Is(err, errNotPcap), errors.Is(err, errCorrupt), errors.Is(err, errTruncated):
			t.finish(err)

			return
		default:
			readFails++

			if readFails >= maxReadErrors {
				t.finish(err)

				return
			}
		}

		if time.Since(checked) >= activeCheck {
			checked = time.Now()

			if !active(t.capture) {
				t.finish(t.drain())

				return
			}
		}

		if !more {
			t.markCaughtUp()

			select {
			case <-t.stop:
				return
			case <-time.After(interval):
			}

			continue
		}

		select {
		case <-t.stop:
			return
		default:
		}
	}
}

// drain reads what is left of a capture minimega has stopped writing.
func (t *Tap) drain() error {
	for {
		more, err := t.poll()
		if err != nil || !more {
			return err
		}
	}
}

// poll reads what has been written since the last poll and publishes its
// whole records. It reports whether there may be more to read already.
func (t *Tap) poll() (bool, error) {
	chunk := remoteChunk
	if t.local {
		chunk = localChunk
	}

	t.mu.Lock()
	off := t.size
	t.mu.Unlock()

	data, err := t.src.Read(off, chunk)
	if err != nil {
		return false, err //nolint:wrapcheck // sources wrap their errors
	}

	more := len(data) == chunk

	// the bytes are published from the start of data: the header first,
	// when this is the start of the file, then whole records
	records := data
	headerLen := 0

	if off == 0 {
		if len(data) < fileHeaderLen {
			return false, nil
		}

		h, err := parseHeader(data)
		if err != nil {
			return false, fmt.Errorf("%s: %w", t.capture.Path, err)
		}

		t.mu.Lock()
		t.header = h
		t.mu.Unlock()

		records, headerLen = data[fileHeaderLen:], fileHeaderLen
	}

	whole, err := t.header.complete(records)
	if err != nil {
		return false, fmt.Errorf("%s at byte %d: %w", t.capture.Path, off+int64(headerLen+whole), err)
	}

	gained := headerLen + whole
	if gained == 0 {
		return more, nil
	}

	if !t.local {
		if _, err := t.store.WriteAt(data[:gained], off); err != nil {
			return false, fmt.Errorf("spooling capture: %w", err)
		}
	}

	t.mu.Lock()
	t.size += int64(gained)
	t.modified = time.Now()
	t.notify()
	t.mu.Unlock()

	return more, nil
}

func (t *Tap) markCaughtUp() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.caughtUp {
		t.caughtUp = true
		t.notify()
	}
}

func (t *Tap) finish(err error) {
	if err != nil {
		plog.Warn(
			plog.TypeSystem,
			"stopped following capture",
			"exp", t.capture.Exp,
			"vm", t.capture.VM,
			"interface", t.capture.Interface,
			"err", err,
		)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.done = true
	t.caughtUp = true
	t.err = err
	t.notify()
}

// notify wakes everyone waiting on the Tap; t.mu is held.
func (t *Tap) notify() {
	close(t.changed)
	t.changed = make(chan struct{})
}
