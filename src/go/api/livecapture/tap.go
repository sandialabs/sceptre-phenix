// Package livecapture follows pcap and pcapng captures while minimega is still writing them, so their packets can be
// streamed as they arrive. A Tap follows one capture: a VM interface's on the node running the VM (directly on the
// headnode, a byte range at a time over the mesh elsewhere), or an experiment capture's merged file on the headnode. It
// exposes only whole records or blocks, and is shared by any number of readers.
package livecapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
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

	// how long one of those questions may take.
	activeCheckTimeout = 30 * time.Second

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

	// streamChunk bounds one send of a Stream; it holds whole records or
	// blocks. A pcapng block larger than streamChunk is sent alone.
	streamChunk = 1 << 20
)

var (
	// ErrNotCapturing is returned for a VM interface minimega is not
	// capturing, or an experiment capture that does not run.
	ErrNotCapturing = errors.New("no capture is running")

	// errTruncated is returned when a capture file shrinks, which minimega
	// never does to a capture it is still writing.
	errTruncated = errors.New("capture file truncated")
)

// Capture is a running capture: of one VM interface, or the experiment capture ID.
type Capture struct {
	Exp       string
	VM        string
	Interface int

	// ID is the ID of an experiment capture, empty for a VM interface's.
	ID string

	// Path is where minimega writes the capture, on Host.
	Path string
	Host string
}

// key names the capture in the registry. An experiment capture's key holds NUL bytes, which no VM name holds, so it
// differs from the key of every VM interface.
func (c Capture) key() string {
	if c.ID != "" {
		return c.Exp + "\x00capture\x00" + c.ID
	}

	return c.Exp + "/" + c.VM + "/" + strconv.Itoa(c.Interface)
}

// find returns the running capture of the VM's interface. A stopped
// experiment has no captures; asking minimega about one would recreate its
// namespace, so it is not asked. A capture that failed or is full has ended.
func find(exp, vm string, iface int) (Capture, error) {
	missing := fmt.Errorf("%w: interface %d of VM %s in experiment %s", ErrNotCapturing, iface, vm, exp)

	if !experiment.Running(exp) {
		return Capture{}, missing
	}

	for _, c := range mm.GetVMCaptures(mm.NS(exp), mm.VMName(vm)) {
		if c.Interface != iface || c.State == "failed" || c.State == "full" {
			continue
		}

		host, err := mm.GetVMHost(mm.NS(exp), mm.VMName(vm))
		if err != nil {
			return Capture{}, fmt.Errorf("finding the host of VM %s: %w", vm, err)
		}

		return Capture{Exp: exp, VM: vm, Interface: iface, ID: "", Path: c.Filepath, Host: host}, nil
	}

	return Capture{}, missing
}

// findExperiment returns the experiment capture id while minimega runs it, asking minimega in ctx. minimega merges the
// capture into one file on the headnode, so the Tap reads that file. A stopped experiment is not asked about, as find
// does not ask.
func findExperiment(ctx context.Context, exp, id string) (Capture, error) {
	missing := fmt.Errorf("%w: experiment capture %s in experiment %s", ErrNotCapturing, id, exp)

	if !experiment.Running(exp) {
		return Capture{}, missing
	}

	captures, err := mm.GetNamespaceCaptures(mm.NS(exp), mm.Context(ctx))
	if err != nil {
		return Capture{}, fmt.Errorf("listing the experiment captures of %s: %w", exp, err)
	}

	for _, c := range captures {
		if c.ID != id || !c.Running() || c.Path == "" {
			continue
		}

		return Capture{Exp: exp, VM: "", Interface: 0, ID: id, Path: mm.GetMMFullPath(c.Path), Host: mm.Headnode()}, nil
	}

	return Capture{}, missing
}

// active reports whether minimega is still writing the capture, asking it in ctx. It returns an error, not false, when
// minimega cannot tell.
func active(ctx context.Context, c Capture) (bool, error) {
	var (
		found Capture
		err   error
	)

	if c.ID != "" {
		found, err = findExperiment(ctx, c.Exp, c.ID)
	} else {
		found, err = find(c.Exp, c.VM, c.Interface)
	}

	switch {
	case errors.Is(err, ErrNotCapturing):
		return false, nil
	case err != nil:
		return false, err
	}

	return found.Path == c.Path, nil
}

// newSource returns what reads the capture's file, and whether that is the
// file itself, on the headnode.
func newSource(c Capture) (source, bool, error) {
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
	format   format // reads the bytes at size
	preamble []span // the SHB, IDBs and NRBs of the last pcapng section, in the order written
	size     int64  // bytes of store readers may read: the header and whole records or blocks
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
	key := Capture{Exp: exp, VM: vm, Interface: iface, ID: "", Path: "", Host: ""}.key()

	return open(key, func() (Capture, error) { return find(exp, vm, iface) })
}

// OpenExperiment is Open for the experiment capture id of the experiment exp, which it reads from the merged file on
// the headnode. ctx bounds the question to minimega that finds the capture.
func OpenExperiment(ctx context.Context, exp, id string) (*Tap, error) {
	key := Capture{Exp: exp, VM: "", Interface: 0, ID: id, Path: "", Host: ""}.key()

	return open(key, func() (Capture, error) { return findExperiment(ctx, exp, id) })
}

// open returns a Tap following the capture at key, which locate finds, as Open does.
func open(key string, locate func() (Capture, error)) (*Tap, error) {
	if t := acquireRunning(key); t != nil {
		return t, nil
	}

	c, err := locate()
	if err != nil {
		registryMu.Lock()
		defer registryMu.Unlock()

		if current := registry[key]; errors.Is(err, ErrNotCapturing) && current != nil {
			current.acquire()

			return current, nil
		}

		return nil, err
	}

	// another Open started one meanwhile
	if t := acquireRunning(key); t != nil {
		return t, nil
	}

	// Asks minimega whether the capture is on the headnode and may create a
	// spool, so it runs without the registry lock, which Release and expiry
	// of every other capture take.
	t, err := prepare(c)

	registryMu.Lock()
	defer registryMu.Unlock()

	// and another started one while this one was prepared
	if current := registry[key]; current != nil && !current.State().Done {
		current.acquire()

		if t != nil {
			go t.discard()
		}

		return current, nil
	}

	if err != nil {
		return nil, err
	}

	registry[key] = t

	go t.follow()

	t.acquire()

	return t, nil
}

// acquireRunning takes a reference to the Tap following the capture at key, if
// one is still following it.
func acquireRunning(key string) *Tap {
	registryMu.Lock()
	defer registryMu.Unlock()

	if t := registry[key]; t != nil && !t.State().Done {
		t.acquire()

		return t
	}

	return nil
}

// prepare readies a Tap of the capture, which follows it once started.
func prepare(c Capture) (*Tap, error) {
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
		t.store, err = os.CreateTemp("", spoolPattern)
		if t.store != nil {
			t.spool = t.store.Name()
		}
	}

	if err != nil {
		_ = src.Close()

		return nil, fmt.Errorf("opening store for capture %s: %w", c.Path, err)
	}

	return t, nil
}

// spoolPattern names the temporary files a Tap of a capture on another node
// spools the capture to.
const spoolPattern = "phenix-capture-*.pcap"

// RemoveStaleSpools deletes the capture spools in the temporary directory
// last written before since, such as those a server that was stopped or
// crashed with a capture open left, and returns how many it deleted. A spool
// still written to is newer, and a Tap reads its spool through the file it
// holds open, so deleting one in use would lose nothing.
func RemoveStaleSpools(since time.Time) (int, error) {
	spools, err := filepath.Glob(filepath.Join(os.TempDir(), spoolPattern))
	if err != nil {
		return 0, fmt.Errorf("listing capture spools: %w", err)
	}

	var (
		removed int
		errs    []error
	)

	for _, spool := range spools {
		info, err := os.Lstat(spool)
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(since) {
			continue
		}

		if err := os.Remove(spool); err != nil {
			errs = append(errs, err)

			continue
		}

		removed++
	}

	return removed, errors.Join(errs...)
}

// discard closes a Tap prepare returned that was never started.
func (t *Tap) discard() {
	_ = t.src.Close()
	_ = t.store.Close()

	if t.spool != "" {
		_ = os.Remove(t.spool)
	}
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

// Stream sends the capture as a pcap or pcapng byte stream: the pcap file
// header, then runs of whole records or blocks as they arrive, until the
// capture stops (returning nil, or why following it failed) or ctx ends. With
// fromNow, packets captured before the call are skipped; a pcapng stream
// first gets the blocks that describe the section and its interfaces. send
// must not keep the slice it is given.
func (t *Tap) Stream(ctx context.Context, fromNow bool, send func([]byte) error) error {
	s := &streamer{tap: t, send: send, buf: make([]byte, streamChunk), pos: 0, format: format{ng: false, order: nil}}
	skip := fromNow

	for {
		st := t.State()

		// caught up with nothing published: every byte to come is from now on
		if skip && st.CaughtUp && st.Size == 0 {
			skip = false
		}

		if s.format.order == nil && st.Size > 0 {
			if err := s.header(); err != nil {
				return err
			}
		}

		if s.format.order != nil && skip && st.CaughtUp {
			if err := s.preamble(); err != nil {
				return err
			}

			skip = false
		}

		if s.format.order != nil && !skip {
			if err := s.records(ctx, st.Size); err != nil {
				return err
			}
		}

		if st.Done && (skip || s.pos >= st.Size) {
			return st.Err
		}

		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // caller's context
		case <-st.Changed:
		}
	}
}

// span is a byte range [start, end) of a capture.
type span struct {
	start, end int64
}

// fileFormat returns a format that reads the capture from its start. The
// Tap keeps only the format at its size: the pcap format does not change, and
// the section header at the start of a pcapng file sets its own byte order.
func (t *Tap) fileFormat() format {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.format
}

// cut returns how many bytes readers may read, the pcapng blocks a reader
// that starts there needs first, and the format of the bytes there.
func (t *Tap) cut() (int64, []span, format) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.size, slices.Clone(t.preamble), t.format
}

// streamer is one Stream of a Tap.
type streamer struct {
	tap    *Tap
	send   func([]byte) error
	buf    []byte
	pos    int64  // of the next byte to send
	format format // of the bytes at pos; its order is nil until the first bytes are published
}

// header sends the pcap file header. A pcapng file has none.
func (s *streamer) header() error {
	s.format = s.tap.fileFormat()

	n := s.format.headerLen()
	if n == 0 {
		return nil
	}

	if _, err := s.tap.ReadAt(s.buf[:n], 0); err != nil {
		return fmt.Errorf("reading capture header: %w", err)
	}

	if err := s.send(s.buf[:n]); err != nil {
		return err
	}

	s.pos = int64(n)

	return nil
}

// preamble moves the stream to the end of what the Tap holds now. It first
// sends the pcapng blocks before that point which the blocks after it need.
func (s *streamer) preamble() error {
	cut, spans, next := s.tap.cut()

	var n int

	for _, sp := range spans {
		size := int(sp.end - sp.start)

		if n > 0 && n+size > streamChunk {
			if err := s.flush(n); err != nil {
				return err
			}

			n = 0
		}

		s.grow(n + size)

		if _, err := s.tap.ReadAt(s.buf[n:n+size], sp.start); err != nil {
			return fmt.Errorf("reading capture preamble: %w", err)
		}

		n += size
	}

	if err := s.flush(n); err != nil {
		return err
	}

	s.pos, s.format = max(s.pos, cut), next

	return nil
}

// records sends the whole records or blocks from pos up to size.
func (s *streamer) records(ctx context.Context, size int64) error {
	limit := streamChunk

	for s.pos < size {
		n, err := s.tap.ReadAt(s.buf[:min(size-s.pos, int64(limit))], s.pos)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading capture: %w", err)
		}

		whole, next, _ := s.format.complete(s.buf[:n], nil)
		if whole == 0 {
			// a block larger than the limit is read and sent alone
			if need := s.format.pending(s.buf[:n]); need > limit && int64(need) <= size-s.pos {
				limit = need
				s.grow(need)

				continue
			}

			return fmt.Errorf("%w: at byte %d", errCorrupt, s.pos)
		}

		if err := s.send(s.buf[:whole]); err != nil {
			return err
		}

		s.pos, s.format, limit = s.pos+int64(whole), next, streamChunk

		if ctx.Err() != nil {
			return ctx.Err() //nolint:wrapcheck // caller's context
		}
	}

	return nil
}

// flush sends the first n bytes of the buffer, if there are any.
func (s *streamer) flush(n int) error {
	if n == 0 {
		return nil
	}

	return s.send(s.buf[:n])
}

// grow makes the buffer hold at least n bytes.
func (s *streamer) grow(n int) {
	if len(s.buf) < n {
		s.buf = make([]byte, n)
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

// CloseAll stops following every capture and removes the spools read from other nodes, for a process about to exit; a
// reader still holding a Tap sees it done. The spools go first, since stopping waits for any read in flight, which on an
// unresponsive node lasts until the read times out.
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
		interval   = remotePoll
		checked    = time.Now()
		readFails  int
		checkFails int
	)

	if t.local {
		interval = localPoll
	}

	// a question to minimega gives up once the Tap stops
	stopped, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-t.stop:
			cancel()
		case <-stopped.Done():
		}
	}()

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

			ctx, cancelCheck := context.WithTimeout(stopped, activeCheckTimeout)
			running, err := active(ctx, t.capture)

			cancelCheck()

			switch {
			case err != nil:
				// as for reads, only failures in a row end the Tap
				checkFails++

				if checkFails >= maxReadErrors {
					t.finish(fmt.Errorf("asking minimega whether the capture runs: %w", err))

					return
				}
			case !running:
				t.finish(t.drain())

				return
			default:
				checkFails = 0
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
// whole records or blocks. It reports whether there may be more to read already.
func (t *Tap) poll() (bool, error) {
	t.mu.Lock()
	off, f := t.size, t.format
	t.mu.Unlock()

	r, err := t.read(off, f)
	if err != nil {
		return false, err
	}

	if r.gained == 0 {
		return r.more, nil
	}

	if !t.local {
		if _, err := t.store.WriteAt(r.data[:r.gained], off); err != nil {
			return false, fmt.Errorf("spooling capture: %w", err)
		}
	}

	t.mu.Lock()
	t.size += int64(r.gained)
	t.format = r.format

	// a new section's blocks do not need the blocks of the sections before it
	if r.section {
		t.preamble = r.preamble
	} else {
		t.preamble = append(t.preamble, r.preamble...)
	}

	t.modified = time.Now()
	t.notify()
	t.mu.Unlock()

	return r.more, nil
}

// polled is what one poll read.
type polled struct {
	data   []byte
	gained int    // leading bytes of data to publish: the header, then whole records or blocks
	more   bool   // data filled what was asked for
	format format // of the bytes after the gained ones

	// the SHB, IDBs and NRBs in the gained bytes; section is set when they
	// start with an SHB
	preamble []span
	section  bool
}

// read reads the capture from off, where f is its format, and finds the
// whole records or blocks there. A pcapng block larger than one chunk is read
// again, alone.
func (t *Tap) read(off int64, f format) (polled, error) {
	chunk := remoteChunk
	if t.local {
		chunk = localChunk
	}

	for {
		data, err := t.src.Read(off, chunk)
		if err != nil {
			return polled{}, err //nolint:wrapcheck // sources wrap their errors
		}

		// the bytes are published from the start of data: the header first,
		// when this is the start of the file, then whole records or blocks
		head := 0

		if off == 0 {
			if len(data) < fileHeaderLen {
				return polled{}, nil
			}

			if f, err = parseHeader(data); err != nil {
				return polled{}, fmt.Errorf("%s: %w", t.capture.Path, err)
			}

			head = f.headerLen()
		}

		r := polled{data: data, gained: 0, more: len(data) == chunk, format: f, preamble: nil, section: false}
		base := off + int64(head)

		whole, next, err := f.complete(data[head:], func(blockType uint32, start, end int) {
			if blockType == blockSHB {
				r.preamble, r.section = nil, true
			}

			if blockType == blockSHB || blockType == blockIDB || blockType == blockNRB {
				r.preamble = append(r.preamble, span{start: base + int64(start), end: base + int64(end)})
			}
		})
		if err != nil {
			return polled{}, fmt.Errorf("%s at byte %d: %w", t.capture.Path, base+int64(whole), err)
		}

		if need := head + f.pending(data[head:]); whole == 0 && r.more && need > chunk {
			chunk = need

			continue
		}

		r.gained, r.format = head+whole, next

		return r, nil
	}
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
			"capture", t.capture.ID,
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
