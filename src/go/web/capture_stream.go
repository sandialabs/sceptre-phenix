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
	"phenix/web/audit"
	"phenix/web/capability"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	captureStreamWriteDeadline = 10 * time.Second
	captureStreamPingInterval  = 30 * time.Second
	captureStreamReadDeadline  = 2 * captureStreamPingInterval
)

// removeStaleCaptureSpools deletes the spools of remote captures that a
// previous run of the server, stopped or crashed while a capture was open,
// left in the temporary directory.
func removeStaleCaptureSpools() {
	removed, err := livecapture.RemoveStaleSpools(time.Now())
	if err != nil {
		plog.Warn(plog.TypeSystem, "removing stale capture spools", "err", err)
	}

	if removed > 0 {
		plog.Info(plog.TypeSystem, "removed stale capture spools", "count", removed)
	}
}

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
// Streams a running capture as a pcap file (with ?from=now, only packets from now on) until it stops: a chunked body
// Wireshark reads from a pipe, or for a websocket, binary messages holding the file header and then whole packets.
func GetVMCaptureStream(w http.ResponseWriter, r *http.Request) error {
	var (
		role    = middleware.RoleFromContext(r.Context())
		vars    = mux.Vars(r)
		exp     = vars["exp"]
		name    = vars["name"]
		fromNow = r.URL.Query().Get("from") == "now"
	)

	if !captureReadAllowed(role, exp, name) {
		audit.Denied(r, audit.CaptureStream, "streaming VM capture not allowed", audit.Fields{Experiment: exp, VM: name})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	iface, err := strconv.Atoi(vars["iface"])
	if err != nil || iface < 0 {
		return weberror.New(http.StatusBadRequest, "interface must be an interface index")
	}

	fields := audit.Fields{Experiment: exp, VM: name, Interface: &iface}

	if websocket.IsWebSocketUpgrade(r) && lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	tap, err := openCaptureTap(r, fields)
	if err != nil {
		return err
	}

	defer tap.Release()

	audit.Success(r, audit.CaptureStream, "streaming VM capture", fields)

	if websocket.IsWebSocketUpgrade(r) {
		streamCaptureWebSocket(w, r, tap, fromNow)

		return nil
	}

	streamCapture(w, r, tap, fromNow)

	return nil
}

// GetCaptureStreamByToken - GET /webshark/streams/{token}
//
// Streams the running capture a stream grant names, as GetVMCaptureStream does; the grant is the credential. It ends with
// the grant, or when its user may no longer read the capture.
func (c grantChecker) GetCaptureStreamByToken(w http.ResponseWriter, r *http.Request) error {
	g, status := c.use(r, mux.Vars(r)["token"], capability.OpStream)
	if status != 0 {
		return weberror.New(status, http.StatusText(status))
	}

	fields := grantAuditFields(r, g, capability.OpStream)

	tap, err := openCaptureTap(r, fields)
	if err != nil {
		return err
	}

	defer tap.Release()

	audit.Success(r, audit.CaptureStream, "streaming VM capture", fields)

	ctx, cancel := c.untilRevoked(r.Context(), g)
	defer cancel()

	streamCapture(w, r.WithContext(ctx), tap, r.URL.Query().Get("from") == "now")

	return nil
}

// openCaptureTap starts following the running capture of the VM interface
// fields name, for request r, or returns the error to answer when it cannot.
func openCaptureTap(r *http.Request, fields audit.Fields) (*livecapture.Tap, error) {
	exp, vm, iface := fields.Experiment, fields.VM, *fields.Interface

	tap, err := livecapture.Open(exp, vm, iface)
	if err != nil {
		// not capturing included: it was an attempt to read the capture
		audit.Failure(r, audit.CaptureStream, "opening VM capture stream failed", fields, err)

		status := captureStreamStatus(err)
		if status == http.StatusInternalServerError {
			plog.Error(plog.TypeSystem, "opening VM capture stream", "exp", exp, "vm", vm, "interface", iface, "err", err)
		}

		return nil, weberror.New(status, err.Error())
	}

	return tap, nil
}

// streamCapture sends the capture as a chunked pcap body, until it stops or
// the request's context ends.
func streamCapture(w http.ResponseWriter, r *http.Request, tap *livecapture.Tap, fromNow bool) {
	ctx, release := lifecycle.WithStop(r.Context(), "capture stream")
	defer release()

	capture := tap.Capture()

	w.Header().Set("Content-Type", pcapContentType)
	w.Header().Set("Content-Disposition", util.Attachment(fmt.Sprintf("%s-%d.pcap", capture.VM, capture.Interface)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // proxies must pass packets on as they come
	w.WriteHeader(http.StatusOK)

	err := tap.Stream(ctx, fromNow, pcapSender(w))
	if err != nil && ctx.Err() == nil {
		// the status is gone; the client sees the stream end early
		plog.Warn(
			plog.TypeSystem, "streaming VM capture",
			"exp", capture.Exp, "vm", capture.VM, "interface", capture.Interface, "err", err,
		)
	}
}

// pcapSender returns what sends each chunk of a capture stream as it comes.
func pcapSender(w http.ResponseWriter) func([]byte) error {
	rc := http.NewResponseController(w)

	return func(b []byte) error {
		// a client that stops reading ends the stream, letting go of the capture
		_ = rc.SetWriteDeadline(time.Now().Add(captureStreamWriteDeadline))

		if _, err := w.Write(b); err != nil {
			return err //nolint:wrapcheck // the client went away
		}

		return rc.Flush() //nolint:wrapcheck // the client went away
	}
}

// streamCaptureWebSocket sends the capture as binary websocket messages. The
// client only ever sends control messages; closing the socket ends the
// stream.
func streamCaptureWebSocket(w http.ResponseWriter, r *http.Request, tap *livecapture.Tap, fromNow bool) {
	upgrader := websocket.Upgrader{ //nolint:exhaustruct // partial initialization
		CheckOrigin: func(*http.Request) bool { return true },
		Error:       weberror.UpgradeError,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "upgrading capture stream to websocket", "err", err)

		return
	}

	defer conn.Close()

	stopCtx, release := lifecycle.WithStop(r.Context(), "capture stream")
	defer release()

	ctx, cancel := context.WithCancel(stopCtx)
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

	switch {
	case lifecycle.Stopped(stopCtx):
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, lifecycle.CloseReason),
			time.Now().Add(lifecycle.CloseWait),
		)
	case ctx.Err() == nil:
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
