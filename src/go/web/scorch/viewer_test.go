package scorch

import (
	"bytes"
	"errors"
	"net"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"phenix/util/polltest"
)

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
	polltest.Within(t, "the terminal's client getting all its output", func() {
		for i := range chunks {
			if _, err := w.Write(chunk); err != nil {
				t.Errorf("writing chunk %d to the PTY: %v", i, err)

				return
			}

			for len(rw.String()) < (i+1)*len(chunk) {
				runtime.Gosched()
			}
		}
	})

	mu.Lock()
	_, kept := roTerms[term.Pid][ro.id]
	mu.Unlock()

	if kept {
		t.Errorf("readTerm left the stalled read-only viewer %v in roTerms[%d], want it dropped", ro.id, term.Pid)
	}

	_ = w.Close()

	polltest.Within(t, "the PTY reader ending", func() { <-read })
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

	polltest.Within(t, "the history write starting", func() { <-slow.writing })

	close(term.Done)

	polltest.Within(t, "the exit being queued", func() { <-waited })
	polltest.Within(t, "taking the terminal lock", func() {
		mu.Lock()
		defer mu.Unlock()

		if _, ok := roTerms[term.Pid]; ok {
			t.Errorf("waitReadOnly after the terminal exited left roTerms[%d] listed, want it removed", term.Pid)
		}
	})

	ro.leave()

	polltest.Within(t, "the viewer ending", func() { <-served })
}

// serve stops at the first failed write, and closes the connection.
func TestViewerStopsOnWriteError(t *testing.T) {
	t.Parallel()

	conn := &failingConn{closed: make(chan struct{})}
	v := newViewer(conn)

	v.offer([]byte("a"))
	v.offer([]byte("b"))

	polltest.Within(t, "serving", v.serve)

	if conn.writes != 1 || !v.isGone() {
		t.Errorf("viewer.serve() on a failing conn: writes = %d, gone = %v; want 1, true", conn.writes, v.isGone())
	}

	select {
	case <-conn.closed:
	default:
		t.Error("viewer.serve() on a failing conn left it open, want it closed")
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
