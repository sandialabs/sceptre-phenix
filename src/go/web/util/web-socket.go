package util

import (
	"io"
	"net"

	"phenix/util/plog"

	"golang.org/x/net/websocket"
)

// Taken (almost) as-is from minimega/miniweb.

func ConnectWSHandler(endpoint string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		// Undocumented "feature" of websocket -- need to set to
		// PayloadType in order for a direct io.Copy to work.
		ws.PayloadType = websocket.BinaryFrame

		// connect to the remote host
		remote, err := net.Dial("tcp", endpoint)
		if err != nil {
			plog.Error(plog.TypeSystem, "dialing websocket", "err", err)
			return
		}

		defer remote.Close()

		plog.Info(plog.TypeSystem, "websocket client connected", "endpoint", endpoint)

		go io.Copy(ws, remote)
		io.Copy(remote, ws)

		plog.Info(plog.TypeSystem, "websocket client disconnected", "endpoint", endpoint)
	}
}
