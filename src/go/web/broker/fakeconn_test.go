package broker

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// withinTimeout is how long within waits.
const withinTimeout = 2 * time.Second

// within fails the test if fn does not return within withinTimeout.
func within(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(withinTimeout):
		t.Fatalf("%s took longer than %v", what, withinTimeout)
	}
}

// fakeConn is a client connection that takes every frame at once. Data
// messages arrive on frames, control frames on controls.
type fakeConn struct {
	t *testing.T

	frames   chan []byte
	controls chan controlFrame
	// messages ReadMessage returns, in order; it blocks once they run out
	reads chan []byte

	closed    chan struct{}
	closeOnce sync.Once

	// data writers active at once; more than one is a race gorilla forbids
	writers atomic.Int32
}

type controlFrame struct {
	kind int
	data []byte
}

func newFakeConn(t *testing.T) *fakeConn {
	t.Helper()

	return &fakeConn{
		t:        t,
		frames:   make(chan []byte, 4096),
		controls: make(chan controlFrame, 64),
		reads:    make(chan []byte, 64),
		closed:   make(chan struct{}),
	}
}

func (c *fakeConn) SetReadLimit(int64)                {}
func (c *fakeConn) SetReadDeadline(time.Time) error   { return nil }
func (c *fakeConn) SetPongHandler(func(string) error) {}
func (c *fakeConn) SetWriteDeadline(time.Time) error  { return nil }
func (c *fakeConn) ReadMessage() (int, []byte, error) { return c.read() }

func (c *fakeConn) NextWriter(int) (io.WriteCloser, error) { return c.nextWriter() }

func (c *fakeConn) isClosed() bool { return isDone(c.closed) }

func (c *fakeConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })

	return nil
}

func (c *fakeConn) read() (int, []byte, error) {
	select {
	case msg := <-c.reads:
		return websocket.TextMessage, msg, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *fakeConn) nextWriter() (io.WriteCloser, error) {
	if c.isClosed() {
		return nil, net.ErrClosed
	}

	if c.writers.Add(1) > 1 {
		c.t.Error("two data writers at once")
	}

	return &fakeWriter{conn: c, buf: bytes.Buffer{}}, nil
}

func (c *fakeConn) WriteControl(kind int, data []byte, _ time.Time) error {
	if c.isClosed() {
		return net.ErrClosed
	}

	c.controls <- controlFrame{kind: kind, data: data}

	return nil
}

// messages returns the messages written so far; a frame can carry several,
// one per line.
func (c *fakeConn) messages() [][]byte {
	var msgs [][]byte

	for {
		select {
		case f := <-c.frames:
			msgs = append(msgs, bytes.Split(f, newline)...)
		default:
			return msgs
		}
	}
}

type fakeWriter struct {
	conn *fakeConn
	buf  bytes.Buffer
}

func (w *fakeWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }

func (w *fakeWriter) Close() error {
	w.conn.writers.Add(-1)
	w.conn.frames <- w.buf.Bytes()

	return nil
}

// blockingConn is a client connection that has stopped reading: a data write
// blocks until the write deadline passes or the connection is closed.
type blockingConn struct {
	*fakeConn

	mu       sync.Mutex
	deadline time.Time

	// closed when the first data write starts blocking
	writing     chan struct{}
	writingOnce sync.Once
}

func newBlockingConn(t *testing.T) *blockingConn {
	t.Helper()

	return &blockingConn{fakeConn: newFakeConn(t), writing: make(chan struct{})}
}

func (c *blockingConn) SetWriteDeadline(d time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deadline = d

	return nil
}

func (c *blockingConn) NextWriter(int) (io.WriteCloser, error) {
	if c.isClosed() {
		return nil, net.ErrClosed
	}

	if c.writers.Add(1) > 1 {
		c.t.Error("two data writers at once")
	}

	return &blockingWriter{conn: c}, nil
}

type blockingWriter struct {
	conn *blockingConn
}

var errDeadline = errors.New("write deadline passed")

func (w *blockingWriter) Write([]byte) (int, error) {
	w.conn.writingOnce.Do(func() { close(w.conn.writing) })

	w.conn.mu.Lock()
	d := w.conn.deadline
	w.conn.mu.Unlock()

	var timeout <-chan time.Time

	if !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()

		timeout = timer.C
	}

	select {
	case <-w.conn.closed:
		return 0, net.ErrClosed
	case <-timeout:
		return 0, errDeadline
	}
}

func (w *blockingWriter) Close() error {
	w.conn.writers.Add(-1)

	return nil
}

func isDone(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// newFakeClient returns a client on conn that may do anything.
func newFakeClient(conn wsConn) *Client {
	return &Client{
		role:     roleAllowing("*", "*"),
		conn:     conn,
		publish:  make(chan any, publishChannelBuffer),
		done:     make(chan struct{}),
		shotSize: defaultBrokerScreenshotSize,
	}
}
