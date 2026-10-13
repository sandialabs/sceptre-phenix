package util

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// The proxy dials its endpoint with the client's request context, so a
// client that leaves stops the dial, and the dialer gives up on its own after
// proxyDialTimeout.
//
//nolint:paralleltest // replaces the proxy's dialer
func TestProxyDialFollowsTheRequest(t *testing.T) {
	saved := dialProxy

	t.Cleanup(func() { dialProxy = saved })

	dialed := make(chan context.Context, 1)

	dialProxy = func(ctx context.Context, _ string) (net.Conn, error) {
		dialed <- ctx

		return nil, errors.New("refused")
	}

	srv := httptest.NewServer(websocket.Handler(ConnectWSHandler("vm:5900")))
	t.Cleanup(srv.Close)

	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ws.Close() })

	select {
	case ctx := <-dialed:
		if ctx.Done() == nil {
			t.Error("dialed with a context that is never done")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the proxy did not dial")
	}
}
