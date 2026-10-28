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
	"pgregory.net/rapid"

	"phenix/util/polltest"
	"phenix/util/testenv"
	"phenix/web/rbac"
)

const consoleTestTimeout = 5 * time.Second

// useTestConsoles runs `cat` as the console, which echoes input and exits on
// CTRL+D, and ends every console the test leaves running.
func useTestConsoles(t *testing.T, grace time.Duration) *consoleRegistry {
	t.Helper()

	testenv.RequireCommands(t, "cat")

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
			console.end(consoleEndedByUser)
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
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "application/json" {
		t.Fatalf("POST /console = %d %s, want %d application/json", resp.StatusCode, ct, http.StatusOK)
	}

	var body struct {
		PID int `json:"pid"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding POST /console: %v", err)
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
			t.Fatalf("reading the console for %q = %v, having read %q", want, err, output.String())
		}
	}
}

func waitConsoleEnded(t *testing.T, console *consoleSession) {
	t.Helper()

	select {
	case <-console.done:
	case <-time.After(consoleTestTimeout):
		t.Fatalf("console %d did not end within %v", console.pid, consoleTestTimeout)
	}
}

// waitConsoleClients waits until n websockets are attached to the console.
func waitConsoleClients(t *testing.T, console *consoleSession, n int) {
	t.Helper()

	polltest.Until(t, fmt.Sprintf("%d websockets attached to console %d", n, console.pid), func() bool {
		console.mu.Lock()
		defer console.mu.Unlock()

		return len(console.clients) == n
	})
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
		t.Fatalf("writing %q to the console: %v", line, err)
	}

	readConsoleUntil(t, ws, line+"\r\n"+line+"\r\n")
}

func TestConsoleHistory(t *testing.T) {
	history := newConsoleHistory(16)

	// each write in turn, and what the 16-byte history then replays
	for _, step := range []struct{ write, want string }{
		{"one\n", "one\n"},
		{"two\n", "one\ntwo\n"},
		// fills it exactly: nothing dropped yet
		{"abcd\n\x1b[1", "one\ntwo\nabcd\n\x1b[1"},
		// wraps: the oldest output goes, and the replay starts at a line
		{"mfour\n", "abcd\n\x1b[1mfour\n"},
		// more than it holds at once: only the end is kept
		{"a very long line\nof output!\n", "of output!\n"},
	} {
		history.Write([]byte(step.write))

		if got := string(history.Bytes()); got != step.want {
			t.Errorf("after Write(%q), Bytes() = %q, want %q", step.write, got, step.want)
		}
	}
}

// consoleHistoryModel is what a size-byte history replays of all the output written to it.
func consoleHistoryModel(all []byte, size int) []byte {
	if len(all) <= size {
		return all
	}

	kept := all[len(all)-size:]
	if i := bytes.IndexByte(kept, '\n'); i >= 0 {
		kept = kept[i+1:]
	}

	return kept
}

func TestConsoleHistoryProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(1, 24).Draw(t, "size")
		writes := rapid.SliceOfN(rapid.SliceOfN(rapid.SampledFrom([]byte("ab\n")), 0, 3*size), 1, 12).Draw(t, "writes")

		history := newConsoleHistory(size)

		all := make([]byte, 0, len(writes)*3*size)

		for _, output := range writes {
			history.Write(output)
			all = append(all, output...)

			if got, want := history.Bytes(), consoleHistoryModel(all, size); !bytes.Equal(got, want) {
				t.Fatalf("newConsoleHistory(%d) after writes %q: Bytes() = %q, want %q", size, writes, got, want)
			}
		}
	})
}

func TestConsoleReplaysOutputToAnotherWebSocket(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	first, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d: %v", pid, err)
	}

	if _, err := first.Write([]byte("hello console\n")); err != nil {
		t.Fatalf("writing to console %d: %v", pid, err)
	}

	readConsoleUntil(t, first, "hello console\r\nhello console\r\n")

	// a second tab gets what the console printed before it attached
	second, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d a second time: %v", pid, err)
	}

	readConsoleUntil(t, second, "hello console\r\nhello console\r\n")

	// and both get new output, whichever sent the input
	if _, err := second.Write([]byte("again\n")); err != nil {
		t.Fatalf("writing to console %d from the second websocket: %v", pid, err)
	}

	readConsoleUntil(t, first, "again\r\nagain\r\n")
	readConsoleUntil(t, second, "again\r\nagain\r\n")

	// closing them leaves the console, and its output, for the next one
	first.Close()
	second.Close()
	waitConsoleClients(t, console, 0)

	third, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d a third time: %v", pid, err)
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
	events := captureAudit(t)

	unattached := idleTimer(t, console)

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d: %v", pid, err)
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
		t.Fatalf("dialing console %d again: %v", pid, err)
	}

	waitConsoleClients(t, console, 1)

	console.expire(detached)
	echoTestConsole(t, ws, "reloaded")

	// the last tab closed
	ws.Close()
	waitConsoleClients(t, console, 0)

	console.expire(idleTimer(t, console))
	waitConsoleEnded(t, console)

	wantAudit(t, events(), "console-end", "success", map[string]any{
		"user.name": "alice", "process.pid": pid, "phenix.console.end_reason": "idle",
	})

	if n := countAudit(events(), "console-attach", "success", map[string]any{"process.pid": pid}); n != 2 {
		t.Errorf("console-attach success audit events = %d, want 2", n)
	}

	if resp := do(t, http.MethodGet, api+"/console/"+strconv.Itoa(pid), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /console/%d after it ended = %d, want %d", pid, resp.StatusCode, http.StatusNotFound)
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
		t.Fatalf("attaching to console %d: %v", pid, err)
	}

	for i := range consoleClientQueue {
		console.broadcast(fmt.Appendf(nil, "line %d\n", i))
	}

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d: %v", pid, err)
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
		t.Errorf("dropped client's queued chunks = %d, want %d", queued, consoleClientQueue)
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
	events := captureAudit(t)

	ws, err := dialTestConsole(t, api, pid)
	if err != nil {
		t.Fatalf("dialing console %d: %v", pid, err)
	}

	// CTRL+D: cat exits
	if _, err := ws.Write([]byte{4}); err != nil {
		t.Fatalf("writing CTRL+D to console %d: %v", pid, err)
	}

	waitConsoleEnded(t, console)

	// the websocket closes once the console has ended
	_ = ws.SetReadDeadline(time.Now().Add(consoleTestTimeout))

	buf := make([]byte, 64)
	for {
		if _, err := ws.Read(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("reading the websocket of ended console %d = %v, want %v", pid, err, io.EOF)
			}

			break
		}
	}

	if registry.lookup(pid) != nil {
		t.Errorf("lookup(%d) of the ended console = registered, want nil", pid)
	}

	got := events()
	wantAudit(t, got, "console-attach", "success", map[string]any{"user.name": "alice", "process.pid": pid})
	wantAudit(t, got, "console-end", "success", map[string]any{
		"user.name": "alice", "process.pid": pid, "phenix.console.end_reason": "exited",
	})

	if _, err := dialTestConsole(t, api, pid); err == nil {
		t.Errorf("dialing ended console %d = nil error, want it refused", pid)
	}
}

func TestDeleteConsoleEndsIt(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)
	api := consoleAPI(t, "alice", consoleRole())
	events := captureAudit(t)
	pid := createTestConsole(t, api)
	console := registry.lookup(pid)

	resp := do(t, http.MethodDelete, api+"/console/"+strconv.Itoa(pid), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /console/%d = %d, want %d", pid, resp.StatusCode, http.StatusNoContent)
	}

	// ended by the time the request answers
	select {
	case <-console.done:
	default:
		t.Errorf("console %d still running after DELETE answered", pid)
	}

	got := events()
	wantAudit(t, got, "console-create", "success", map[string]any{"user.name": "alice", "process.pid": pid})
	wantAudit(t, got, "console-terminate", "success", map[string]any{"user.name": "alice", "process.pid": pid})
	wantAudit(t, got, "console-end", "success", map[string]any{
		"user.name": "alice", "process.pid": pid, "phenix.console.end_reason": "user",
	})

	resp = do(t, http.MethodDelete, api+"/console/"+strconv.Itoa(pid), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE /console/%d again = %d, want %d", pid, resp.StatusCode, http.StatusNotFound)
	}
}

func TestConsoleIsOnlyItsOwners(t *testing.T) {
	registry := useTestConsoles(t, time.Minute)

	var (
		alice  = consoleAPI(t, "alice", consoleRole())
		bob    = consoleAPI(t, "bob", consoleRole())
		pid    = createTestConsole(t, alice)
		path   = "/console/" + strconv.Itoa(pid)
		events = captureAudit(t)
	)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, path},
		{http.MethodPost, path + "/size?cols=100&rows=40"},
		{http.MethodDelete, path},
	} {
		if resp := do(t, tt.method, bob+tt.path, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as bob = %d, want %d", tt.method, tt.path, resp.StatusCode, http.StatusNotFound)
		}
	}

	if _, err := dialTestConsole(t, bob, pid); err == nil {
		t.Errorf("dialing alice's console %d as bob = nil error, want it refused", pid)
	}

	if registry.lookup(pid) == nil {
		t.Fatalf("lookup(%d) after bob's requests = nil, want alice's console running", pid)
	}

	for _, action := range []string{"console-terminate", "console-attach"} {
		wantAudit(t, events(), action, "failure", map[string]any{
			"user.name": "bob", "process.pid": pid, "event.reason": "not owner",
		})
	}

	resp := do(t, http.MethodGet, alice+path, "")
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "application/json" {
		t.Errorf("GET %s as alice = %d %s, want %d application/json", path, resp.StatusCode, ct, http.StatusOK)
	}

	if resp := do(t, http.MethodPost, alice+path+"/size?cols=100&rows=40", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("POST %s/size as alice = %d, want %d", path, resp.StatusCode, http.StatusOK)
	}
}

func TestConsoleRoutesNeedPermission(t *testing.T) {
	useTestConsoles(t, time.Minute)

	pid := createTestConsole(t, consoleAPI(t, "alice", consoleRole()))
	path := "/console/" + strconv.Itoa(pid)

	// may use the console, but not start, resize or end one
	viewer := consoleAPI(t, "alice", testRole("miniconsole", "get"))
	events := captureAudit(t)

	for _, tt := range []struct{ method, path string }{
		{http.MethodPost, "/console"},
		{http.MethodPost, path + "/size?cols=100&rows=40"},
		{http.MethodDelete, path},
	} {
		if resp := do(t, tt.method, viewer+tt.path, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a console viewer = %d, want %d", tt.method, tt.path, resp.StatusCode, http.StatusForbidden)
		}
	}

	none := consoleAPI(t, "alice", testRole("experiments", "list"))

	if resp := do(t, http.MethodGet, none+path, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET %s without console permission = %d, want %d", path, resp.StatusCode, http.StatusForbidden)
	}

	if _, err := dialTestConsole(t, none, pid); err == nil {
		t.Errorf("dialing console %d without console permission = nil error, want it refused", pid)
	}

	for _, action := range []string{"console-create", "console-terminate"} {
		wantDenied(t, events(), action)
	}

	// the status check, resize and websocket refusals
	for _, route := range []string{path, path + "/size", path + "/ws"} {
		wantAudit(t, events(), "console-attach", "failure", map[string]any{
			"event.reason": "forbidden", "url.path": "/api/v1" + route,
		})
	}
}

func TestCreateConsoleNeedsTheConsoleEnabled(t *testing.T) {
	useTestConsoles(t, time.Minute)

	o.minimegaConsole = false

	resp := do(t, http.MethodPost, consoleAPI(t, "alice", consoleRole())+"/console", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /console with the console disabled = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}
