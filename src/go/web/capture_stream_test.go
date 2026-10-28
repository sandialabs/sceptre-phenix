package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/livecapture"
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
