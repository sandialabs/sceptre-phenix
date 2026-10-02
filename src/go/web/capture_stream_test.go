package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
	}{
		"no file permission": {
			testRole("vms/captures", "list", "test-experiment/test-vm"), "0", http.StatusForbidden,
		},
		"no capture permission": {
			testRole("experiments/files", "get", "test-experiment"), "0", http.StatusForbidden,
		},
		"interface name": {sharkRole(), "eth0", http.StatusBadRequest},
		"not captured":   {sharkRole(), "1", http.StatusNotFound},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := serveAs(t, "alice", tt.role).URL

			resp := do(t, http.MethodGet,
				server+"/api/v1/experiments/test-experiment/vms/test-vm/captures/"+tt.iface+"/stream", "")
			if resp.StatusCode != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, resp.StatusCode, readBody(t, resp))
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

			expect := openCaptureStream(t, url, tt.websocket)

			expect(header, "the file header")

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
		t.Fatal(err)
	}

	t.Cleanup(tap.Release)

	deadline := time.After(5 * time.Second)

	for st := tap.State(); !st.CaughtUp; st = tap.State() {
		select {
		case <-st.Changed:
		case <-deadline:
			t.Fatal("capture was never read")
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
			t.Fatal(err)
		}

		resp.Body.Close()
		t.Cleanup(func() { conn.Close() })

		return func(want []byte, what string) {
			t.Helper()

			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

			kind, msg, err := conn.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(msg, want) {
				t.Fatalf("message: %v, type %d, %d bytes; want %s (%d bytes)", err, kind, len(msg), what, len(want))
			}
		}
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != pcapContentType {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	return func(want []byte, what string) {
		t.Helper()

		got := make([]byte, len(want))
		if _, err := io.ReadFull(resp.Body, got); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("reading %s: %v; streamed bytes differ from the capture", what, err)
		}
	}
}
