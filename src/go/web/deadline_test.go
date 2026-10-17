package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// deadlineWriter records whether a write deadline was set before each write.
type deadlineWriter struct {
	*httptest.ResponseRecorder

	deadline time.Time
	// writes made with a fresh deadline, and without one
	bounded, unbounded int
}

func (w *deadlineWriter) SetWriteDeadline(d time.Time) error {
	w.deadline = d

	return nil
}

func (w *deadlineWriter) Write(b []byte) (int, error) {
	if w.deadline.After(time.Now()) {
		w.bounded++
	} else {
		w.unbounded++
	}

	w.deadline = time.Time{}

	return w.ResponseRecorder.Write(b)
}

func newDeadlineWriter() *deadlineWriter {
	return &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
}

// Every WebShark event and every chunk of an HTTP capture stream is written
// with a deadline, so a client that stops reading lets go of the capture.
func TestStreamsWriteWithDeadlines(t *testing.T) {
	t.Parallel()

	t.Run("WebShark events", func(t *testing.T) {
		t.Parallel()

		w := newDeadlineWriter()
		watch := &sharkWatch{w: w, rc: http.NewResponseController(w), heartbeat: nil}

		for range 3 {
			if !watch.send("heartbeat", map[string]any{"t": 1}) {
				t.Fatal(`sharkWatch.send("heartbeat") = false, want true`)
			}
		}

		if w.bounded != 3 {
			t.Errorf("after 3 sharkWatch.send calls, writes with a deadline = %d, want 3", w.bounded)
		}

		if w.unbounded != 0 {
			t.Errorf("after 3 sharkWatch.send calls, writes without a deadline = %d, want 0", w.unbounded)
		}
	})

	t.Run("capture stream", func(t *testing.T) {
		t.Parallel()

		w := newDeadlineWriter()
		send := pcapSender(w)

		for range 3 {
			if err := send([]byte("chunk")); err != nil {
				t.Fatalf("pcapSender(w)(%q) error = %v", "chunk", err)
			}
		}

		if w.bounded != 3 {
			t.Errorf("after 3 pcapSender sends, writes with a deadline = %d, want 3", w.bounded)
		}

		if w.unbounded != 0 {
			t.Errorf("after 3 pcapSender sends, writes without a deadline = %d, want 0", w.unbounded)
		}
	})
}
