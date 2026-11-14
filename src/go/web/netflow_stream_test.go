package web

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/mm"
	"phenix/web/lifecycle"
	"phenix/web/rbac"
)

// netflowTestExp is the experiment the netflow capture tests capture.
const netflowTestExp = "netflow-exp"

// netflowTestLine is a netflow record as minimega sends it, and netflowTestFlow
// the flow the websocket sends for it.
const netflowTestLine = "0 0 6 10.0.0.1:1234 <-> 10.0.0.2:443 2 2048\n"

//nolint:gochecknoglobals // constant test value
var netflowTestFlow = map[string]any{
	"proto": float64(6), "src": "10.0.0.1", "sport": float64(1234), "dst": "10.0.0.2", "dport": float64(443),
	"packets": float64(2), "bytes": float64(2048),
}

// netflowMM is a minimega whose headnode is "head", recording each mesh
// command as "<namespace> <node>: <command>" and failing it with fail.
type netflowMM struct {
	mm.MM

	mu   sync.Mutex
	sent []string
	fail error
}

func (*netflowMM) Headnode() string { return "head" }

func (m *netflowMM) MeshSend(ns, node, cmd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sent = append(m.sent, ns+" "+node+": "+cmd)

	return m.fail
}

// take returns the mesh commands sent since the last take.
func (m *netflowMM) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	sent := m.sent
	m.sent = nil

	return sent
}

func (m *netflowMM) failWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fail = err
}

// useNetflowExperiment stores netflowTestExp, on bridge and scheduled on
// node1, running unless stopped, and a minimega for its captures. Captures
// are not recorded, and one still running stops when the test ends.
func useNetflowExperiment(t *testing.T, bridge string, running bool) *netflowMM {
	t.Helper()

	c, err := store.NewConfig("experiment/" + netflowTestExp)
	if err != nil {
		t.Fatalf("store.NewConfig(experiment/%s) error = %v", netflowTestExp, err)
	}

	c.Spec = map[string]any{
		"experimentName": netflowTestExp, "defaultBridge": bridge, "baseDir": t.TempDir(),
		"topology": map[string]any{"nodes": []any{}},
	}

	if running {
		c.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "schedules": map[string]any{"vm": "node1"}}
	}

	useTestStore(t, *c)

	fake := useNetflowMM(t)

	saved := o
	o.netflowMaxSize = 0

	t.Cleanup(func() { o = saved })

	return fake
}

// useNetflowMM installs a netflowMM, and stops netflowTestExp's capture,
// if any, when the test ends.
func useNetflowMM(t *testing.T) *netflowMM {
	t.Helper()

	fake := new(netflowMM)

	original := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() {
		fake.failWith(nil)

		_ = experiment.StopNetflow(netflowTestExp)

		mm.DefaultMM = original //nolint:reassign // restore test double
	})

	return fake
}

// netflowRole may start, read and stop netflowTestExp's capture.
func netflowRole() rbac.Role {
	return combinedRole(
		testRole("experiments/netflow", "create", netflowTestExp),
		testRole("experiments/netflow", "get", netflowTestExp),
		testRole("experiments/netflow", "delete", netflowTestExp),
	)
}

// dialNetflow opens the capture's websocket at url, an http one.
func dialNetflow(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(url, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dialing %s/ws: %v", url, err)
	}

	_ = resp.Body.Close()

	t.Cleanup(func() { _ = conn.Close() })

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	return conn
}

// sendNetflowUntil sends netflowTestLine to the capture's port every 10 ms,
// as minimega would, until the returned function is called. The websocket
// gets only the records sent once it is subscribed, so a test sends until one
// arrives.
func sendNetflowUntil(t *testing.T, port int) func() {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dialing the netflow capture's UDP port %d: %v", port, err)
	}

	var (
		stop = make(chan struct{})
		done = make(chan struct{})
	)

	go func() {
		defer close(done)

		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			_, _ = conn.Write([]byte(netflowTestLine))

			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()

	stopped := sync.OnceFunc(func() {
		close(stop)
		<-done

		_ = conn.Close()
	})

	t.Cleanup(stopped)

	return stopped
}

// readUntilClosed reads the websocket's messages until it closes, and returns
// how; each message before must be netflowTestFlow.
func readUntilClosed(t *testing.T, conn *websocket.Conn) *websocket.CloseError {
	t.Helper()

	for {
		var flow map[string]any

		err := conn.ReadJSON(&flow)
		if err == nil {
			if diff := cmp.Diff(netflowTestFlow, flow); diff != "" {
				t.Errorf("netflow websocket message mismatch (-want +got):\n%s", diff)
			}

			continue
		}

		var closed *websocket.CloseError
		if !errors.As(err, &closed) {
			t.Fatalf("reading the netflow websocket = %v, want a close message", err)
		}

		return closed
	}
}

// netflowPort returns the UDP port netflowTestExp's running capture listens on.
func netflowPort(t *testing.T) int {
	t.Helper()

	flow := experiment.GetNetflow(netflowTestExp)
	if flow == nil {
		t.Fatalf("GetNetflow(%s) = nil, want the running capture", netflowTestExp)
	}

	addr, ok := flow.Conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("netflow capture address = %v, want a UDP address", flow.Conn.LocalAddr())
	}

	return addr.Port
}

// A started capture asks every node to send the bridge's netflow to the
// headnode, streams each flow on the websocket, and once stopped tells the
// nodes and closes the websocket normally.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestNetflowStreamsFlowsUntilStopped(t *testing.T) {
	fake := useNetflowExperiment(t, "br0", true)
	url := serveAs(t, "alice", netflowRole()).URL + "/api/v1/experiments/" + netflowTestExp + "/netflow"

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s = %d %s, want 204", url, resp.StatusCode, readBody(t, resp))
	}

	port := netflowPort(t)

	wantStart := []string{
		netflowTestExp + " head: capture netflow mode ascii",
		netflowTestExp + " node1: capture netflow bridge br0 udp head:" + strconv.Itoa(port),
	}

	if diff := cmp.Diff(wantStart, fake.take()); diff != "" {
		t.Errorf("POST %s mesh commands mismatch (-want +got):\n%s", url, diff)
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("GET %s with a running capture = %d, want 204", url, resp.StatusCode)
	}

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("POST %s with a running capture = %d, want 409", url, resp.StatusCode)
	}

	conn := dialNetflow(t, url)
	stopSending := sendNetflowUntil(t, port)

	var flow map[string]any
	if err := conn.ReadJSON(&flow); err != nil {
		t.Fatalf("reading a flow from %s/ws: %v", url, err)
	}

	stopSending()

	if diff := cmp.Diff(netflowTestFlow, flow); diff != "" {
		t.Errorf("%s/ws flow mismatch (-want +got):\n%s", url, diff)
	}

	if resp := do(t, http.MethodDelete, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d %s, want 204", url, resp.StatusCode, readBody(t, resp))
	}

	if diff := cmp.Diff([]string{netflowTestExp + " node1: capture netflow delete bridge br0"}, fake.take()); diff != "" {
		t.Errorf("DELETE %s mesh commands mismatch (-want +got):\n%s", url, diff)
	}

	if closed := readUntilClosed(t, conn); closed.Code != websocket.CloseNormalClosure || closed.Text != "netflow stopped" {
		t.Errorf("%s/ws after the capture stopped closed with %d %q, want %d %q", url, closed.Code, closed.Text,
			websocket.CloseNormalClosure, "netflow stopped")
	}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if resp := do(t, method, url, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s after the capture stopped = %d, want 404", method, url, resp.StatusCode)
		}
	}
}

// A request without a websocket handshake gets 400. A client that closes
// the websocket gets the close answered, and the capture runs on for other
// clients.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestNetflowWebSocketClientCloses(t *testing.T) {
	useNetflowExperiment(t, "br0", true)
	url := serveAs(t, "alice", netflowRole()).URL + "/api/v1/experiments/" + netflowTestExp + "/netflow"

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s = %d %s, want 204", url, resp.StatusCode, readBody(t, resp))
	}

	if resp := do(t, http.MethodGet, url+"/ws", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET %s/ws without a websocket handshake = %d, want 400", url, resp.StatusCode)
	}

	first, second := dialNetflow(t, url), dialNetflow(t, url)

	msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye")
	if err := first.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("closing %s/ws: %v", url, err)
	}

	if closed := readUntilClosed(t, first); closed.Code != websocket.CloseNormalClosure {
		t.Errorf("%s/ws answered the client's close with %d %q, want %d", url, closed.Code, closed.Text,
			websocket.CloseNormalClosure)
	}

	stopSending := sendNetflowUntil(t, netflowPort(t))

	var flow map[string]any
	if err := second.ReadJSON(&flow); err != nil {
		t.Fatalf("reading a flow from the other client's %s/ws: %v", url, err)
	}

	stopSending()

	if diff := cmp.Diff(netflowTestFlow, flow); diff != "" {
		t.Errorf("the other client's %s/ws flow mismatch (-want +got):\n%s", url, diff)
	}
}

// Once the server starts shutting down, an open netflow websocket closes
// going away, and a new one is refused with 503.
//
//nolint:paralleltest // replaces the store, minimega, the server options, the netflow registry and the lifecycle
func TestNetflowWebSocketAtShutdown(t *testing.T) {
	lifecycle.Start()
	t.Cleanup(lifecycle.Start)

	useNetflowExperiment(t, "br0", true)
	url := serveAs(t, "alice", netflowRole()).URL + "/api/v1/experiments/" + netflowTestExp + "/netflow"

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s = %d %s, want 204", url, resp.StatusCode, readBody(t, resp))
	}

	conn := dialNetflow(t, url)

	lifecycle.Stop()

	if closed := readUntilClosed(t, conn); closed.Code != websocket.CloseGoingAway || closed.Text != lifecycle.CloseReason {
		t.Errorf("%s/ws at shutdown closed with %d %q, want %d %q", url, closed.Code, closed.Text,
			websocket.CloseGoingAway, lifecycle.CloseReason)
	}

	ws := "ws" + strings.TrimPrefix(url, "http") + "/ws"

	_, resp, err := websocket.DefaultDialer.DialContext(t.Context(), ws, nil)
	if err == nil || resp == nil {
		t.Fatalf("dialing %s at shutdown error = %v, want a refused handshake", ws, err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("dialing %s at shutdown = %d, want %d", ws, resp.StatusCode, http.StatusServiceUnavailable)
	}
}

// Starting a capture fails with the status of why: an unknown or stopped
// experiment, the default phenix bridge, or a node that refuses it. A
// failed start leaves no capture running.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestStartNetflowFails(t *testing.T) {
	tests := map[string]struct {
		exp      string
		bridge   string
		stopped  bool
		meshFail bool
		status   int
		message  string
		commands int
	}{
		"unknown experiment": {
			exp: "missing", bridge: "br0", status: http.StatusNotFound, message: "unable to find experiment",
		},
		"stopped experiment": {
			bridge: "br0", stopped: true, status: http.StatusConflict, message: "cannot start netflow on stopped experiment",
		},
		"phenix bridge": {
			bridge: "phenix", status: http.StatusConflict,
			message: "cannot start netflow on experiment with default bridge set to 'phenix'",
		},
		"node refuses": {
			bridge: "br0", meshFail: true, status: http.StatusInternalServerError,
			message: "unable to start netflow capture", commands: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := useNetflowExperiment(t, tc.bridge, !tc.stopped)
			if tc.meshFail {
				fake.failWith(errors.New("mesh send failed"))
			}

			exp := netflowTestExp
			if tc.exp != "" {
				exp = tc.exp
			}

			role := combinedRole(netflowRole(), testRole("experiments/netflow", "create", exp))
			url := serveAs(t, "alice", role).URL + "/api/v1/experiments/" + exp + "/netflow"

			resp := do(t, http.MethodPost, url, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.status {
				t.Fatalf("POST %s = %d %s, want %d", url, resp.StatusCode, body, tc.status)
			}

			if got := errorMessage(t, tc.status, body); got != tc.message {
				t.Errorf("POST %s error message = %q, want %q", url, got, tc.message)
			}

			if got := len(fake.take()); got != tc.commands {
				t.Errorf("POST %s sent %d mesh commands, want %d", url, got, tc.commands)
			}

			if flow := experiment.GetNetflow(netflowTestExp); flow != nil {
				t.Errorf("GetNetflow(%s) after a failed start = %v, want nil", netflowTestExp, flow)
			}
		})
	}
}

// A capture a node fails to stop runs on, and a later stop stops it.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestStopNetflowFailureKeepsTheCapture(t *testing.T) {
	fake := useNetflowExperiment(t, "br0", true)
	url := serveAs(t, "alice", netflowRole()).URL + "/api/v1/experiments/" + netflowTestExp + "/netflow"

	if resp := do(t, http.MethodPost, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST %s = %d %s, want 204", url, resp.StatusCode, readBody(t, resp))
	}

	port := netflowPort(t)

	fake.failWith(errors.New("mesh send failed"))

	resp := do(t, http.MethodDelete, url, "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusInternalServerError ||
		errorMessage(t, http.StatusInternalServerError, body) != "unable to stop netflow capture" {
		t.Errorf("DELETE %s with a failing node = %d %s, want 500 unable to stop netflow capture", url, resp.StatusCode, body)
	}

	if resp := do(t, http.MethodGet, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("GET %s after a failed stop = %d, want 204", url, resp.StatusCode)
	}

	fake.failWith(nil)

	if resp := do(t, http.MethodDelete, url, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE %s after the node recovered = %d, want 204", url, resp.StatusCode)
	}

	want := []string{
		netflowTestExp + " head: capture netflow mode ascii",
		netflowTestExp + " node1: capture netflow bridge br0 udp head:" + strconv.Itoa(port),
		netflowTestExp + " node1: capture netflow delete bridge br0",
		netflowTestExp + " node1: capture netflow delete bridge br0",
	}

	if diff := cmp.Diff(want, fake.take()); diff != "" {
		t.Errorf("mesh commands mismatch (-want +got):\n%s", diff)
	}

	if flow := experiment.GetNetflow(netflowTestExp); flow != nil {
		t.Errorf("GetNetflow(%s) after the stop = %v, want nil", netflowTestExp, flow)
	}
}

// Every netflow route refuses a role without the permission, and the
// websocket answers 404 with no capture running.
//
//nolint:paralleltest // replaces the store, minimega, the server options and the netflow registry
func TestNetflowRoutesRefuse(t *testing.T) {
	useNetflowExperiment(t, "br0", true)

	var (
		path    = "/api/v1/experiments/" + netflowTestExp + "/netflow"
		allowed = serveAs(t, "alice", netflowRole()).URL + path
		other   = serveAs(t, "bob", combinedRole(
			testRole("experiments/netflow", "create", "other"),
			testRole("experiments/netflow", "get", "other"),
			testRole("experiments/netflow", "delete", "other"),
		)).URL + path
	)

	tests := []struct {
		method, url string
		want        int
	}{
		{http.MethodGet, other, http.StatusForbidden},
		{http.MethodPost, other, http.StatusForbidden},
		{http.MethodDelete, other, http.StatusForbidden},
		{http.MethodGet, other + "/ws", http.StatusForbidden},
		{http.MethodGet, allowed + "/ws", http.StatusNotFound},
		{http.MethodGet, allowed, http.StatusNotFound},
	}

	for _, tc := range tests {
		if resp := do(t, tc.method, tc.url, ""); resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.url, resp.StatusCode, tc.want)
		}
	}

	if flow := experiment.GetNetflow(netflowTestExp); flow != nil {
		t.Errorf("GetNetflow(%s) after refused requests = %v, want nil", netflowTestExp, flow)
	}
}
