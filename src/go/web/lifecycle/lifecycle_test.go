package lifecycle

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"sync"
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

//nolint:unparam // closeWriter fixes the signature
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

	var body struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}

	if err := json.Unmarshal(after.Body.Bytes(), &body); err != nil {
		t.Fatalf("RefuseWhileStopping() after Stop() body %q is not JSON: %v", after.Body.String(), err)
	}

	got := []string{
		strconv.Itoa(after.Code), after.Header().Get("Retry-After"), after.Header().Get("Content-Type"), body.Message, body.Code,
	}
	want := []string{"503", RetryAfter, "application/json", Unavailable, "unavailable"}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RefuseWhileStopping() after Stop() status, Retry-After, Content-Type, message, code mismatch (-want +got):\n%s", diff)
	}
}

// lifecycleCloser is a Conn that cannot send a close frame.
type lifecycleCloser struct {
	deadline atomic.Pointer[time.Time]
	closed   chan struct{}
}

func (c *lifecycleCloser) SetDeadline(t time.Time) error {
	c.deadline.Store(&t)

	return nil
}

func (c *lifecycleCloser) Close() error {
	close(c.closed)

	return nil
}

//nolint:paralleltest // replaces the package's registry
func TestTrackConnWithoutCloseFrame(t *testing.T) {
	useLifecycle(t)

	conn := &lifecycleCloser{closed: make(chan struct{})}
	release := TrackConn("console", conn)

	defer release()

	Stop()

	select {
	case <-conn.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("TrackConn() of a connection without close frames not closed 5s after Stop()")
	}

	if d := conn.deadline.Load(); d == nil || time.Until(*d) > CloseWait {
		t.Errorf("TrackConn() at Stop() set deadline %v, want within %s", d, CloseWait)
	}
}

// Wait returns nil when its context ends with nothing open, stopped or not.
//
//nolint:paralleltest // replaces the package's registry
func TestWaitWithNothingOpen(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run("stopped "+strconv.FormatBool(stop), func(t *testing.T) {
			useLifecycle(t)

			if stop {
				Stop()
			}

			ended, cancel := context.WithCancel(context.Background())
			cancel()

			if got := Wait(ended); got != nil {
				t.Errorf("Wait(ended context) with nothing open = %v, want nil", got)
			}

			if got := IsStopping(); got != stop {
				t.Errorf("IsStopping() = %t, want %t", got, stop)
			}
		})
	}
}

// A WithStop context released before shutdown ends without ErrStopping.
//
//nolint:paralleltest // replaces the package's registry
func TestWithStopReleasedBeforeStop(t *testing.T) {
	useLifecycle(t)

	ctx, release := WithStop(context.Background(), "stream")
	release()

	<-ctx.Done()

	if Stopped(ctx) {
		t.Errorf("Stopped(released WithStop context) = true, want false (cause %v)", context.Cause(ctx))
	}

	if Stopped(context.Background()) {
		t.Error("Stopped(context.Background()) = true, want false")
	}

	Stop()

	if got := Wait(context.Background()); got != nil {
		t.Errorf("Wait() after the only handler was released = %v, want nil", got)
	}
}

// Each handler tracked while Stop runs is stopped exactly once, whether
// Stop or Track stops it, and Wait returns once all are released.
//
//nolint:paralleltest // replaces the package's registry
func TestTrackRacingStop(t *testing.T) {
	useLifecycle(t)

	const handlers = 50

	var (
		stops    [handlers]atomic.Int32
		stopped  sync.WaitGroup
		tracked  sync.WaitGroup
		releases = make(chan func(), handlers)
	)

	stopped.Add(handlers)
	tracked.Add(handlers)

	for i := range handlers {
		go func() {
			defer tracked.Done()

			releases <- Track("racer", func() {
				stops[i].Add(1)
				stopped.Done()
			})
		}()
	}

	Stop()
	tracked.Wait()
	stopped.Wait()
	close(releases)

	for i := range handlers {
		if got := stops[i].Load(); got != 1 {
			t.Errorf("handler %d stopped %d times, want 1", i, got)
		}
	}

	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if diff := cmp.Diff(map[string]int{"racer": handlers}, Wait(short)); diff != "" {
		t.Errorf("Wait() with every racer open mismatch (-want +got):\n%s", diff)
	}

	for release := range releases {
		go release()
	}

	if got := Wait(context.Background()); got != nil {
		t.Errorf("Wait() after every racer was released = %v, want nil", got)
	}
}

// Wait counts, and waits for, a handler tracked after Stop ran with nothing
// open.
//
//nolint:paralleltest // replaces the package's registry
func TestWaitCountsHandlerTrackedAfterStop(t *testing.T) {
	tests := []struct {
		name    string
		release bool
		want    map[string]int
	}{
		{name: "still open", release: false, want: map[string]int{"late": 1}},
		{name: "released later", release: true, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useLifecycle(t)

			Stop()

			release := Track("late", nil)
			defer release()

			var released atomic.Bool

			ctx := context.Background()

			if tt.release {
				go func() {
					time.Sleep(20 * time.Millisecond)
					released.Store(true)
					release()
				}()
			} else {
				short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()

				ctx = short
			}

			if diff := cmp.Diff(tt.want, Wait(ctx)); diff != "" {
				t.Errorf("Wait() with a handler tracked after Stop() mismatch (-want +got):\n%s", diff)
			}

			if got := released.Load(); got != tt.release {
				t.Errorf("Wait() returned with the late handler released = %t, want %t", got, tt.release)
			}
		})
	}
}
