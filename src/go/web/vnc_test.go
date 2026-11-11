package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
	"pgregory.net/rapid"

	"phenix/util/mm"
	"phenix/util/plog/plogtest"
	"phenix/web/audit"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// vncPath is the VNC session route of test-vm in test-experiment.
const vncPath = "/api/v1/experiments/test-experiment/vms/test-vm/vnc"

// testVNCInfo is what minimega gives for test-vm, unless a test says otherwise.
var testVNCInfo = mm.VNCInfo{Host: "compute-04", Port: 43691, VGA: "std"} //nolint:gochecknoglobals // test value

// getVNCSession stores test-experiment with test-vm, whose vncBanner is banner (none if nil), makes minimega give
// info for test-vm, and gets its VNC session through the router as a permitted role.
func getVNCSession(t *testing.T, banner any, info mm.VNCInfo) (*http.Response, string) {
	t.Helper()

	node := vncNode("test-vm")
	if banner != nil {
		node["annotations"] = map[string]any{"vncBanner": banner}
	}

	useTestStore(t, testExperiment(t, "", node))
	useTestMM(t, &testMM{vnc: info})

	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm"))
	resp := do(t, http.MethodGet, server.URL+vncPath, "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET vms/test-vm/vnc = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodGet, vncPath, resp)

	return resp, readBody(t, resp)
}

// decodeVNCSession decodes a VNC session body.
func decodeVNCSession(t *testing.T, body string) vncSession {
	t.Helper()

	var session vncSession
	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatalf("GET vms/test-vm/vnc body %q is not a VNC session: %v", body, err)
	}

	return session
}

// The VNC session is JSON with the experiment, the VM, the websocket path, the banners, the display and the
// clipboard mode, and it is not cached.
func TestGetVNCSessionShape(t *testing.T) {
	resp, body := getVNCSession(t, nil, testVNCInfo)

	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("GET vms/test-vm/vnc body %q is not JSON: %v", body, err)
	}

	banner := map[string]any{
		"lines":           []any{"EXP: test-experiment - VM: test-vm"},
		"backgroundColor": "white",
		"textColor":       "black",
	}
	want := map[string]any{
		"experiment": "test-experiment",
		"vm":         "test-vm",
		"websocket":  "experiments/test-experiment/vms/test-vm/vnc/ws",
		"banners":    map[string]any{"top": banner, "bottom": banner},
		"display":    map[string]any{"vga": "std", "remoteResize": false},
		"clipboard":  map[string]any{"mode": "pasteAsKeys"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET vms/test-vm/vnc mismatch (-want +got):\n%s", diff)
	}

	wantHeaders := map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "no-cache, no-store, must-revalidate",
		"Pragma":        "no-cache",
		"Expires":       "0",
	}

	gotHeaders := map[string]string{}
	for k := range wantHeaders {
		gotHeaders[k] = resp.Header.Get(k)
	}

	if diff := cmp.Diff(wantHeaders, gotHeaders); diff != "" {
		t.Errorf("GET vms/test-vm/vnc headers mismatch (-want +got):\n%s", diff)
	}
}

// Remote resize is offered only for the virtio video card, and the clipboard mode follows minimega's
// bidirectional-copy-paste.
func TestGetVNCSessionDisplayAndClipboard(t *testing.T) {
	tests := map[string]struct {
		info          mm.VNCInfo
		wantDisplay   vncDisplay
		wantClipboard vncClipboard
	}{
		"std": {
			info:          mm.VNCInfo{Host: "compute-04", Port: 1, VGA: "std"},
			wantDisplay:   vncDisplay{VGA: "std", RemoteResize: false},
			wantClipboard: vncClipboard{Mode: clipboardPasteAsKeys},
		},
		"qxl": {
			info:          mm.VNCInfo{Host: "compute-04", Port: 1, VGA: "qxl"},
			wantDisplay:   vncDisplay{VGA: "qxl", RemoteResize: false},
			wantClipboard: vncClipboard{Mode: clipboardPasteAsKeys},
		},
		"virtio": {
			info:          mm.VNCInfo{Host: "compute-04", Port: 1, VGA: "virtio"},
			wantDisplay:   vncDisplay{VGA: "virtio", RemoteResize: true},
			wantClipboard: vncClipboard{Mode: clipboardPasteAsKeys},
		},
		"bidirectional copy and paste": {
			info:          mm.VNCInfo{Host: "compute-04", Port: 1, VGA: "cirrus", BidirectionalCopyPaste: true},
			wantDisplay:   vncDisplay{VGA: "cirrus", RemoteResize: false},
			wantClipboard: vncClipboard{Mode: clipboardBidirectional},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, body := getVNCSession(t, nil, tc.info)
			session := decodeVNCSession(t, body)

			if diff := cmp.Diff(tc.wantDisplay, session.Display); diff != "" {
				t.Errorf("GET vms/test-vm/vnc with %+v display mismatch (-want +got):\n%s", tc.info, diff)
			}

			if diff := cmp.Diff(tc.wantClipboard, session.Clipboard); diff != "" {
				t.Errorf("GET vms/test-vm/vnc with %+v clipboard mismatch (-want +got):\n%s", tc.info, diff)
			}
		})
	}
}

// The banners come from the vncBanner annotation as plain text: a string splits into lines at its newlines, and
// the map form sets each banner and its colors, or turns both off.
func TestGetVNCSessionBanners(t *testing.T) {
	banner := func(lines ...string) bannerConfig {
		if lines == nil {
			lines = []string{}
		}

		return bannerConfig{Lines: lines, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText}
	}

	tests := []struct {
		name   string
		banner any
		want   vncBanners
	}{
		{
			name: "no annotation names the experiment and VM",
			want: vncBanners{
				Top:    banner("EXP: test-experiment - VM: test-vm"),
				Bottom: banner("EXP: test-experiment - VM: test-vm"),
			},
		},
		{
			name:   "markup in a string stays text",
			banner: `<img src=x onerror="alert(1)">`,
			want:   vncBanners{Top: banner(`<img src=x onerror="alert(1)">`), Bottom: banner(`<img src=x onerror="alert(1)">`)},
		},
		{
			name:   "a string's lines",
			banner: "UNCLASSIFIED\nlab use only",
			want:   vncBanners{Top: banner("UNCLASSIFIED", "lab use only"), Bottom: banner("UNCLASSIFIED", "lab use only")},
		},
		{
			name: "the map form",
			banner: map[string]any{
				"topBanner": map[string]any{
					"banner": []any{"top", "second"}, "backgroundColor": "#a91f3d", "textColor": "#FFF",
				},
				"bottomBanner": map[string]any{
					"banner":          []any{"bottom"},
					"backgroundColor": "red;} body { display: none",
					"textColor":       "url(https://attacker.example/x)",
				},
			},
			want: vncBanners{
				Top:    bannerConfig{Lines: []string{"top", "second"}, BackgroundColor: "#a91f3d", TextColor: "#FFF"},
				Bottom: banner("bottom"),
			},
		},
		{
			name:   "the map form with one banner",
			banner: map[string]any{"bottomBanner": map[string]any{"banner": []any{"one", "two"}}},
			want:   vncBanners{Top: banner(), Bottom: banner("one", "two")},
		},
		{
			name:   "disabled",
			banner: map[string]any{"disabled": true, "topBanner": map[string]any{"banner": []any{"top"}}},
			want:   vncBanners{Top: banner(), Bottom: banner()},
		},
		{
			name:   "a map that does not decode",
			banner: map[string]any{"topBanner": "top"},
			want:   vncBanners{Top: banner(), Bottom: banner()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, body := getVNCSession(t, tt.banner, testVNCInfo)

			if diff := cmp.Diff(tt.want, decodeVNCSession(t, body).Banners); diff != "" {
				t.Errorf("GET vms/test-vm/vnc with vncBanner %v banners mismatch (-want +got):\n%s", tt.banner, diff)
			}

			if strings.Contains(body, "null") {
				t.Errorf("GET vms/test-vm/vnc with vncBanner %v = %s, want [] and not null for no lines", tt.banner, body)
			}
		})
	}
}

// The map form of the annotation sets only banners: other keys do not reach the session.
func TestGetVNCSessionBannerMapSetsOnlyBanners(t *testing.T) {
	_, body := getVNCSession(t, map[string]any{
		"websocket":  "https://attacker.example/",
		"experiment": "attacker-experiment",
		"topBanner":  map[string]any{"banner": []any{"top"}},
	}, testVNCInfo)

	for _, leaked := range []string{"attacker.example", "attacker-experiment"} {
		if strings.Contains(body, leaked) {
			t.Errorf("GET vms/test-vm/vnc = %s, which contains %q from the annotation, want it left out", body, leaked)
		}
	}
}

// vncServe serves a request for test-vm of test-experiment, with query, to h directly, as alice with role, in ctx.
func vncServe(
	ctx context.Context, h func(http.ResponseWriter, *http.Request) error, role rbac.Role, query string,
) *httptest.ResponseRecorder {
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "alice")

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, vncPath+query, nil)
	req = mux.SetURLVars(req, map[string]string{"exp": "test-experiment", "name": "test-vm"})

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(h).ServeHTTP(rec, req)

	return rec
}

// vncNode is a topology node of the named VM.
func vncNode(name string) map[string]any {
	return map[string]any{
		"type":     "VirtualMachine",
		"general":  map[string]any{"hostname": name},
		"hardware": map[string]any{"vcpus": 1, "memory": 256, "os_type": "linux"},
	}
}

func TestFindTopologyNode(t *testing.T) {
	useTestStore(t, testExperiment(t, "", vncNode("other-vm"), vncNode("test-vm")))

	if node := findTopologyNode("test-experiment", "test-vm"); node == nil || node.General().Hostname() != "test-vm" {
		t.Errorf("findTopologyNode(test-experiment, test-vm) = %v, want test-vm's node", node)
	}

	for _, tc := range []struct{ exp, vm string }{
		{"test-experiment", "missing-vm"},
		{"missing-experiment", "test-vm"},
		{"test-experiment", ""},
	} {
		if node := findTopologyNode(tc.exp, tc.vm); node != nil {
			t.Errorf("findTopologyNode(%q, %q) = %v, want nil", tc.exp, tc.vm, node)
		}
	}
}

// The VNC session is refused, audited, without vms/vnc get, and is not found for a VM the experiment does not
// have.
func TestGetVNCRefuses(t *testing.T) {
	useTestStore(t, testExperiment(t, "", vncNode("other-vm")))
	useTestMM(t, &testMM{vnc: testVNCInfo})

	events := captureAudit(t)

	rec := vncServe(t.Context(), GetVNC, testRole("vms/vnc", "get", "test-experiment/other-vm"), "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET vms/test-vm/vnc without vms/vnc get = %d, want %d", rec.Code, http.StatusForbidden)
	}

	wantDenied(t, events(), "vnc-attach")

	rec = vncServe(t.Context(), GetVNC, testRole("vms/vnc", "get", "test-experiment/*"), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET vms/test-vm/vnc of a missing VM = %d, want %d", rec.Code, http.StatusNotFound)
	}

	if got := errorMessage(t, rec.Code, rec.Body.String()); got != "VM not found" {
		t.Errorf("GET vms/test-vm/vnc of a missing VM = %q, want VM not found", got)
	}
}

// A VNC session that minimega cannot give is refused with a status for the reason, and a request that has left
// gets nothing.
func TestGetVNCMinimegaFails(t *testing.T) {
	useTestStore(t, testExperiment(t, "", vncNode("test-vm")))

	role := testRole("vms/vnc", "get", "test-experiment/test-vm")

	tests := map[string]struct {
		err         error
		wantStatus  int
		wantMessage string
	}{
		"a VM that is not running": {
			err:         fmt.Errorf("getting VNC info: %w", mm.ErrVMNotFound),
			wantStatus:  http.StatusConflict,
			wantMessage: "VM is not running",
		},
		"a minimega without the vga column": {
			err:         fmt.Errorf("getting VNC info: %w", mm.ErrOldMinimega),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: mm.ErrOldMinimega.Error(),
		},
		"a minimega that fails": {
			err:         errors.New("busy"),
			wantStatus:  http.StatusBadGateway,
			wantMessage: "unable to get the VNC info of the VM from minimega",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useTestMM(t, &testMM{vncErr: tc.err})

			for _, query := range []string{"", "?probe=true"} {
				rec := vncServe(t.Context(), GetVNC, role, query)
				if rec.Code != tc.wantStatus {
					t.Errorf("GET vms/test-vm/vnc%s = %d, want %d: %s", query, rec.Code, tc.wantStatus, rec.Body.String())
				}

				if got := errorMessage(t, rec.Code, rec.Body.String()); got != tc.wantMessage {
					t.Errorf("GET vms/test-vm/vnc%s message = %q, want %q", query, got, tc.wantMessage)
				}
			}
		})
	}

	t.Run("a request that has left", func(t *testing.T) {
		useTestMM(t, &testMM{vncErr: fmt.Errorf("vnc: %w", context.Canceled)})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if rec := vncServe(ctx, GetVNC, role, ""); rec.Body.Len() != 0 {
			t.Errorf("GET vms/test-vm/vnc whose client left wrote %q, want nothing", rec.Body.String())
		}
	})
}

// listenVNC listens on a local port for the VM's VNC server, and returns the VM's VNC info as minimega gives it and
// the listener.
func listenVNC(t *testing.T) (mm.VNCInfo, net.Listener) {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for the VM's VNC server: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", l.Addr())
	}

	return mm.VNCInfo{Host: "127.0.0.1", Port: addr.Port, VGA: "std"}, l
}

// probeErrorBody is the Error body of a failed probe.
type probeErrorBody struct {
	Message string         `json:"message"`
	Code    weberror.Code  `json:"code"`
	Details map[string]any `json:"details"`
}

// A probe of a VNC port that nobody listens on answers 502 with the code vnc_unreachable and the host and port,
// in less than the probe timeout.
func TestGetVNCProbeUnreachable(t *testing.T) {
	info, l := listenVNC(t)
	_ = l.Close()

	useTestStore(t, testExperiment(t, "", vncNode("test-vm")))
	useTestMM(t, &testMM{vnc: info})

	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm"))

	start := time.Now()
	resp := do(t, http.MethodGet, server.URL+vncPath+"?probe=true", "")
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("GET vms/test-vm/vnc?probe=true of a closed port = %d, want %d: %s",
			resp.StatusCode, http.StatusBadGateway, readBody(t, resp))
	}

	if elapsed > vncProbeTimeout+time.Second {
		t.Errorf("GET vms/test-vm/vnc?probe=true of a closed port took %v, want at most %v", elapsed, vncProbeTimeout)
	}

	wantDocumentedBody(t, http.MethodGet, vncPath, resp)

	var got probeErrorBody
	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
		t.Fatalf("GET vms/test-vm/vnc?probe=true body is not an Error: %v", err)
	}

	want := probeErrorBody{
		Message: "phenix cannot connect to the VNC port of the VM",
		Code:    weberror.CodeVNCUnreachable,
		Details: map[string]any{"host": "127.0.0.1", "port": float64(info.Port)},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET vms/test-vm/vnc?probe=true of a closed port mismatch (-want +got):\n%s", diff)
	}
}

// A probe of a listening VNC port connects, closes the connection, and answers the normal session.
func TestGetVNCProbeReachable(t *testing.T) {
	info, l := listenVNC(t)

	accepted := make(chan error, 1)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			accepted <- err

			return
		}

		defer conn.Close()

		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		// the probe closes its connection without sending
		_, err = conn.Read(make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			accepted <- fmt.Errorf("reading from the probe = %w, want EOF", err)

			return
		}

		accepted <- nil
	}()

	useTestStore(t, testExperiment(t, "", vncNode("test-vm")))
	useTestMM(t, &testMM{vnc: info})

	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm"))
	resp := do(t, http.MethodGet, server.URL+vncPath+"?probe=true", "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET vms/test-vm/vnc?probe=true = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	wantDocumentedBody(t, http.MethodGet, vncPath, resp)

	session := decodeVNCSession(t, readBody(t, resp))
	if want := "experiments/test-experiment/vms/test-vm/vnc/ws"; session.WebSocket != want {
		t.Errorf("GET vms/test-vm/vnc?probe=true websocket = %q, want %q", session.WebSocket, want)
	}

	select {
	case err := <-accepted:
		if err != nil {
			t.Errorf("the VM's VNC server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("GET vms/test-vm/vnc?probe=true did not connect to the VM's VNC server")
	}
}

// A probe value that is not a boolean is refused.
func TestGetVNCProbeNotBoolean(t *testing.T) {
	useTestStore(t, testExperiment(t, "", vncNode("test-vm")))
	useTestMM(t, &testMM{vnc: testVNCInfo})

	rec := vncServe(t.Context(), GetVNC, testRole("vms/vnc", "get", "test-experiment/test-vm"), "?probe=maybe")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET vms/test-vm/vnc?probe=maybe = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestVNCProbeTimeout(t *testing.T) {
	t.Parallel()

	if vncProbeTimeout != 3*time.Second {
		t.Errorf("vncProbeTimeout = %v, want 3s", vncProbeTimeout)
	}
}

// A VNC websocket refused for want of vms/vnc is audited (the contract tests
// cover which roles are refused).
func TestVNCWebSocketDenialIsAudited(t *testing.T) {
	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/other-vm")).URL
	events := captureAudit(t)

	resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/vnc/ws", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET vms/test-vm/vnc/ws without vms/vnc get = %d, want %d: %s", resp.StatusCode, http.StatusForbidden, readBody(t, resp))
	}

	wantDenied(t, events(), "vnc-attach")
}

// vncAttachRuns counts the runs of TestVNCWebSocketAttachIsAudited in this process.
//
//nolint:gochecknoglobals // each run in a process needs a new user
var vncAttachRuns atomic.Uint64

// Opening a VM's VNC websocket is audited once it is attached.
func TestVNCWebSocketAttachIsAudited(t *testing.T) {
	info, l := listenVNC(t)
	echoVNC(l)

	useTestMM(t, &testMM{vnc: info})

	// the server's own limit audits a user and VM once a window, so each run is a new user
	user := fmt.Sprintf("alice-%d", vncAttachRuns.Add(1))

	api := serveAs(t, user, testRole("vms/vnc", "get", "test-experiment/test-vm")).URL + "/api/v1"
	events := captureAudit(t)

	ws, err := websocket.Dial(
		"ws"+strings.TrimPrefix(api, "http")+"/experiments/test-experiment/vms/test-vm/vnc/ws", "", api,
	)
	if err != nil {
		t.Fatalf("websocket.Dial(vms/test-vm/vnc/ws) error = %v", err)
	}

	defer ws.Close()

	if _, err := ws.Write([]byte("RFB")); err != nil {
		t.Fatalf("writing to vms/test-vm/vnc/ws: %v", err)
	}

	buf := make([]byte, 3)
	if _, err := io.ReadFull(ws, buf); err != nil {
		t.Errorf("reading the echo from vms/test-vm/vnc/ws: %v", err)
	} else if got := string(buf); got != "RFB" {
		t.Errorf("vms/test-vm/vnc/ws echoed %q, want RFB", got)
	}

	wantAudit(t, events(), "vnc-attach", "success", map[string]any{
		"user.name": user, "phenix.experiment.name": "test-experiment", "phenix.vm.name": "test-vm",
		"phenix.suppressed": 0,
	})
}

// echoVNC serves the VM's VNC server on l: it echoes what each connection sends.
func echoVNC(l net.Listener) {
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
}

// testVNCAttaches is a limit of the vnc-attach records by the clock, the time since 2026-01-01 that the test moves.
func testVNCAttaches(clock *atomic.Int64) *audit.Repeats {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	return audit.NewRepeats(vncAttachWindow, vncFailureWindow, maxVNCAuditKeys, func() time.Time {
		return start.Add(time.Duration(clock.Load()))
	})
}

// attachVNC opens the VNC websocket of vm in test-experiment as user, through a server that audits with attaches,
// and closes it once it has echoed a message.
func attachVNC(t *testing.T, attaches *audit.Repeats, user, vm string) {
	t.Helper()

	role := testRole("vms/vnc", "get", "test-experiment/vm1", "test-experiment/vm2")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, role)
		ctx = context.WithValue(ctx, middleware.ContextKeyUser, user)
		r = mux.SetURLVars(r.WithContext(ctx), map[string]string{"exp": "test-experiment", "name": vm})

		_ = serveVNCWebSocket(w, r, attaches)
	}))
	defer srv.Close()

	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), "", srv.URL)
	if err != nil {
		t.Fatalf("websocket.Dial(vms/%s/vnc/ws) as %s error = %v", vm, user, err)
	}

	defer ws.Close()

	// the echo comes through the proxy, which starts after the audit
	if _, err := ws.Write([]byte("RFB")); err != nil {
		t.Fatalf("writing to vms/%s/vnc/ws: %v", vm, err)
	}

	if _, err := io.ReadFull(ws, make([]byte, 3)); err != nil {
		t.Fatalf("reading the echo from vms/%s/vnc/ws: %v", vm, err)
	}
}

// A user's attaches to one VM are audited once each 5 minutes, live tiles connecting again as they come back on
// screen; another user or VM has its own record, and the first attach after the window counts those skipped.
func TestVNCWebSocketAttachAuditedOnceAWindow(t *testing.T) {
	info, l := listenVNC(t)
	echoVNC(l)

	useTestMM(t, &testMM{vnc: info})

	var (
		clock    = new(atomic.Int64)
		attaches = testVNCAttaches(clock)
		events   = captureAudit(t)
	)

	steps := []struct {
		advance  time.Duration
		user, vm string
		want     int // the user's success records of vm so far
	}{
		{user: "alice", vm: "vm1", want: 1},
		{user: "alice", vm: "vm1", want: 1},
		{user: "bob", vm: "vm1", want: 1},
		{user: "alice", vm: "vm2", want: 1},
		{advance: vncAttachWindow - time.Second, user: "alice", vm: "vm1", want: 1},
		{advance: time.Second, user: "alice", vm: "vm1", want: 2},
	}

	for i, s := range steps {
		clock.Add(int64(s.advance))
		attachVNC(t, attaches, s.user, s.vm)

		fields := map[string]any{"user.name": s.user, "phenix.experiment.name": "test-experiment", "phenix.vm.name": s.vm}
		if got := countAudit(events(), "vnc-attach", "success", fields); got != s.want {
			t.Errorf("step %d: %s's vnc-attach success records of %s = %d, want %d", i, s.user, s.vm, got, s.want)
		}
	}

	// the attach after the window counts the 2 skipped
	wantAudit(t, events(), "vnc-attach", "success", map[string]any{
		"user.name": "alice", "phenix.vm.name": "vm1", "phenix.suppressed": 2,
	})
}

// The VNC websocket dials the VM's VNC server only after the upgrade: a request that is not a websocket
// handshake does not connect to it.
func TestVNCWebSocketDialsAfterUpgrade(t *testing.T) {
	info, l := listenVNC(t)

	useTestMM(t, &testMM{vnc: info})

	server := serveAs(t, "alice", testRole("vms/vnc", "get", "test-experiment/test-vm")).URL

	resp := do(t, http.MethodGet, server+vncPath+"/ws", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET vms/test-vm/vnc/ws without a handshake = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	tcp, ok := l.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener %T is not TCP", l)
	}

	// a connection made before the answer is already queued
	if err := tcp.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("setting the listener deadline: %v", err)
	}

	if conn, err := tcp.Accept(); err == nil {
		_ = conn.Close()

		t.Error("GET vms/test-vm/vnc/ws without a handshake connected to the VM's VNC server, want no connection")
	}
}

// A VNC websocket whose endpoint minimega cannot find is refused and audited
// as failed, answering nothing to a client that has left; while the server
// stops, it is refused before it is upgraded.
func TestGetVNCWebSocketFails(t *testing.T) {
	role := testRole("vms/vnc", "get", "test-experiment/test-vm")
	attaches := testVNCAttaches(new(atomic.Int64))
	serve := func(w http.ResponseWriter, r *http.Request) error { return serveVNCWebSocket(w, r, attaches) }

	useTestMM(t, &testMM{vncErr: errors.New("vm not found")})

	events := captureAudit(t)

	rec := vncServe(t.Context(), serve, role, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET vms/test-vm/vnc/ws without an endpoint = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if got := errorMessage(t, rec.Code, rec.Body.String()); got != "unable to get VNC endpoint" {
		t.Errorf("GET vms/test-vm/vnc/ws without an endpoint = %q, want unable to get VNC endpoint", got)
	}

	wantAudit(t, events(), "vnc-attach", "failure", map[string]any{"user.name": "alice", "phenix.vm.name": "test-vm"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	useTestMM(t, &testMM{vncErr: fmt.Errorf("vnc: %w", context.Canceled)})

	if rec := vncServe(ctx, serve, role, ""); rec.Body.Len() != 0 {
		t.Errorf("GET vms/test-vm/vnc/ws whose client left wrote %q, want nothing", rec.Body.String())
	}

	useTestMM(t, &testMM{vnc: mm.VNCInfo{Host: "127.0.0.1", Port: 5900}})

	lifecycle.Stop()
	t.Cleanup(lifecycle.Start)

	rec = vncServe(t.Context(), serve, role, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET vms/test-vm/vnc/ws while stopping = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	if got := rec.Header().Get("Retry-After"); got != lifecycle.RetryAfter {
		t.Errorf("GET vms/test-vm/vnc/ws while stopping Retry-After = %q, want %q", got, lifecycle.RetryAfter)
	}
}

// A user's failed attaches to one VM with one error are audited, and logged as errors, once a minute; the rest are
// logged at debug level. Another error has its own record.
func TestGetVNCWebSocketFailureRateLimited(t *testing.T) {
	var (
		role     = testRole("vms/vnc", "get", "test-experiment/test-vm")
		clock    = new(atomic.Int64)
		attaches = testVNCAttaches(clock)
		serve    = func(w http.ResponseWriter, r *http.Request) error { return serveVNCWebSocket(w, r, attaches) }
		events   = captureAudit(t)
		logs     = plogtest.Capture(t)
	)

	steps := []struct {
		advance time.Duration
		err     string
		want    int    // the failure records with err so far
		level   string // of the log record of the failure
	}{
		{err: "vm not found", want: 1, level: "ERROR"},
		{err: "vm not found", want: 1, level: "DEBUG"},
		{err: "minimega timed out", want: 1, level: "ERROR"},
		{advance: vncFailureWindow - time.Second, err: "vm not found", want: 1, level: "DEBUG"},
		{advance: time.Second, err: "vm not found", want: 2, level: "ERROR"},
	}

	for i, s := range steps {
		clock.Add(int64(s.advance))
		useTestMM(t, &testMM{vncErr: errors.New(s.err)})

		if rec := vncServe(t.Context(), serve, role, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("step %d: GET vms/test-vm/vnc/ws without an endpoint = %d, want %d", i, rec.Code, http.StatusBadRequest)
		}

		fields := map[string]any{"user.name": "alice", "phenix.vm.name": "test-vm", "error.message": s.err}
		if got := countAudit(events(), "vnc-attach", "failure", fields); got != s.want {
			t.Errorf("step %d: vnc-attach failure records of %q = %d, want %d", i, s.err, got, s.want)
		}

		logged := logs.Take(t, plogtest.Message("getting VNC endpoint"))
		if len(logged) != 1 || logged[0]["level"] != s.level {
			t.Errorf("step %d: getting VNC endpoint records = %v, want one at level %s", i, logged, s.level)
		}
	}
}

// genVNCBannerText draws text heavy in markup and newlines.
func genVNCBannerText() *rapid.Generator[string] {
	return rapid.StringOf(rapid.OneOf(
		rapid.SampledFrom([]rune("<>&\"'+=/;:{}()\n\r\t abcAB")),
		rapid.Rune(),
	))
}

// vncBannerColor is a banner color in an annotation and the color the session
// should give for it.
type vncBannerColor struct {
	given, shown string
}

// genVNCBannerColor draws CSS color names and hex colors, kept as they are,
// and unset, malformed or injected colors, which give fallback.
func genVNCBannerColor(fallback string) *rapid.Generator[vncBannerColor] {
	return rapid.Custom(func(t *rapid.T) vncBannerColor {
		switch rapid.IntRange(0, 2).Draw(t, "kind") {
		case 0:
			c := rapid.OneOf(
				rapid.SampledFrom([]string{"red", "Navy", "rebeccapurple", "transparent", "currentColor"}),
				rapid.StringMatching(`#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})`),
			).Draw(t, "css")

			return vncBannerColor{given: c, shown: c}
		case 1:
			return vncBannerColor{given: "", shown: fallback}
		default:
			c := rapid.OneOf(
				rapid.StringMatching(`[a-z#0-9]{0,4}[;:(){} "'/\\<>,.!@-][a-z0-9 ;(){}]{0,8}`),
				rapid.StringMatching(`#([0-9a-f]{1,2}|[0-9a-f]{5}|[0-9a-f]{7}|[0-9a-f]{9})`),
				rapid.StringMatching(`[a-z]{33,36}`),
			).Draw(t, "bad")

			return vncBannerColor{given: c, shown: fallback}
		}
	})
}

// The session gives any banner text back as it is, a string split at its
// newlines, and a banner's colors are CSS colors or the defaults.
func TestVNCSessionBannerProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			annotation any
			want       vncBanners
		)

		if rapid.Bool().Draw(t, "map form") {
			banners := map[string]any{}
			sides := map[string]*bannerConfig{"top": &want.Top, "bottom": &want.Bottom}

			for _, side := range []string{"top", "bottom"} {
				lines := rapid.SliceOfN(genVNCBannerText(), 0, 3).Draw(t, side+" lines")
				background := genVNCBannerColor(defaultBannerBackground).Draw(t, side+" background")
				text := genVNCBannerColor(defaultBannerText).Draw(t, side+" text color")

				banner := make([]any, 0, len(lines))
				for _, line := range lines {
					banner = append(banner, line)
				}

				banners[side+"Banner"] = map[string]any{
					"banner": banner, "backgroundColor": background.given, "textColor": text.given,
				}

				*sides[side] = bannerConfig{
					Lines: append([]string{}, lines...), BackgroundColor: background.shown, TextColor: text.shown,
				}
			}

			annotation = banners
		} else {
			text := genVNCBannerText().Draw(t, "text")
			annotation = text

			lines := strings.Split(text, "\n")
			want.Top = bannerConfig{Lines: lines, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText}
			want.Bottom = want.Top
		}

		session := newVNCSession("test-experiment", "test-vm", testVNCInfo)
		session.setBanners("test-vm", annotation)

		raw, err := json.Marshal(session)
		if err != nil {
			t.Fatalf("json.Marshal(session for vncBanner %q) error = %v", annotation, err)
		}

		var got vncSession
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", raw, err)
		}

		// encoding/json gives invalid UTF-8 back as U+FFFD, as a browser reads it
		if diff := cmp.Diff(jsonRoundTrip(t, want), got.Banners); diff != "" {
			t.Fatalf("session banners for vncBanner %q mismatch (-want +got):\n%s", annotation, diff)
		}
	})
}

// jsonRoundTrip is v as a client decodes it from JSON.
func jsonRoundTrip(t *rapid.T, v vncBanners) vncBanners {
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%v) error = %v", v, err)
	}

	var out vncBanners
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", raw, err)
	}

	return out
}

// A vncBanner annotation of another type leaves the banners empty, and one
// with unknown keys is used, warning of the keys.
func TestVNCSessionSetBanners(t *testing.T) {
	logs := plogtest.Capture(t)

	defaults := bannerConfig{Lines: []string{}, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText}

	tests := map[string]struct {
		annotation  any
		top, bottom bannerConfig
		warning     string
	}{
		"number": {annotation: 42, top: defaults, bottom: defaults},
		"list":   {annotation: []any{"top"}, top: defaults, bottom: defaults},
		"unknown keys": {
			annotation: map[string]any{
				"topBanner":  map[string]any{"banner": []any{"top"}, "textColor": "red"},
				"sideBanner": "left",
			},
			top:     bannerConfig{Lines: []string{"top"}, BackgroundColor: defaultBannerBackground, TextColor: "red"},
			bottom:  defaults,
			warning: "ignoring unknown vncBanner annotation keys for VM",
		},
		"empty string": {
			annotation: "",
			top:        bannerConfig{Lines: []string{""}, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText},
			bottom:     bannerConfig{Lines: []string{""}, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			logs.Take(t, nil)

			session := newVNCSession("exp", "vm", testVNCInfo)
			session.setBanners("vm", tc.annotation)

			if diff := cmp.Diff(tc.top, session.Banners.Top); diff != "" {
				t.Errorf("setBanners(%v) top banner mismatch (-want +got):\n%s", tc.annotation, diff)
			}

			if diff := cmp.Diff(tc.bottom, session.Banners.Bottom); diff != "" {
				t.Errorf("setBanners(%v) bottom banner mismatch (-want +got):\n%s", tc.annotation, diff)
			}

			if tc.warning != "" && len(logs.Take(t, plogtest.Message(tc.warning))) != 1 {
				t.Errorf("setBanners(%v) logged no %q, want it", tc.annotation, tc.warning)
			}
		})
	}
}

func TestNewVNCSession(t *testing.T) {
	t.Parallel()

	defaults := bannerConfig{Lines: []string{}, BackgroundColor: defaultBannerBackground, TextColor: defaultBannerText}
	want := &vncSession{
		Experiment: "exp",
		VM:         "vm 1",
		WebSocket:  "experiments/exp/vms/vm%201/vnc/ws",
		Banners:    vncBanners{Top: defaults, Bottom: defaults},
		Display:    vncDisplay{VGA: "virtio", RemoteResize: true},
		Clipboard:  vncClipboard{Mode: clipboardBidirectional},
	}

	got := newVNCSession("exp", "vm 1", mm.VNCInfo{Host: "h", Port: 1, VGA: "virtio", BidirectionalCopyPaste: true})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("newVNCSession() mismatch (-want +got):\n%s", diff)
	}
}

func TestBannerColor(t *testing.T) {
	t.Parallel()

	for color, want := range map[string]string{
		"":                      "fallback",
		"red":                   "red",
		"RebeccaPurple":         "RebeccaPurple",
		"#abc":                  "#abc",
		"#ABCD":                 "#ABCD",
		"#a91f3d":               "#a91f3d",
		"#a91f3dff":             "#a91f3dff",
		"#ab":                   "fallback",
		"#abcde":                "fallback",
		"#abcdefg":              "fallback",
		"rgb(1,2,3)":            "fallback",
		"red;":                  "fallback",
		"red blue":              "fallback",
		strings.Repeat("a", 32): strings.Repeat("a", 32),
		strings.Repeat("a", 33): "fallback",
	} {
		if got := bannerColor("vm", color, "fallback"); got != want {
			t.Errorf("bannerColor(%q) = %q, want %q", color, got, want)
		}
	}
}
