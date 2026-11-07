package scorch

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
)

const (
	// chunks of output queued for a viewer; a viewer further behind is
	// dropped, and reopening the output replays all of it.
	viewerQueue = 256
	// how long one chunk may take to reach a viewer.
	viewerWriteTimeout = 10 * time.Second
)

// frameConn is the part of a websocket a viewer writes to.
type frameConn interface {
	Write(b []byte) (int, error)
	SetWriteDeadline(t time.Time) error
	Close() error
}

// viewer is a websocket watching component or terminal output. Whoever owns
// the output queues chunks with offer and never waits on the socket; serve,
// on the viewer's own goroutine, writes them out.
type viewer struct {
	id   string
	conn frameConn
	out  chan []byte

	// closed once the viewer stops being served: its client left, a write
	// failed, or its output finished
	gone     chan struct{}
	goneOnce sync.Once

	// set by finish; read and written only by the output's owner
	finished bool
}

func newViewer(conn frameConn) *viewer {
	return &viewer{
		id:       uuid.Must(uuid.NewV4()).String(),
		conn:     conn,
		out:      make(chan []byte, viewerQueue),
		gone:     make(chan struct{}),
		goneOnce: sync.Once{},
		finished: false,
	}
}

// offer queues a copy of b for the viewer without waiting. It reports false
// when the viewer's queue is full or its output finished; the owner then
// finishes and forgets it.
func (v *viewer) offer(b []byte) bool {
	if v.finished {
		return false
	}

	select {
	case v.out <- bytes.Clone(b):
		return true
	default:
		return false
	}
}

// finish ends the viewer's output once its queue is written. Only the
// output's owner calls it.
func (v *viewer) finish() {
	if !v.finished {
		v.finished = true

		close(v.out)
	}
}

// isGone reports whether the viewer has stopped being served.
func (v *viewer) isGone() bool {
	select {
	case <-v.gone:
		return true
	default:
		return false
	}
}

// serve writes the viewer's queued output until it finishes, a write fails
// or the client leaves, then closes the connection.
func (v *viewer) serve() {
	defer v.leave()

	for {
		select {
		case <-v.gone:
			return
		case b, ok := <-v.out:
			if !ok {
				return
			}

			if len(b) == 0 {
				continue
			}

			if err := v.conn.SetWriteDeadline(time.Now().Add(viewerWriteTimeout)); err != nil {
				return
			}

			if _, err := v.conn.Write(b); err != nil {
				return
			}
		}
	}
}

// watch reads from the viewer's client, which sends nothing, to notice it
// leaving.
func (v *viewer) watch(r io.Reader) {
	_, _ = io.Copy(io.Discard, r)

	v.leave()
}

// leave marks the viewer gone and closes its connection, ending a write in
// progress.
func (v *viewer) leave() {
	v.goneOnce.Do(func() { close(v.gone) })

	_ = v.conn.Close()
}
