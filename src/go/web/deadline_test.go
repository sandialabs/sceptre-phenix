package web

import (
	"errors"
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

//nolint:unparam // http.ResponseController fixes the signature
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

// deadlineGoneWriter is a client that has gone away: every write fails.
type deadlineGoneWriter struct {
	*deadlineWriter
}

func (deadlineGoneWriter) Write([]byte) (int, error) {
	return 0, errors.New("broken pipe")
}

// A write that fails ends the WebShark events and the capture stream.
func TestStreamsEndWhenAWriteFails(t *testing.T) {
	t.Parallel()

	t.Run("WebShark events", func(t *testing.T) {
		t.Parallel()

		w := deadlineGoneWriter{newDeadlineWriter()}
		watch := &sharkWatch{w: w, rc: http.NewResponseController(w), heartbeat: nil}

		if watch.send("heartbeat", map[string]any{"t": 1}) {
			t.Error(`sharkWatch.send("heartbeat") to a gone client = true, want false`)
		}
	})

	t.Run("capture stream", func(t *testing.T) {
		t.Parallel()

		w := deadlineGoneWriter{newDeadlineWriter()}
		if err := pcapSender(w)([]byte("chunk")); err == nil {
			t.Error("pcapSender(w)(chunk) to a gone client = nil, want an error")
		}

		if body := w.Body.String(); body != "" {
			t.Errorf("pcapSender(w)(chunk) to a gone client wrote %q, want nothing", body)
		}
	})

	t.Run("event format", func(t *testing.T) {
		t.Parallel()

		w := newDeadlineWriter()
		watch := &sharkWatch{w: w, rc: http.NewResponseController(w), heartbeat: nil}

		if !watch.send("capture-changed", map[string]any{"size": 2}) {
			t.Fatal(`sharkWatch.send("capture-changed") = false, want true`)
		}

		if got, want := w.Body.String(), "event: capture-changed\ndata: {\"size\":2}\n\n"; got != want {
			t.Errorf(`sharkWatch.send("capture-changed") wrote %q, want %q`, got, want)
		}
	})
}
