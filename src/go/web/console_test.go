package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"phenix/web/rbac"
)

const consoleTestTimeout = 5 * time.Second

// useTestConsoles runs `cat` as the console, which echoes input and exits on
// CTRL+D, and ends every console the test leaves running.
func useTestConsoles(t *testing.T, grace time.Duration) *consoleRegistry {
	t.Helper()

	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat is not installed")
	}

	original, originalOpts := consoles, o
	registry := newConsoleRegistry(grace, func(ctx context.Context) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "cat"), nil
	})

	consoles = registry
	o.minimegaConsole = true

	t.Cleanup(func() {
		registry.mu.Lock()
		running := make([]*consoleSession, 0, len(registry.sessions))
		for _, console := range registry.sessions {
			running = append(running, console)
		}
		registry.mu.Unlock()

		for _, console := range running {
			console.end()
		}

		consoles, o = original, originalOpts
	})

	return registry
}

// consoleAPI serves the API to user with role, and returns its URL.
func consoleAPI(t *testing.T, user string, role rbac.Role) string {
	t.Helper()

	return serveAs(t, user, role).URL + "/api/v1"
}

// consoleRole may start and use consoles.
func consoleRole() rbac.Role {
	return combinedRole(testRole("miniconsole", "get"), testRole("miniconsole", "post"))
}

// createTestConsole starts a console as the API's user.
func createTestConsole(t *testing.T, api string) int {
	t.Helper()

	resp := do(t, http.MethodPost, api+"/console", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("creating console: got %d", resp.StatusCode)
	}

	var body struct {
		PID int `json:"pid"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	return body.PID
}

func dialTestConsole(t *testing.T, api string, pid int) (*websocket.Conn, error) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(api, "http") + "/console/" + strconv.Itoa(pid) + "/ws"

	ws, err := websocket.Dial(url, "", api)
	if err == nil {
		t.Cleanup(func() { ws.Close() })
	}

	return ws, err
}

// readConsoleUntil reads from ws until its output contains want.
func readConsoleUntil(t *testing.T, ws *websocket.Conn, want string) {
	t.Helper()

	_ = ws.SetReadDeadline(time.Now().Add(consoleTestTimeout))

	var (
		output bytes.Buffer
		buf    = make([]byte, 4096)
	)

	for !strings.Contains(output.String(), want) {
		n, err := ws.Read(buf)
		output.Write(buf[:n])

		if err != nil {
			t.Fatalf("reading console for %q: %v; got %q", want, err, output.String())
		}
	}
}

func waitConsoleEnded(t *testing.T, console *consoleSession) {
	t.Helper()

	select {
	case <-console.done:
	case <-time.After(consoleTestTimeout):
		t.Fatalf("console %d did not end", console.pid)
	}
}

// waitConsoleClients waits until n websockets are attached to the console.
func waitConsoleClients(t *testing.T, console *consoleSession, n int) {
	t.Helper()

	deadline := time.Now().Add(consoleTestTimeout)

	for {
		console.mu.Lock()
		attached := len(console.clients)
		console.mu.Unlock()

		if attached == n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d websockets attached to console %d, want %d", attached, console.pid, n)
		}

		time.Sleep(time.Millisecond)
	}
}

// idleTimer returns what the console's running idle timer passes expire once
// the grace period has passed.
func idleTimer(t *testing.T, console *consoleSession) int {
	t.Helper()

	console.mu.Lock()
	defer console.mu.Unlock()

	if console.idle == nil {
		t.Fatalf("console %d has no idle timer running", console.pid)
	}

	return console.idleGen
}

// echoTestConsole checks the console is still running: cat echoes a line.
func echoTestConsole(t *testing.T, ws *websocket.Conn, line string) {
	t.Helper()

	if _, err := ws.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}

	readConsoleUntil(t, ws, line+"\r\n"+line+"\r\n")
}

func TestConsoleHistory(t *testing.T) {
	history := newConsoleHistory(16)

	history.Write([]byte("one\n"))
	history.Write([]byte("two\n"))

	if got := string(history.Bytes()); got != "one\ntwo\n" {
		t.Fatalf("before filling: got %q", got)
	}

	// fills it exactly: nothing dropped yet
	history.Write([]byte("abcd\n\x1b[1"))

	if got := string(history.Bytes()); got != "one\ntwo\nabcd\n\x1b[1" {
		t.Fatalf("filled: got %q", got)
	}

	// wraps: the oldest output goes, and the replay starts at a line
	history.Write([]byte("mfour\n"))

	if got := string(history.Bytes()); got != "abcd\n\x1b[1mfour\n" {
		t.Fatalf("wrapped: got %q", got)
	}

	// more than it holds at once: only the end is kept
	history.Write([]byte("a very long line\nof output!\n"))

	if got := string(history.Bytes()); got != "of output!\n" {
		t.Fatalf("oversized: got %q", got)
	}
}

func TestConsoleReplaysOutputToAnotherWebSocket(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	first, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Write([]byte("hello console\n")); err != nil {
		t.Fatal(err)
	}

	readConsoleUntil(t, first, "hello console\r\nhello console\r\n")

	// a second tab gets what the console printed before it attached
	second, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	readConsoleUntil(t, second, "hello console\r\nhello console\r\n")

	// and both get new output, whichever sent the input
	if _, err := second.Write([]byte("again\n")); err != nil {
		t.Fatal(err)
	}

	readConsoleUntil(t, first, "again\r\nagain\r\n")
	readConsoleUntil(t, second, "again\r\nagain\r\n")

	// closing them leaves the console, and its output, for the next one
	first.Close()
	second.Close()
	waitConsoleClients(t, console, 0)

	third, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	readConsoleUntil(t, third, "again\r\nagain\r\n")
}

// The grace period is far longer than the test, which fires the idle timers
// itself, as they fire once it has passed.
func TestConsoleEndsOnceUnattachedForTheGracePeriod(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	unattached := idleTimer(t, console)

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	waitConsoleClients(t, console, 1)

	// attached for longer than the grace period: still running
	console.expire(unattached)
	echoTestConsole(t, ws, "attached")

	// a reload: detached, then attached again within the grace period
	ws.Close()
	waitConsoleClients(t, console, 0)

	detached := idleTimer(t, console)

	ws, err = dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	waitConsoleClients(t, console, 1)

	console.expire(detached)
	echoTestConsole(t, ws, "reloaded")

	// the last tab closed
	ws.Close()
	waitConsoleClients(t, console, 0)

	console.expire(idleTimer(t, console))
	waitConsoleEnded(t, console)

	if resp := do(t, http.MethodGet, api+"/console/"+strconv.Itoa(pid), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for the ended console, got %d", resp.StatusCode)
	}
}

// A websocket that stops reading is dropped once its queue of output is full,
// and the others keep getting the console's output.
func TestConsoleDropsASlowWebSocket(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	// nothing writes this client's queue to its websocket, as when the
	// browser behind it stops reading
	slow, err := console.attach()
	if err != nil {
		t.Fatal(err)
	}

	for i := range consoleClientQueue {
		console.broadcast(fmt.Appendf(nil, "line %d\n", i))
	}

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	waitConsoleClients(t, console, 2)
	readConsoleUntil(t, ws, fmt.Sprintf("line %d\n", consoleClientQueue-1))

	echoTestConsole(t, ws, "more output")
	waitConsoleClients(t, console, 1)

	// the dropped websocket is written what was already queued for it, then
	// closed
	queued := 0
	for range slow.out {
		queued++
	}

	if queued != consoleClientQueue {
		t.Fatalf("dropped client had %d chunks queued, want %d", queued, consoleClientQueue)
	}
}

func TestConsoleEndsIfNoWebSocketEverAttaches(t *testing.T) {
	registry := useTestConsoles(t, 100*time.Millisecond)
	pid := createTestConsole(t, consoleAPI(t, "alice", consoleRole()))

	waitConsoleEnded(t, registry.lookup(pid))
}

func TestConsoleEndsWhenItsProcessExits(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatal(err)
	}

	// CTRL+D: cat exits
	if _, err := ws.Write([]byte{4}); err != nil {
		t.Fatal(err)
	}

	waitConsoleEnded(t, console)

	// the websocket closes once the console has ended
	_ = ws.SetReadDeadline(time.Now().Add(consoleTestTimeout))

	buf := make([]byte, 64)
	for {
		if _, err := ws.Read(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("expected the websocket to close, got %v", err)
			}

			break
		}
	}

	if registry.lookup(pid) != nil {
		t.Fatal("ended console still registered")
	}

	if _, err := dialTestConsole(t, api, pid); err == nil {
		t.Fatal("attached to an ended console")
	}
}

func TestDeleteConsoleEndsIt(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	resp := do(t, http.MethodDelete, api+"/console/"+strconv.Itoa(pid), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// ended by the time the request answers
	select {
	case <-console.done:
	default:
		t.Fatal("console still running after DELETE")
	}

	resp = do(t, http.MethodDelete, api+"/console/"+strconv.Itoa(pid), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 deleting it again, got %d", resp.StatusCode)
	}
}

func TestConsoleIsOnlyItsOwners(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)

	var (
		alice = consoleAPI(t, "alice", consoleRole())
		bob   = consoleAPI(t, "bob", consoleRole())
		pid   = createTestConsole(t, alice)
		path  = "/console/" + strconv.Itoa(pid)
	)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, path},
		{http.MethodPost, path + "/size?cols=100&rows=40"},
		{http.MethodDelete, path},
	} {
		if resp := do(t, tt.method, bob+tt.path, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s as another user: expected 404, got %d", tt.method, tt.path, resp.StatusCode)
		}
	}

	if _, err := dialTestConsole(t, bob, pid); err == nil {
		t.Fatal("another user attached to the console")
	}

	if registry.lookup(pid) == nil {
		t.Fatal("another user ended the console")
	}

	if resp := do(t, http.MethodGet, alice+path, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET as the owner: expected 200, got %d", resp.StatusCode)
	}

	if resp := do(t, http.MethodPost, alice+path+"/size?cols=100&rows=40", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("resize as the owner: expected 200, got %d", resp.StatusCode)
	}
}

func TestConsoleRoutesNeedPermission(t *testing.T) {
	useTestConsoles(t, time.Minute)

	pid := createTestConsole(t, consoleAPI(t, "alice", consoleRole()))
	path := "/console/" + strconv.Itoa(pid)

	// may use the console, but not start, resize or end one
	viewer := consoleAPI(t, "alice", testRole("miniconsole", "get"))

	for _, tt := range []struct{ method, path string }{
		{http.MethodPost, "/console"},
		{http.MethodPost, path + "/size?cols=100&rows=40"},
		{http.MethodDelete, path},
	} {
		if resp := do(t, tt.method, viewer+tt.path, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s: expected 403, got %d", tt.method, tt.path, resp.StatusCode)
		}
	}

	none := consoleAPI(t, "alice", testRole("experiments", "list"))

	if resp := do(t, http.MethodGet, none+path, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET: expected 403, got %d", resp.StatusCode)
	}

	if _, err := dialTestConsole(t, none, pid); err == nil {
		t.Fatal("attached without permission")
	}
}

func TestCreateConsoleNeedsTheConsoleEnabled(t *testing.T) {
	useTestConsoles(t, time.Minute)

	o.minimegaConsole = false

	resp := do(t, http.MethodPost, consoleAPI(t, "alice", consoleRole())+"/console", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}
