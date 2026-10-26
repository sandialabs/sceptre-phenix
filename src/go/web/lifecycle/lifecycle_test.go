package lifecycle

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// useLifecycle gives the test a serving period of its own.
func useLifecycle(t *testing.T) {
	t.Helper()

	Start()
	t.Cleanup(Start)
}

// fakeConn records how it was closed.
type fakeConn struct {
	deadline atomic.Pointer[time.Time]
	// the close codes sent, in order
	codes  []int
	closed chan struct{}
}

func (c *fakeConn) WriteClose(status int) error {
	c.codes = append(c.codes, status)

	return nil
}

func (c *fakeConn) SetDeadline(t time.Time) error {
	c.deadline.Store(&t)

	return nil
}

func (c *fakeConn) Close() error {
	close(c.closed)

	return nil
}

//nolint:paralleltest // replaces the package's registry
func TestStopEndsTrackedHandlers(t *testing.T) {
	useLifecycle(t)

	stopped := make(chan string, 3)

	releaseVNC := Track("VNC", func() { stopped <- "VNC" })
	releaseNone := Track("terminal", nil)

	ctx, releaseStream := WithStop(context.Background(), "stream")

	conn := &fakeConn{closed: make(chan struct{})}
	releaseConn := TrackConn("console", conn)

	select {
	case <-Stopping():
		t.Fatal("Stopping() closed before Stop(), want open")
	default:
	}

	Stop()
	Stop() // only the first call does anything

	<-Stopping()

	if got := <-stopped; got != "VNC" {
		t.Errorf("Stop() stopped %q, want VNC", got)
	}

	<-ctx.Done()

	if !Stopped(ctx) {
		t.Errorf("Stopped(WithStop context) after Stop() = false, want true (cause %v)", context.Cause(ctx))
	}

	<-conn.closed

	if d := conn.deadline.Load(); d == nil || time.Until(*d) > CloseWait {
		t.Errorf("TrackConn() at Stop() set deadline %v, want within %s", d, CloseWait)
	}

	// written before Close, which closed conn.closed
	if diff := cmp.Diff([]int{1001}, conn.codes); diff != "" {
		t.Errorf("TrackConn() at Stop() close codes mismatch (-want +got):\n%s", diff)
	}

	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	want := map[string]int{"VNC": 1, "terminal": 1, "stream": 1, "console": 1}
	if diff := cmp.Diff(want, Wait(short)); diff != "" {
		t.Errorf("Wait() with every handler open mismatch (-want +got):\n%s", diff)
	}

	releaseVNC()
	releaseVNC() // a second release does nothing
	releaseNone()
	releaseStream()
	releaseConn()

	if got := Wait(context.Background()); got != nil {
		t.Errorf("Wait() with every handler released = %v, want nil", got)
	}
}

//nolint:paralleltest // replaces the package's registry
func TestTrackAfterStopStopsAtOnce(t *testing.T) {
	useLifecycle(t)

	Stop()

	stopped := make(chan struct{})
	release := Track("late", func() { close(stopped) })

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Track() after Stop() did not call stop, want it called at once")
	}

	release()

	if got := Wait(context.Background()); got != nil {
		t.Errorf("Wait() after the late handler ended = %v, want nil", got)
	}
}

//nolint:paralleltest // replaces the package's registry
func TestStartForgetsEarlierHandlers(t *testing.T) {
	useLifecycle(t)

	release := Track("old", nil)
	defer release()

	Stop()
	Start()

	select {
	case <-Stopping():
		t.Error("Stopping() after Start() is closed, want open")
	default:
	}

	Stop()

	if got := Wait(context.Background()); got != nil {
		t.Errorf("Wait() after Start() = %v, want nil: the earlier period's handlers are not counted", got)
	}
}

//nolint:paralleltest // replaces the package's registry
func TestRefuseWhileStopping(t *testing.T) {
	useLifecycle(t)

	before := httptest.NewRecorder()
	if RefuseWhileStopping(before) {
		t.Errorf("RefuseWhileStopping() before Stop() = true (%d), want false", before.Code)
	}

	Stop()

	after := httptest.NewRecorder()
	if !RefuseWhileStopping(after) {
		t.Fatal("RefuseWhileStopping() after Stop() = false, want true")
	}

	got := []string{strconv.Itoa(after.Code), after.Header().Get("Retry-After"), strings.TrimSpace(after.Body.String())}
	want := []string{"503", RetryAfter, Unavailable}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RefuseWhileStopping() after Stop() status, Retry-After, body mismatch (-want +got):\n%s", diff)
	}
}
