package util

import (
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"

	"phenix/util/plog/plogtest"
)

// echoServer accepts TCP connections on loopback and echoes what each sends.
func echoServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	return ln.Addr().String()
}

// dialProxy opens a websocket to a ConnectWSHandler for endpoint.
func dialProxy(t *testing.T, endpoint string) *websocket.Conn {
	t.Helper()

	srv := httptest.NewServer(websocket.Handler(ConnectWSHandler(endpoint)))
	t.Cleanup(srv.Close)

	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), "", srv.URL)
	if err != nil {
		t.Fatalf("dialing the websocket proxy: %v", err)
	}

	t.Cleanup(func() { _ = ws.Close() })

	return ws
}

//nolint:paralleltest // captures the process-wide log
func TestConnectWSHandlerCopiesBothWays(t *testing.T) {
	logs := plogtest.Capture(t)
	ws := dialProxy(t, echoServer(t))

	if _, err := ws.Write([]byte("vnc bytes")); err != nil {
		t.Fatalf("writing to the proxy: %v", err)
	}

	got := make([]byte, len("vnc bytes"))
	if _, err := io.ReadFull(ws, got); err != nil {
		t.Fatalf("reading the echo: %v", err)
	}

	if string(got) != "vnc bytes" {
		t.Errorf("proxy echoed %q, want %q", got, "vnc bytes")
	}

	if n := len(logs.Records(t, plogtest.Message("websocket client connected"))); n != 1 {
		t.Errorf("logged %d connected records, want 1", n)
	}
}

//nolint:paralleltest // captures the process-wide log
func TestConnectWSHandlerClosesWhenTheEndpointRefuses(t *testing.T) {
	logs := plogtest.Capture(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	closed := ln.Addr().String()
	_ = ln.Close()

	ws := dialProxy(t, closed)

	if _, err := ws.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("reading from a proxy whose endpoint refused = %v, want EOF", err)
	}

	if n := len(logs.Records(t, plogtest.Message("dialing websocket"))); n != 1 {
		t.Errorf("logged %d dial errors, want 1", n)
	}
}
