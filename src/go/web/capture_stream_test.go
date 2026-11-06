package web

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/livecapture"
	"phenix/store"
	"phenix/util/mm"
	"phenix/web/lifecycle"
	"phenix/web/rbac"
)

func TestGetVMCaptureStreamRefuses(t *testing.T) {
	useLiveCapture(t, useWebShark(t))

	tests := map[string]struct {
		role  rbac.Role
		iface string
		want  int
		// the audit event: "denied", "failed", or "" for none
		wantAudit string
	}{
		"no file permission": {
			testRole("vms/captures", "list", "test-experiment/test-vm"), "0", http.StatusForbidden, "denied",
		},
		"no capture permission": {
			testRole("experiments/files", "get", "test-experiment"), "0", http.StatusForbidden, "denied",
		},
		"interface name": {sharkRole(), "eth0", http.StatusBadRequest, ""},
		// an attempt to read the capture, though there is none
		"not captured": {sharkRole(), "1", http.StatusNotFound, "failed"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, "alice", tt.role).URL
			events := captureAudit(t)

			path := "/api/v1/experiments/test-experiment/vms/test-vm/captures/" + tt.iface + "/stream"

			resp := do(t, http.MethodGet, server+path, "")
			if resp.StatusCode != tt.want {
				t.Errorf("GET %s = %d, want %d: %s", path, resp.StatusCode, tt.want, readBody(t, resp))
			}

			switch got := events(); tt.wantAudit {
			case "denied":
				wantDenied(t, got, "capture-stream")
			case "failed":
				e := wantAudit(t, got, "capture-stream", "failure", map[string]any{"user.name": "alice", "phenix.vm.name": "test-vm"})
				if e != nil && e["error.message"] == nil {
					t.Errorf("GET %s capture-stream failure event = %v, want an error.message", path, e)
				}
			default:
				if len(got) != 0 {
					t.Errorf("GET %s audit events = %v, want none before the RBAC check", path, got)
				}
			}
		})
	}
}

func TestGetVMCaptureStream(t *testing.T) {
	for _, tt := range []struct {
		name      string
		websocket bool
		fromNow   bool
	}{
		{"pcap body", false, false},
		{"websocket from now", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			live := useLiveCapture(t, useWebShark(t))
			header, old, fresh := testPcapHeader(), testPcapRecord(60), testPcapRecord(1500)
			live.write(t, header, old)

			// the stream starts once everything captured so far has been read,
			// so from=now skips exactly the packet already captured
			followLiveCapture(t)

			url := serveAs(t, "alice", sharkRole()).URL +
				"/api/v1/experiments/test-experiment/vms/test-vm/captures/0/stream"
			if tt.fromNow {
				url += "?from=now"
			}

			events := captureAudit(t)
			expect := openCaptureStream(t, url, tt.websocket)

			expect(header, "the file header")

			wantAudit(t, events(), "capture-stream", "success", map[string]any{
				"user.name": "alice", "phenix.experiment.name": "test-experiment",
				"phenix.vm.name": "test-vm", "phenix.vm.interface": 0,
			})

			if !tt.fromNow {
				expect(old, "the packet already captured")
			}

			// a packet captured while the stream is open arrives on it
			live.write(t, fresh)
			expect(fresh, "the new packet")
		})
	}
}

// followLiveCapture follows the test VM's capture, as a stream of it does,
// until it has read everything captured so far.
func followLiveCapture(t *testing.T) {
	t.Helper()

	tap, err := livecapture.Open("test-experiment", "test-vm", 0)
	if err != nil {
		t.Fatalf("livecapture.Open(test-experiment, test-vm, 0) = %v", err)
	}

	t.Cleanup(tap.Release)

	deadline := time.After(5 * time.Second)

	for st := tap.State(); !st.CaughtUp; st = tap.State() {
		select {
		case <-st.Changed:
		case <-deadline:
			t.Fatal("test-vm's capture was not read within 5s")
		}
	}
}

// openCaptureStream streams the capture from url, over a websocket or as a
// pcap body, and returns what checks the next bytes to arrive. Over a
// websocket, each check is of one message.
func openCaptureStream(t *testing.T, url string, overWebSocket bool) func(want []byte, what string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	if overWebSocket {
		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(url, "http"), nil)
		if err != nil {
			t.Fatalf("dialing %s: %v", url, err)
		}

		resp.Body.Close()
		t.Cleanup(func() { conn.Close() })

		return func(want []byte, what string) {
			t.Helper()

			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

			kind, msg, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("reading %s from %s: %v", what, url, err)
			}

			if kind != websocket.BinaryMessage {
				t.Fatalf("reading %s from %s = message type %d, want %d", what, url, kind, websocket.BinaryMessage)
			}

			if diff := cmp.Diff(want, msg); diff != "" {
				t.Fatalf("reading %s from %s mismatch (-want +got):\n%s", what, url, diff)
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext(GET %s) = %v", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", url, resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Content-Type"); got != pcapContentType {
		t.Errorf("GET %s Content-Type = %q, want %q", url, got, pcapContentType)
	}

	return func(want []byte, what string) {
		t.Helper()

		got := make([]byte, len(want))
		if n, err := io.ReadFull(resp.Body, got); err != nil {
			t.Fatalf("reading %s from %s = %d bytes, %v; want %d", what, url, n, err, len(want))
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("reading %s from %s mismatch (-want +got):\n%s", what, url, diff)
		}
	}
}

// captureStreamRemoteMM is the test minimega, but with every VM on a mesh
// node whose name phenix will not pass to minimega.
type captureStreamRemoteMM struct {
	*testMM
}

func (captureStreamRemoteMM) IsHeadnode(string) bool { return false }

func (captureStreamRemoteMM) GetVMHost(...mm.Option) (string, error) { return "compute 1", nil }

// captureStreamCorruptRecord is a pcap record no writer would produce: it
// holds more bytes than the packet had.
func captureStreamCorruptRecord() []byte {
	b := testPcapRecord(100)
	binary.LittleEndian.PutUint32(b[12:], 50)

	return b
}

// captureStreamFailingWriter is a ResponseWriter whose writes fail.
type captureStreamFailingWriter struct {
	*httptest.ResponseRecorder
}

func (captureStreamFailingWriter) Write([]byte) (int, error) {
	return 0, errors.New("client went away")
}

// The spools of remote captures last written before now are deleted, and
// only those; one that cannot be deleted is logged.
func TestRemoveStaleCaptureSpools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	var (
		stale  = filepath.Join(dir, "phenix-capture-1.pcap")
		fresh  = filepath.Join(dir, "phenix-capture-2.pcap")
		other  = filepath.Join(dir, "other.pcap")
		future = time.Now().Add(time.Hour)
	)

	for path, mod := range map[string]time.Time{stale: time.Now().Add(-time.Hour), fresh: future, other: time.Now().Add(-time.Hour)} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", path, err)
		}

		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("os.Chtimes(%s) error = %v", path, err)
		}
	}

	records := captureRecords(t)

	removeStaleCaptureSpools()

	for path, want := range map[string]bool{stale: false, fresh: true, other: true} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("after removeStaleCaptureSpools(), %s exists = %t, want %t", filepath.Base(path), err == nil, want)
		}
	}

	if !captureStreamLogged(records(), "removed stale capture spools", `"count":1`) {
		t.Errorf("removeStaleCaptureSpools() logs = %v, want a removal of 1 spool", records())
	}

	t.Run("cannot delete", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root deletes every file")
		}

		if err := os.Chtimes(fresh, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
			t.Fatalf("os.Chtimes(%s) error = %v", fresh, err)
		}

		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("os.Chmod(%s) error = %v", dir, err)
		}

		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		records := captureRecords(t)

		removeStaleCaptureSpools()

		if _, err := os.Stat(fresh); err != nil {
			t.Errorf("os.Stat(%s) after a failed removal error = %v", fresh, err)
		}

		if !captureStreamLogged(records(), "removing stale capture spools", "permission denied") {
			t.Errorf("removeStaleCaptureSpools() logs = %v, want a warning naming the failed removal", records())
		}

		if captureStreamLogged(records(), "removed stale capture spools") {
			t.Errorf("removeStaleCaptureSpools() logs = %v, want no removal logged", records())
		}
	})
}

// captureStreamLogged reports whether a log line holds msg and each of parts.
func captureStreamLogged(lines []string, msg string, parts ...string) bool {
	for _, line := range lines {
		if !strings.Contains(line, `"msg":"`+msg+`"`) {
			continue
		}

		found := true

		for _, p := range parts {
			found = found && strings.Contains(line, p)
		}

		if found {
			return true
		}
	}

	return false
}

// A capture minimega is not writing is a 404, a path phenix will not read a
// 400, and any other failure to open it a 500.
func TestCaptureStreamStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"not capturing": {fmt.Errorf("finding: %w", livecapture.ErrNotCapturing), http.StatusNotFound},
		"unsafe path":   {fmt.Errorf("mesh: %w", livecapture.ErrUnsafePath), http.StatusBadRequest},
		"unsupported":   {fmt.Errorf("open: %w", mm.ErrCaptureUnsupported), http.StatusNotImplemented},
		"other":         {errors.New("opening store"), http.StatusInternalServerError},
		"nil":           {nil, http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := captureStreamStatus(tc.err); got != tc.want {
				t.Errorf("captureStreamStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// Read access to a capture needs both its listing and the experiment's
// files.
func TestCaptureReadAllowed(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		role rbac.Role
		want bool
	}{
		"both":          {sharkRole(), true},
		"captures only": {testRole("vms/captures", "list", "test-experiment/test-vm"), false},
		"files only":    {testRole("experiments/files", "get", "test-experiment"), false},
		"another VM's captures": {
			combinedRole(
				testRole("vms/captures", "list", "test-experiment/other"),
				testRole("experiments/files", "get", "test-experiment"),
			),
			false,
		},
		"another experiment": {
			combinedRole(testRole("vms/captures", "list", "other/test-vm"), testRole("experiments/files", "get", "other")),
			false,
		},
		"admin":          {adminRole(), true},
		"no permissions": {rbac.Role{}, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := captureReadAllowed(tc.role, "test-experiment", "test-vm"); got != tc.want {
				t.Errorf("captureReadAllowed(%s) = %t, want %t", name, got, tc.want)
			}
		})
	}
}

// A capture on a node phenix will not name to minimega is a 400, and one whose
// file cannot be opened a 500; both are audited as failed reads.
func TestGetVMCaptureStreamOpenFails(t *testing.T) {
	tests := map[string]struct {
		remote bool
		want   int
	}{
		"unsafe mesh node": {remote: true, want: http.StatusBadRequest},
		"file missing":     {want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := useWebShark(t, mm.Capture{VM: "test-vm", Interface: 0, Filepath: filepath.Join(t.TempDir(), "gone.pcap")})
			t.Cleanup(livecapture.CloseAll)

			if tc.remote {
				mm.DefaultMM = captureStreamRemoteMM{testMM: fake} //nolint:reassign // useTestMM restores it
			}

			server := serveAs(t, "alice", sharkRole()).URL
			events := captureAudit(t)

			resp := do(t, http.MethodGet, server+"/api/v1/experiments/test-experiment/vms/test-vm/captures/0/stream", "")
			if body := readBody(t, resp); resp.StatusCode != tc.want {
				t.Errorf("GET stream = %d, want %d: %s", resp.StatusCode, tc.want, body)
			}

			wantAudit(t, events(), "capture-stream", "failure", map[string]any{"user.name": "alice", "phenix.vm.interface": 0})
		})
	}
}

// A stream grant whose capture has since stopped is a 404, audited as a
// failed read by the grant's user.
func TestCaptureTokenStreamOfStoppedCapture(t *testing.T) {
	useLiveCapture(t, useWebShark(t))

	server := serveAs(t, "alice", sharkRole()).URL
	token := streamToken(t, server)

	// stop following it, as when minimega stopped writing it
	livecapture.CloseAll()

	c, _ := store.NewConfig("experiment/test-experiment")
	if err := store.Get(c); err != nil {
		t.Fatalf("store.Get(test-experiment) error = %v", err)
	}

	c.Status = nil
	if err := store.Update(c); err != nil {
		t.Fatalf("store.Update(test-experiment) error = %v", err)
	}

	events := captureAudit(t)

	resp := do(t, http.MethodGet, server+"/"+token.URLs["stream"], "")
	if body := readBody(t, resp); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET stream of a stopped capture = %d, want %d: %s", resp.StatusCode, http.StatusNotFound, body)
	}

	wantAudit(t, events(), "capture-stream", "failure", map[string]any{"user.name": "alice", "phenix.capability.id": token.ID})
}

// A capture whose file turns out corrupt ends its stream early: a pcap body
// just ends, and a websocket closes normally saying the stream failed.
func TestCaptureStreamEndsOnCorruptCapture(t *testing.T) {
	for _, overWebSocket := range []bool{false, true} {
		t.Run(fmt.Sprintf("websocket=%t", overWebSocket), func(t *testing.T) {
			live := useLiveCapture(t, useWebShark(t))
			header := testPcapHeader()
			live.write(t, header)

			url := serveAs(t, "alice", sharkRole()).URL + "/api/v1/experiments/test-experiment/vms/test-vm/captures/0/stream"

			if !overWebSocket {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()

				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("GET %s: %v", url, err)
				}
				defer resp.Body.Close()

				live.write(t, captureStreamCorruptRecord())

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("reading %s to its end: %v", url, err)
				}

				if diff := cmp.Diff(header, body); diff != "" {
					t.Errorf("GET %s body mismatch (-want +got):\n%s", url, diff)
				}

				return
			}

			conn, resp, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(url, "http"), nil)
			if err != nil {
				t.Fatalf("dialing %s: %v", url, err)
			}

			resp.Body.Close()
			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

			if _, msg, err := conn.ReadMessage(); err != nil || !cmp.Equal(header, msg) {
				t.Fatalf("reading the file header = %x, %v; want %x", msg, err, header)
			}

			live.write(t, captureStreamCorruptRecord())

			for {
				_, _, err := conn.ReadMessage()

				var closed *websocket.CloseError
				if errors.As(err, &closed) {
					if closed.Code != websocket.CloseNormalClosure || closed.Text != "capture stream failed" {
						t.Errorf("stream closed with %d %q, want %d %q", closed.Code, closed.Text,
							websocket.CloseNormalClosure, "capture stream failed")
					}

					return
				}

				if err != nil {
					t.Fatalf("reading %s: %v, want a close message", url, err)
				}
			}
		})
	}
}

// Once the server starts shutting down, a new websocket stream is refused
// with 503, and an open one, which pongs keep open, closes going away.
func TestCaptureStreamWebSocketAtShutdown(t *testing.T) {
	lifecycle.Start()
	t.Cleanup(lifecycle.Start)

	live := useLiveCapture(t, useWebShark(t))
	header, fresh := testPcapHeader(), testPcapRecord(60)
	live.write(t, header)

	url := "ws" + strings.TrimPrefix(serveAs(t, "alice", sharkRole()).URL, "http") +
		"/api/v1/experiments/test-experiment/vms/test-vm/captures/0/stream"

	conn, resp, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	if err != nil {
		t.Fatalf("dialing %s: %v", url, err)
	}

	resp.Body.Close()
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	if _, msg, err := conn.ReadMessage(); err != nil || !cmp.Equal(header, msg) {
		t.Fatalf("reading the file header = %x, %v; want %x", msg, err, header)
	}

	if err := conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("sending a pong: %v", err)
	}

	live.write(t, fresh)

	if _, msg, err := conn.ReadMessage(); err != nil || !cmp.Equal(fresh, msg) {
		t.Fatalf("reading a packet after a pong = %x, %v; want %x", msg, err, fresh)
	}

	lifecycle.Stop()

	var closed *websocket.CloseError
	if _, _, err := conn.ReadMessage(); !errors.As(err, &closed) ||
		closed.Code != websocket.CloseGoingAway || closed.Text != lifecycle.CloseReason {
		t.Errorf("stream at shutdown read error = %v, want close %d %q", err, websocket.CloseGoingAway, lifecycle.CloseReason)
	}

	_, resp, err = websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	if err == nil || resp == nil {
		t.Fatalf("dialing %s at shutdown error = %v, want a refused handshake", url, err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != lifecycle.RetryAfter {
		t.Errorf("dialing %s at shutdown = %d Retry-After %q, want %d %q", url, resp.StatusCode,
			resp.Header.Get("Retry-After"), http.StatusServiceUnavailable, lifecycle.RetryAfter)
	}
}

// A websocket stream of a request that is no websocket handshake answers the
// failed upgrade, and never reads the capture.
func TestStreamCaptureWebSocketRefusesPlainRequest(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	streamCaptureWebSocket(w, httptest.NewRequest(http.MethodGet, "/stream", nil), nil, false)

	if w.Code != http.StatusBadRequest || w.Header().Get("Sec-Websocket-Version") != "13" {
		t.Errorf("streamCaptureWebSocket(plain GET) = %d Sec-Websocket-Version %q, want %d %q",
			w.Code, w.Header().Get("Sec-Websocket-Version"), http.StatusBadRequest, "13")
	}
}

// pcapSender writes and flushes each chunk, and returns why a write failed.
func TestPcapSender(t *testing.T) {
	t.Parallel()

	t.Run("sent", func(t *testing.T) {
		t.Parallel()

		w := httptest.NewRecorder()
		send := pcapSender(w)

		for _, chunk := range []string{"header", "", "record"} {
			if err := send([]byte(chunk)); err != nil {
				t.Fatalf("send(%q) error = %v", chunk, err)
			}
		}

		if w.Body.String() != "headerrecord" || !w.Flushed {
			t.Errorf("after sends, body = %q, flushed = %t; want %q, true", w.Body.String(), w.Flushed, "headerrecord")
		}
	})

	t.Run("client gone", func(t *testing.T) {
		t.Parallel()

		w := captureStreamFailingWriter{httptest.NewRecorder()}
		if err := pcapSender(w)([]byte("header")); err == nil {
			t.Error("send() to a client that went away error = nil, want the write's error")
		}

		if w.Flushed {
			t.Error("send() flushed after a failed write")
		}
	})
}
