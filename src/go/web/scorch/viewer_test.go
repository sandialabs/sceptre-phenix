package scorch

import (
	"bytes"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// within fails the test if fn does not return within 2 seconds.
func within(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s took longer than 2s", what)
	}
}

// fastConn is a viewer's websocket that takes every write at once.
type fastConn struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed chan struct{}
	once   sync.Once
}

func newFastConn() *fastConn { return &fastConn{closed: make(chan struct{})} }

func (c *fastConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.buf.Write(b)
}

func (c *fastConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fastConn) Close() error {
	c.once.Do(func() { close(c.closed) })

	return nil
}

func (c *fastConn) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.buf.String()
}

// stuckConn is a viewer's websocket whose client stopped reading: a write
// blocks until the connection is closed.
type stuckConn struct {
	closed  chan struct{}
	once    sync.Once
	writing chan struct{}
	started sync.Once
}

func newStuckConn() *stuckConn {
	return &stuckConn{closed: make(chan struct{}), writing: make(chan struct{})}
}

func (c *stuckConn) Write([]byte) (int, error) {
	c.started.Do(func() { close(c.writing) })

	<-c.closed

	return 0, net.ErrClosed
}

func (c *stuckConn) SetWriteDeadline(time.Time) error { return nil }

func (c *stuckConn) Close() error {
	c.once.Do(func() { close(c.closed) })

	return nil
}

// drained reports whether v's queue was closed, reading what is left in it.
func drained(v *viewer) bool {
	for {
		select {
		case _, ok := <-v.out:
			if !ok {
				return true
			}
		default:
			return false
		}
	}
}

// A read-only terminal viewer that stops reading is dropped; the terminal's
// own client gets all its output, and the PTY keeps being read.
//
//nolint:paralleltest // uses the package's terminal state
func TestSlowReadOnlyTerminalDoesNotStallThePTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	term := WebTerm{Pid: -1, Pty: r, Done: make(chan struct{})}

	t.Cleanup(func() {
		mu.Lock()
		delete(roTerms, term.Pid)
		delete(history, term.Pid)
		mu.Unlock()
	})

	slow := newStuckConn()
	ro := attachReadOnly(term, slow)

	go ro.serve()

	t.Cleanup(ro.leave)

	rw := newFastConn()
	read := make(chan struct{})

	go func() {
		defer close(read)

		readTerm(term, rw)
	}()

	chunk := bytes.Repeat([]byte("x"), 64)

	const chunks = 2 * viewerQueue

	// one chunk at a time, each read before the next is written, so the
	// reader queues each on its own
	within(t, "the terminal's client getting all its output", func() {
		for i := range chunks {
			if _, err := w.Write(chunk); err != nil {
				t.Error(err)

				return
			}

			for len(rw.String()) < (i+1)*len(chunk) {
				time.Sleep(time.Millisecond)
			}
		}
	})

	mu.Lock()
	_, kept := roTerms[term.Pid][ro.id]
	mu.Unlock()

	if kept {
		t.Error("the stalled read-only viewer is still attached")
	}

	_ = w.Close()

	within(t, "the PTY reader ending", func() { <-read })
}

// A terminal exiting tells its read-only viewers without writing to them
// under the terminal lock.
//
//nolint:paralleltest // uses the package's terminal state
func TestTerminalExitDoesNotWriteUnderLock(t *testing.T) {
	term := WebTerm{Pid: -2, Done: make(chan struct{})}

	mu.Lock()
	history[term.Pid] = *bytes.NewBufferString("output so far")
	mu.Unlock()

	t.Cleanup(func() {
		mu.Lock()
		delete(roTerms, term.Pid)
		delete(history, term.Pid)
		mu.Unlock()
	})

	slow := newStuckConn()
	ro := attachReadOnly(term, slow)
	served := make(chan struct{})

	go func() {
		defer close(served)

		ro.serve()
	}()

	t.Cleanup(ro.leave)

	waited := make(chan struct{})

	go func() {
		defer close(waited)

		waitReadOnly(term, ro)
	}()

	within(t, "the history write starting", func() { <-slow.writing })

	close(term.Done)

	within(t, "the exit being queued", func() { <-waited })
	within(t, "taking the terminal lock", func() {
		mu.Lock()
		defer mu.Unlock()

		if _, ok := roTerms[term.Pid]; ok {
			t.Error("the exited terminal's viewers are still listed")
		}
	})

	ro.leave()

	within(t, "the viewer ending", func() { <-served })
}

// serve stops at the first failed write, and closes the connection.
func TestViewerStopsOnWriteError(t *testing.T) {
	t.Parallel()

	conn := &failingConn{closed: make(chan struct{})}
	v := newViewer(conn)

	v.offer([]byte("a"))
	v.offer([]byte("b"))

	within(t, "serving", v.serve)

	if conn.writes != 1 || !v.isGone() {
		t.Errorf("writes = %d, gone = %v; want 1 write, gone", conn.writes, v.isGone())
	}

	select {
	case <-conn.closed:
	default:
		t.Error("connection left open")
	}
}

type failingConn struct {
	writes int
	closed chan struct{}
}

func (c *failingConn) Write([]byte) (int, error) {
	c.writes++

	return 0, errors.New("broken pipe")
}

func (c *failingConn) SetWriteDeadline(time.Time) error { return nil }

func (c *failingConn) Close() error {
	close(c.closed)

	return nil
}
