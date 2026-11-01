package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"
	xwebsocket "golang.org/x/net/websocket"

	"phenix/api/job"
	"phenix/util/plog"
	"phenix/util/polltest"
	"phenix/web/lifecycle"
)

// shutdownSlack is how long past its grace period shutting down may take.
const shutdownSlack = time.Second

// testServer is a uiServer a test runs on 127.0.0.1, with a job registry of
// the test's own.
type testServer struct {
	url  string
	jobs *job.Registry

	cancel context.CancelCauseFunc
	once   sync.Once
	result chan error
	took   time.Duration
	err    error
}

func runTestServer(t *testing.T, grace time.Duration, handler http.Handler) *testServer {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on 127.0.0.1: %v", err)
	}

	server := &uiServer{
		grace:          grace,
		servers:        []servedHTTP{{name: "HTTP server", srv: newHTTPServer("", handler), ln: ln, serve: (*http.Server).Serve}},
		socket:         "",
		stopBackground: func() {},
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	s := &testServer{url: "http://" + ln.Addr().String(), jobs: useJobs(t), cancel: cancel, result: make(chan error, 1)}

	go func() { s.result <- server.run(ctx) }()

	t.Cleanup(func() {
		s.stop()
		lifecycle.Start()
	})

	return s
}

// stop shuts the server down as a SIGTERM does, and returns how long run took
// to return, and what it returned.
func (s *testServer) stop() (time.Duration, error) {
	s.once.Do(func() {
		began := time.Now()
		s.cancel(SignalError{Signal: syscall.SIGTERM})

		select {
		case s.err = <-s.result:
		case <-time.After(30 * time.Second):
			s.err = errors.New("run did not return within 30s")
		}

		s.took = time.Since(began)
	})

	return s.took, s.err
}

// captureLogs returns what gives the messages logged since, at each level.
func captureLogs(t *testing.T) func() map[string][]string {
	t.Helper()

	var (
		mu   sync.Mutex
		msgs = map[string][]string{}
	)

	plog.AddHandler(t.Name(), slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		var record struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}

		if err := json.Unmarshal(p, &record); err == nil {
			mu.Lock()
			msgs[record.Level] = append(msgs[record.Level], record.Msg)
			mu.Unlock()
		}

		return len(p), nil
	}), &slog.HandlerOptions{Level: slog.LevelInfo}))
	t.Cleanup(func() { plog.RemoveHandler(t.Name()) })

	return func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()

		out := make(map[string][]string, len(msgs))
		for level, m := range msgs {
			out[level] = slices.Clone(m)
		}

		return out
	}
}

// wantLogged fails the test unless msg was logged at level.
func wantLogged(t *testing.T, logs map[string][]string, level, msg string) {
	t.Helper()

	if !slices.Contains(logs[level], msg) {
		t.Errorf("logged at %s = %q, want %q", level, logs[level], msg)
	}
}

// A request in flight when shutdown begins, and done within the grace period,
// gets its whole response, and the shutdown is logged as complete.
//
//nolint:paralleltest // swaps the job registry
func TestShutdownFinishesRequestsInFlight(t *testing.T) {
	const (
		grace = 2 * time.Second
		body  = "all of it"
	)

	logs := captureLogs(t)
	began := make(chan struct{})

	s := runTestServer(t, grace, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(began)
		time.Sleep(200 * time.Millisecond)

		_, _ = io.WriteString(w, body)
	}))

	type response struct {
		body string
		err  error
	}

	got := make(chan response, 1)

	go func() {
		resp, err := http.Get(s.url)
		if err != nil {
			got <- response{"", err}

			return
		}

		defer resp.Body.Close()

		b, err := io.ReadAll(resp.Body)
		got <- response{string(b), err}
	}()

	<-began

	took, err := s.stop()
	if err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	if took > grace {
		t.Errorf("run() with a request done in 200ms took %s, want less than the %s grace period", took, grace)
	}

	if r := <-got; r.err != nil || r.body != body {
		t.Errorf("GET of a request in flight at shutdown = %q, %v; want %q, nil", r.body, r.err, body)
	}

	wantLogged(t, logs(), "INFO", "shutting down server")
	wantLogged(t, logs(), "INFO", "shutdown complete")
}

// A request still running at the end of the grace period is cut off, run
// returns soon after, and the shutdown says what it cut off.
//
//nolint:paralleltest // swaps the job registry
func TestShutdownCutsOffRequestsPastTheGracePeriod(t *testing.T) {
	const grace = 300 * time.Millisecond

	logs := captureLogs(t)
	began := make(chan struct{})

	s := runTestServer(t, grace, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(began)

		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		}

		_, _ = io.WriteString(w, "too late")
	}))

	failed := make(chan error, 1)

	go func() {
		resp, err := http.Get(s.url)
		if err == nil {
			resp.Body.Close()
		}

		failed <- err
	}()

	<-began

	took, err := s.stop()
	if err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	if took < grace || took > grace+shutdownSlack {
		t.Errorf("run() with a request outlasting the %s grace period took %s, want %s to %s",
			grace, took, grace, grace+shutdownSlack)
	}

	if err := <-failed; err == nil {
		t.Error("GET of a request outlasting the grace period succeeded, want it cut off")
	}

	wantLogged(t, logs(), "WARN", "shutdown grace period ended with work still running")
}

// A grace period of 0 waits for nothing: running jobs are canceled at once.
//
//nolint:paralleltest // swaps the job registry
func TestShutdownWithNoGracePeriod(t *testing.T) {
	s := runTestServer(t, 0, http.NotFoundHandler())

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	slow := startShutdownJob(t, s.jobs, "slow", func(ctx context.Context) error {
		<-ctx.Done()
		<-release

		return ctx.Err()
	})

	took, err := s.stop()
	if err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	if took > shutdownSlack {
		t.Errorf("run() with no grace period took %s, want at most %s", took, shutdownSlack)
	}

	j, _ := s.jobs.Get(slow)
	if !j.CancelRequested {
		t.Error("running job after shutdown with no grace period not canceled, want canceled")
	}
}

// A negative grace period is refused before the server starts.
//
//nolint:paralleltest // sets the server options
func TestStartRefusesNegativeGracePeriod(t *testing.T) {
	saved := o
	t.Cleanup(func() { o = saved })

	err := Start(t.Context(), ServeWithShutdownGracePeriod(-time.Second))
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("Start(grace period -1s) = %v, want an error that it must not be negative", err)
	}
}

// startShutdownJob starts a cancelable job running run, and returns its ID.
func startShutdownJob(t *testing.T, r *job.Registry, title string, run func(context.Context) error) string {
	t.Helper()

	j, err := r.Start(job.Spec{
		Kind: "test", Owner: "alice", Title: title, Cancelable: true,
		Run: func(ctx context.Context, _ *job.Run) error { return run(ctx) },
	})
	if err != nil {
		t.Fatalf("starting job %s: %v", title, err)
	}

	return j.ID
}

// readCloseError reads from conn until it closes, and returns how it closed.
func readCloseError(t *testing.T, conn *websocket.Conn) *websocket.CloseError {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}

		var closed *websocket.CloseError
		if !errors.As(err, &closed) {
			t.Fatalf("reading the websocket after shutdown = %v, want a close frame", err)
		}

		return closed
	}
}

// A golang.org/x/net/websocket socket, as the console, SCORCH, VNC and port
// forwards use, is closed with close code 1001 and no reason.
//
//nolint:paralleltest // swaps the job registry
func TestShutdownClosesXNetWebSocketsGoingAway(t *testing.T) {
	s := runTestServer(t, 2*time.Second, xwebsocket.Handler(func(ws *xwebsocket.Conn) {
		defer lifecycle.TrackConn("test", ws)()

		_, _ = io.Copy(io.Discard, ws)
	}))

	header := http.Header{"Origin": []string{s.url}}

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.url, "http"), header)
	if err != nil {
		t.Fatalf("dialing the x/net websocket: %v", err)
	}

	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	polltest.Until(t, "the handler is tracked", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()

		return lifecycle.Wait(ctx)["test"] == 1
	})

	if _, err := s.stop(); err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	want := &websocket.CloseError{Code: websocket.CloseGoingAway, Text: ""}
	if diff := cmp.Diff(want, readCloseError(t, conn)); diff != "" {
		t.Errorf("x/net websocket close at shutdown mismatch (-want +got):\n%s", diff)
	}
}
