package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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
	"phenix/web/broker"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
)

// shutdownSlack is how long past its grace period shutting down may take.
const shutdownSlack = time.Second

// testServer is a uiServer a test runs on 127.0.0.1, with the broker running
// and a job registry of the test's own.
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

	background, stopBackground := context.WithCancel(context.Background())
	brokerStopped := make(chan struct{})

	go func() {
		defer close(brokerStopped)

		broker.Start(background)
	}()

	server := &uiServer{
		grace:          grace,
		servers:        []servedHTTP{{name: "HTTP server", srv: newHTTPServer("", handler), ln: ln, serve: (*http.Server).Serve}},
		socket:         "",
		stopBackground: stopBackground,
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	s := &testServer{url: "http://" + ln.Addr().String(), jobs: useJobs(t), cancel: cancel, result: make(chan error, 1)}

	go func() { s.result <- server.run(ctx) }()

	t.Cleanup(func() {
		s.stop()
		stopBackground()
		<-brokerStopped
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
//nolint:paralleltest // runs the broker and swaps the job registry
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
//nolint:paralleltest // runs the broker and swaps the job registry
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

// Every broker websocket client is closed with close code 1001 and the
// shutdown reason, which the UI tells apart from a network failure.
//
//nolint:paralleltest // runs the broker and swaps the job registry
func TestShutdownClosesWebSocketsGoingAway(t *testing.T) {
	s := runTestServer(t, 2*time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, testRole("experiments", "list"))
		broker.ServeWS(w, r.WithContext(ctx))
	}))

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.url, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the broker: %v", err)
	}

	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	polltest.Until(t, "the broker has the client", func() bool { return broker.CurrentStats().Clients == 1 })

	if _, err := s.stop(); err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var closed *websocket.CloseError

	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}

	if !errors.As(err, &closed) {
		t.Fatalf("reading the broker websocket after shutdown = %v, want a close frame", err)
	}

	want := &websocket.CloseError{Code: websocket.CloseGoingAway, Text: "server shutting down"}
	if diff := cmp.Diff(want, closed); diff != "" {
		t.Errorf("broker websocket close at shutdown mismatch (-want +got):\n%s", diff)
	}
}

// Running jobs get half the grace period to finish; the rest are canceled
// then, and a minimega console's process is ended at once.
//
//nolint:paralleltest // runs the broker and swaps the job registry and consoles
func TestShutdownGivesJobsHalfTheGracePeriod(t *testing.T) {
	// the quick job has 650ms to spare before half the grace period
	const grace = 1500 * time.Millisecond

	registry := useTestConsoles(t, time.Minute)
	s := runTestServer(t, grace, http.NotFoundHandler())

	console, err := registry.start(context.Background(), "alice")
	if err != nil {
		t.Fatalf("starting a console: %v", err)
	}

	quick := startShutdownJob(t, s.jobs, "quick", func(ctx context.Context) error {
		select {
		case <-time.After(100 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	var canceledAfter time.Duration

	began := time.Now()
	slow := startShutdownJob(t, s.jobs, "slow", func(ctx context.Context) error {
		<-ctx.Done()
		canceledAfter = time.Since(began)

		return ctx.Err()
	})

	took, err := s.stop()
	if err != nil {
		t.Errorf("run() after SIGTERM = %v, want nil", err)
	}

	if took > grace+shutdownSlack {
		t.Errorf("run() took %s, want at most %s", took, grace+shutdownSlack)
	}

	got := map[string]job.Status{}

	for name, id := range map[string]string{"quick": quick, "slow": slow} {
		j, _ := s.jobs.Get(id)
		got[name] = j.Status
	}

	want := map[string]job.Status{"quick": job.StatusSucceeded, "slow": job.StatusCanceled}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("job statuses after shutdown mismatch (-want +got):\n%s", diff)
	}

	if canceledAfter < grace/2 {
		t.Errorf("slow job canceled %s after shutdown began, want at least half the %s grace period", canceledAfter, grace)
	}

	if !isDone(console.done) {
		t.Error("minimega console still running after shutdown, want it ended")
	}
}

// A grace period of 0 waits for nothing: running jobs are canceled at once.
//
//nolint:paralleltest // runs the broker and swaps the job registry
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
//nolint:paralleltest // runs the broker and swaps the job registry
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

// Once the server is shutting down, a new broker websocket, a console and a
// job are refused with 503, Retry-After and the same message in a JSON Error.
//
//nolint:paralleltest // stops the server's lifecycle and swaps the consoles and jobs
func TestRefusedWhileStopping(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	jobs := useJobs(t)

	lifecycle.Start()
	t.Cleanup(lifecycle.Start)

	lifecycle.Stop()

	type answer struct {
		Status     int
		RetryAfter string
		Body       string
	}

	var (
		want     = answer{http.StatusServiceUnavailable, lifecycle.RetryAfter, lifecycle.Unavailable}
		answerOf = func(status int, header http.Header, body string) answer {
			return answer{status, header.Get("Retry-After"), errorMessage(t, status, body)}
		}
	)

	resp := do(t, http.MethodPost, api+"/console", "")
	if diff := cmp.Diff(want, answerOf(resp.StatusCode, resp.Header, readBody(t, resp))); diff != "" {
		t.Errorf("POST /console while stopping mismatch (-want +got):\n%s", diff)
	}

	ws := httptest.NewRecorder()
	broker.ServeWS(ws, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws", nil))

	if diff := cmp.Diff(want, answerOf(ws.Code, ws.Header(), ws.Body.String())); diff != "" {
		t.Errorf("GET /ws while stopping mismatch (-want +got):\n%s", diff)
	}

	jobs.ShutdownContext(context.Background(), 0)

	started := httptest.NewRecorder()
	serveStartJob(started, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/jobs", nil), job.Spec{
		Kind: "test", Run: func(context.Context, *job.Run) error { return nil },
	})

	if diff := cmp.Diff(want, answerOf(started.Code, started.Header(), started.Body.String())); diff != "" {
		t.Errorf("starting a job while stopping mismatch (-want +got):\n%s", diff)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	if n := len(registry.sessions); n != 0 {
		t.Errorf("consoles after a refused start = %d, want none", n)
	}
}

// A console started once endAll has run is ended at once, so none outlives
// the shutdown.
//
//nolint:paralleltest // swaps the consoles
func TestConsoleStartAfterEndAllIsRefused(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if left := registry.endAll(ctx, consoleEndedShutdown); left != 0 {
		t.Errorf("endAll() with no consoles = %d left, want 0", left)
	}

	if _, err := registry.start(context.Background(), "alice"); !errors.Is(err, errServerStopping) {
		t.Errorf("start() after endAll() error = %v, want %v", err, errServerStopping)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	if n := len(registry.sessions); n != 0 {
		t.Errorf("consoles after start() following endAll() = %d, want none", n)
	}
}
