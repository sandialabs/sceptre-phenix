package util

import (
	"context"
	"io"
	"net"
	"time"

	"golang.org/x/net/websocket"

	"phenix/util/plog"
)

// proxyDialTimeout bounds connecting to the VNC or forward endpoint.
const proxyDialTimeout = 10 * time.Second

// dialProxy connects to the endpoint a websocket is proxied to; replaced in
// tests.
//
//nolint:gochecknoglobals // see above
var dialProxy = func(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{Timeout: proxyDialTimeout}).DialContext(ctx, "tcp", endpoint) //nolint:exhaustruct // partial initialization
}

// Taken (almost) as-is from minimega/miniweb.

func ConnectWSHandler(endpoint string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		// Undocumented "feature" of websocket -- need to set to
		// PayloadType in order for a direct io.Copy to work.
		ws.PayloadType = websocket.BinaryFrame

		// connect to the remote host
		// a client that leaves while connecting stops the dial
		remote, err := dialProxy(ws.Request().Context(), endpoint)
		if err != nil {
			plog.Error(plog.TypeSystem, "dialing websocket", "err", err)

			return
		}

		defer func() { _ = remote.Close() }()

		plog.Info(plog.TypeSystem, "websocket client connected", "endpoint", endpoint)

		go func() { _, _ = io.Copy(ws, remote) }()

		_, _ = io.Copy(remote, ws)

		plog.Info(plog.TypeSystem, "websocket client disconnected", "endpoint", endpoint)
	}
}
