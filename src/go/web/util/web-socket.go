package util

import (
	"context"
	"io"
	"net"

	"golang.org/x/net/websocket"

	"phenix/util/plog"
)

// Taken (almost) as-is from minimega/miniweb.

func ConnectWSHandler(endpoint string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		// Undocumented "feature" of websocket -- need to set to
		// PayloadType in order for a direct io.Copy to work.
		ws.PayloadType = websocket.BinaryFrame

		// connect to the remote host
		remote, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", endpoint) //nolint:exhaustruct // partial initialization
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
