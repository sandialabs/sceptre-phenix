package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"phenix/api/livecapture"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

const (
	captureStreamWriteDeadline = 10 * time.Second
	captureStreamPingInterval  = 30 * time.Second
	captureStreamReadDeadline  = 2 * captureStreamPingInterval
)

// pcapContentType is the registered media type of a pcap file.
const pcapContentType = "application/vnd.tcpdump.pcap"

// captureReadAllowed reports whether the role may read the packets of a
// VM's capture: seeing that it exists (vms/captures list) and reading the
// experiment's files it is written to (experiments/files get).
func captureReadAllowed(role rbac.Role, exp, vm string) bool {
	return role.Allowed("vms/captures", "list", exp+"/"+vm) &&
		role.Allowed("experiments/files", "get", exp)
}

// captureStreamStatus is the HTTP status for failing to open a capture.
func captureStreamStatus(err error) int {
	switch {
	case errors.Is(err, livecapture.ErrNotCapturing):
		return http.StatusNotFound
	case errors.Is(err, livecapture.ErrUnsafePath):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// GetVMCaptureStream - GET /experiments/{exp}/vms/{name}/captures/{iface}/stream
//
// Streams a running capture as a pcap file: everything captured so far (or,
// with ?from=now, nothing captured before the request), then each packet as
// minimega writes it, until the capture stops. A plain request gets a
// chunked pcap body, which Wireshark reads from a pipe (curl ... | wireshark
// -k -i -); a websocket request gets the same bytes as binary messages, the
// first holding the file header and each later one whole packets.
func GetVMCaptureStream(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role    = middleware.RoleFromContext(ctx)
		user    = middleware.UserFromContext(ctx)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
		name    = vars["name"]
		fromNow = r.URL.Query().Get("from") == "now"
	)

	if !captureReadAllowed(role, exp, name) {
		plog.Warn(plog.TypeSecurity, "streaming VM capture not allowed", "user", user, "exp", exp, "vm", name)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	iface, err := strconv.Atoi(vars["iface"])
	if err != nil || iface < 0 {
		http.Error(w, "interface must be an interface index", http.StatusBadRequest)

		return
	}

	tap, err := livecapture.Open(exp, name, iface)
	if err != nil {
		status := captureStreamStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "opening VM capture stream", "exp", exp, "vm", name, "interface", iface, "err", err)
		}

		http.Error(w, err.Error(), status)

		return
	}

	defer tap.Release()

	plog.Info(plog.TypeAction, "streaming VM capture", "user", user, "exp", exp, "vm", name, "interface", iface)

	if websocket.IsWebSocketUpgrade(r) {
		streamCaptureWebSocket(w, r, tap, fromNow)

		return
	}

	w.Header().Set("Content-Type", pcapContentType)
	w.Header().Set("Content-Disposition", util.Attachment(fmt.Sprintf("%s-%d.pcap", name, iface)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // proxies must pass packets on as they come
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)

	err = tap.Stream(ctx, fromNow, func(b []byte) error {
		if _, err := w.Write(b); err != nil {
			return err //nolint:wrapcheck // the client went away
		}

		return rc.Flush() //nolint:wrapcheck // the client went away
	})
	if err != nil && ctx.Err() == nil {
		// the status is gone; the client sees the stream end early
		plog.Warn(plog.TypeSystem, "streaming VM capture", "exp", exp, "vm", name, "interface", iface, "err", err)
	}
}

// streamCaptureWebSocket sends the capture as binary websocket messages. The
// client only ever sends control messages; closing the socket ends the
// stream.
func streamCaptureWebSocket(w http.ResponseWriter, r *http.Request, tap *livecapture.Tap, fromNow bool) {
	upgrader := websocket.Upgrader{ //nolint:exhaustruct // partial initialization
		CheckOrigin: func(*http.Request) bool { return true },
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "upgrading capture stream to websocket", "err", err)

		return
	}

	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() { // reader: pongs and close
		defer cancel()

		conn.SetReadLimit(maxControlMessage)
		_ = conn.SetReadDeadline(time.Now().Add(captureStreamReadDeadline))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(captureStreamReadDeadline))
		})

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pings := time.NewTicker(captureStreamPingInterval)
	defer pings.Stop()

	go func() { // pinger: keeps idle captures' sockets open through proxies
		for {
			select {
			case <-ctx.Done():
				return
			case <-pings.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(captureStreamWriteDeadline))
			}
		}
	}()

	err = tap.Stream(ctx, fromNow, func(b []byte) error {
		_ = conn.SetWriteDeadline(time.Now().Add(captureStreamWriteDeadline))

		return conn.WriteMessage(websocket.BinaryMessage, b) //nolint:wrapcheck // the client went away
	})

	reason := "capture stopped"
	if err != nil {
		reason = "capture stream failed"
	}

	if ctx.Err() == nil {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason),
			time.Now().Add(captureStreamWriteDeadline),
		)
	}
}

// maxControlMessage bounds what a capture stream client may send, which is
// only ever websocket control messages.
const maxControlMessage = 512
