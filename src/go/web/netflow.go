package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"phenix/api/experiment"
	putil "phenix/util"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	netflowIDLength      = 24
	netflowBufferSize    = 4096
	netflowReadDeadline  = 10 * time.Second
	netflowWriteDeadline = 5 * time.Second
	netflowReadLimit     = 1024
	netflowTickerRatio   = 7
	netflowTickerDivisor = 10
)

// GetNetflow - GET /experiments/{exp}/netflow.
func GetNetflow(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	if !role.Allowed("experiments/netflow", "get", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting netflow capture not allowed",
			"user",
			user,
			"exp",
			exp,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if flow := experiment.GetNetflow(exp); flow != nil {
		w.WriteHeader(http.StatusNoContent)

		return nil
	}

	return weberror.New(http.StatusNotFound, "no netflow capture is running")
}

// StartNetflow - POST /experiments/{exp}/netflow.
func StartNetflow(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	started := audit.Fields{Experiment: exp}

	if !role.Allowed("experiments/netflow", "create", exp) {
		audit.Denied(r, audit.NetflowStart, "starting netflow capture not allowed", started)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := experiment.StartNetflow(exp, o.netflowMaxSize)
	if err != nil {
		audit.Failure(r, audit.NetflowStart, "starting netflow capture failed", started, err)
		plog.Error(plog.TypeSystem, "starting netflow capture", "exp", exp, "err", err)

		if errors.Is(err, experiment.ErrNetflowAlreadyStarted) {
			return weberror.New(http.StatusBadRequest, "netflow already started for experiment")
		}

		if errors.Is(err, experiment.ErrExperimentNotFound) {
			return weberror.New(http.StatusBadRequest, "unable to find experiment")
		}

		if errors.Is(err, experiment.ErrExperimentNotRunning) {
			return weberror.New(http.StatusConflict, "cannot start netflow on stopped experiment")
		}

		if errors.Is(err, experiment.ErrNetflowPhenixBridge) {
			return weberror.New(http.StatusConflict, "cannot start netflow on experiment with default bridge set to 'phenix'")
		}

		return weberror.New(http.StatusInternalServerError, "unable to start netflow capture")
	}

	audit.Success(r, audit.NetflowStart, "netflow capture started", started)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// StopNetflow - DELETE /experiments/{exp}/netflow.
func StopNetflow(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	stopped := audit.Fields{Experiment: exp}

	if !role.Allowed("experiments/netflow", "delete", exp) {
		audit.Denied(r, audit.NetflowStop, "stopping netflow capture not allowed", stopped)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := experiment.StopNetflow(exp)
	if err != nil {
		audit.Failure(r, audit.NetflowStop, "stopping netflow capture failed", stopped, err)
		plog.Error(plog.TypeSystem, "stopping netflow capture", "exp", exp, "err", err)

		if errors.Is(err, experiment.ErrNetflowNotStarted) {
			return weberror.New(http.StatusNotFound, "no netflow capture is running for experiment "+exp+
				": it may already have stopped")
		}

		return weberror.New(http.StatusInternalServerError, "unable to stop netflow capture")
	}

	audit.Success(r, audit.NetflowStop, "netflow capture stopped", stopped)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// GetNetflowWebSocket - GET /experiments/{exp}/netflow/ws.
//
//nolint:funlen // handler
func GetNetflowWebSocket(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	if !role.Allowed("experiments/netflow", "get", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting netflow websocket not allowed",
			"user",
			user,
			"exp",
			exp,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	flow := experiment.GetNetflow(exp)
	if flow == nil {
		return weberror.New(http.StatusNotFound, "not found")
	}

	var (
		endpoint = flow.Conn.LocalAddr().String()

		id = putil.RandomString(netflowIDLength)

		upgrader = websocket.Upgrader{ //nolint:exhaustruct // partial initialization
			ReadBufferSize:  netflowBufferSize,
			WriteBufferSize: netflowBufferSize,
			Error:           weberror.UpgradeError,
		}

		done = make(chan struct{})
		// closed at shutdown
		stopping = make(chan struct{})
	)

	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "upgrading connection to WebSocket", "err", err)

		return nil
	}

	// Register only after a successful upgrade. A failed handshake must not
	// leave a permanently slow callback in the capture fan-out.
	cb := flow.NewChannel(id)
	if cb == nil {
		_ = conn.Close()

		return nil
	}

	pongHandler := func(string) error { //nolint:unparam // signature required
		plog.Info(plog.TypeSystem, "received pong message from websocket client", "client", id)

		_ = conn.SetReadDeadline(time.Now().Add(netflowReadDeadline))

		return nil
	}

	closeHandler := func(code int, msg string) error { //nolint:unparam // signature required
		plog.Info(plog.TypeSystem, "received close message from websocket client", "client", id)

		var (
			message  = websocket.FormatCloseMessage(code, "")
			deadline = time.Now().Add(netflowWriteDeadline)
		)

		// This will be an extra write message if we initiated the close.
		_ = conn.WriteControl(websocket.CloseMessage, message, deadline)

		return nil
	}

	plog.Info(plog.TypeSystem, "ws client connected to netflow", "endpoint", endpoint, "client", id)

	// WriteControl and Close may run beside a writer stuck in WriteJSON
	defer lifecycle.Track("netflow stream", func() {
		message := websocket.FormatCloseMessage(websocket.CloseGoingAway, lifecycle.CloseReason)
		_ = conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(lifecycle.CloseWait))

		close(stopping)

		// the reader ends on the client's answering close, or here
		select {
		case <-done:
		case <-time.After(lifecycle.CloseWait):
			_ = conn.Close()
		}
	})()

	go func() { // reader (for pong and close messages)
		defer close(done) // stop writer

		_ = conn.SetReadDeadline(time.Now().Add(netflowReadDeadline))
		conn.SetPongHandler(pongHandler)
		conn.SetCloseHandler(closeHandler)
		conn.SetReadLimit(netflowReadLimit)

		expected := []int{
			websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived,
		}

		for {
			// This will error out if:
			//	1. Client does not respond with pong message in time; or
			//  2. Client sends a close message (either initiating it or
			//     responding to ours).
			// Either way, we're done here.
			//
			// NOTE: if this errors because of a pong message not being received in
			// time, the underlying socket will be closed without a WebSocket close
			// message being sent. This is probably okay since it's likely that a pong
			// wasn't received in time because the client no longer exists anyway.
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, expected...) {
					plog.Error(
						plog.TypeSystem,
						"reading websocket message",
						"client",
						id,
						"err",
						err,
					)
				}

				return
			}
		}
	}()

	go func() { // writer (for netflow, ping, and close messages)
		ticker := time.NewTicker((netflowReadDeadline * netflowTickerRatio) / netflowTickerDivisor)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-stopping:
				return
			case msg, open := <-cb:
				if !open {
					plog.Info(
						plog.TypeSystem,
						"netflow channel closed - closing websocket",
						"client",
						id,
					)

					var (
						message = websocket.FormatCloseMessage(
							websocket.CloseNormalClosure,
							"netflow stopped",
						)
						deadline = time.Now().Add(netflowWriteDeadline)
					)

					// This will (eventually) end up causing the reader to exit when it
					// receives the close message response from the client.
					_ = conn.WriteControl(websocket.CloseMessage, message, deadline)

					return
				}

				_ = conn.SetWriteDeadline(time.Now().Add(netflowWriteDeadline))

				err := conn.WriteJSON(msg)
				if err != nil {
					plog.Error(plog.TypeSystem, "writing netflow message", "client", id, "err", err)
					_ = conn.Close()

					return
				}
			case <-ticker.C:
				deadline := time.Now().Add(netflowWriteDeadline)

				err := conn.WriteControl(websocket.PingMessage, nil, deadline)
				if err != nil {
					plog.Error(plog.TypeSystem, "writing ping message", "client", id, "err", err)
					_ = conn.Close()

					return
				}
			}
		}
	}()

	<-done // wait for reader to be done

	_ = conn.Close()

	flow.DeleteChannel(id)

	plog.Info(
		plog.TypeSystem,
		"ws client disconnected from netflow",
		"endpoint",
		endpoint,
		"client",
		id,
	)

	return nil
}

// netflowCaptureTypes are the Content-Type and file extension of each format
// a recorded netflow capture downloads as.
var netflowCaptureTypes = map[string][2]string{ //nolint:gochecknoglobals // constant table
	experiment.NetflowFormatText:  {"text/plain; charset=utf-8", ".txt"},
	experiment.NetflowFormatCSV:   {"text/csv; charset=utf-8; header=present", ".csv"},
	experiment.NetflowFormatJSONL: {"application/x-ndjson", ".jsonl"},
}

// GetNetflowCaptures - GET /experiments/{exp}/netflow/captures.
func GetNetflowCaptures(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		exp  = mux.Vars(r)["exp"]
	)

	if !role.Allowed("experiments/netflow", "get", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(plog.TypeSecurity, "listing netflow captures not allowed", "user", user, "exp", exp)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	captures, err := experiment.NetflowCaptures(exp)
	if err != nil {
		if errors.Is(err, experiment.ErrExperimentNotFound) {
			return weberror.New(http.StatusNotFound, "experiment "+exp+" not found")
		}

		plog.Error(plog.TypeSystem, "listing netflow captures", "exp", exp, "err", err)

		return weberror.New(http.StatusInternalServerError, "unable to list netflow captures")
	}

	body, err := json.Marshal(map[string]any{"captures": captures})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "unable to list netflow captures")
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// DownloadNetflowCapture - GET|HEAD /experiments/{exp}/netflow/captures/{id}?format=.
//
// Text is the recorded file, resumable; CSV and JSONL are converted as they
// are sent, like the files zip. A live capture converts as it was when opened.
func DownloadNetflowCapture(w http.ResponseWriter, r *http.Request) error {
	var (
		role   = middleware.RoleFromContext(r.Context())
		vars   = mux.Vars(r)
		exp    = vars["exp"]
		id     = vars["id"]
		format = r.URL.Query().Get("format")
		head   = r.Method == http.MethodHead
	)

	if format == "" {
		format = experiment.NetflowFormatText
	}

	fields := audit.Fields{
		Experiment: exp, File: "netflow/" + id + ".txt",
		Extra: []any{"phenix.netflow.capture", id, "phenix.netflow.format", format},
	}

	if !role.Allowed("experiments/netflow", "get", exp) {
		if !head {
			audit.Denied(r, audit.NetflowDownload, "downloading netflow capture not allowed", fields)
		}

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	fail := func(status int, msg string, err error) error {
		if !head {
			audit.Failure(r, audit.NetflowDownload, "downloading netflow capture failed", fields, err)
		}

		return weberror.New(status, msg)
	}

	kind, ok := netflowCaptureTypes[format]
	if !ok {
		return fail(http.StatusBadRequest, "format must be text, csv or jsonl", experiment.ErrInvalidNetflowFormat)
	}

	f, capture, err := experiment.OpenNetflowCapture(exp, id)
	if err != nil {
		switch {
		case errors.Is(err, experiment.ErrInvalidNetflowCapture):
			return fail(http.StatusBadRequest, "invalid netflow capture id", err)
		case errors.Is(err, experiment.ErrExperimentNotFound):
			return fail(http.StatusNotFound, "experiment "+exp+" not found", err)
		case errors.Is(err, experiment.ErrNetflowCaptureNotFound):
			return fail(http.StatusNotFound, "netflow capture "+id+" not found", err)
		default:
			plog.Error(plog.TypeSystem, "opening netflow capture", "exp", exp, "id", id, "err", err)

			return fail(http.StatusInternalServerError, "unable to open netflow capture", err)
		}
	}

	defer func() { _ = f.Close() }()

	d := util.Download{Path: f.Name(), Name: exp + "-netflow-" + id + kind[1], ContentType: kind[0], MaxSize: 0}

	if format == experiment.NetflowFormatText {
		return sendNetflowText(w, r, f, d, fields)
	}

	return sendNetflowExport(w, r, io.LimitReader(f, capture.Size), d, format, fields)
}

// sendNetflowText sends a recorded capture's file as it is, resumably.
func sendNetflowText(w http.ResponseWriter, r *http.Request, f *os.File, d util.Download, fields audit.Fields) error {
	served, err := util.ServeOpenFile(w, r, f, d)
	if err != nil {
		plog.Error(plog.TypeSystem, "sending netflow capture", "exp", fields.Experiment, "file", fields.File, "err", err)

		if r.Method != http.MethodHead {
			audit.Failure(r, audit.NetflowDownload, "downloading netflow capture failed", fields, err)
		}

		return weberror.New(http.StatusInternalServerError, "unable to read netflow capture")
	}

	// once per download, not per resumed range or HEAD
	if served.Started {
		if rng := r.Header.Get("Range"); rng != "" {
			fields.Extra = append(fields.Extra, "http.request.range", rng)
		}

		audit.Success(r, audit.NetflowDownload, "downloaded netflow capture", fields)
	}

	return nil
}

// sendNetflowExport sends a capture's lines from src converted to format, as
// they are converted; HEAD sends only the headers.
func sendNetflowExport(
	w http.ResponseWriter, r *http.Request, src io.Reader, d util.Download, format string, fields audit.Fields,
) error {
	start := func() io.Writer {
		header := w.Header()
		header.Set("Content-Type", d.ContentType)
		header.Set("Content-Disposition", util.Attachment(d.Name))
		header.Set("X-Content-Type-Options", "nosniff")
		// built as it is sent, so it cannot be resumed or cached
		header.Set("Accept-Ranges", "none")
		header.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)

		return w
	}

	if r.Method == http.MethodHead {
		start()

		return nil
	}

	var started bool

	exported, err := experiment.ExportNetflow(r.Context(), func() io.Writer {
		started = true

		return start()
	}, src, format)

	fields.Extra = append(fields.Extra, "phenix.netflow.flows", exported.Flows, "phenix.netflow.skipped", exported.Skipped)

	if err != nil {
		audit.Failure(r, audit.NetflowDownload, "downloading netflow capture failed", fields, err)

		if started {
			// aborting tells the client the export is incomplete
			plog.Error(plog.TypeSystem, "streaming netflow capture", "exp", fields.Experiment, "file", fields.File, "err", err)
			panic(http.ErrAbortHandler)
		}

		return weberror.New(http.StatusInternalServerError, "unable to export netflow capture")
	}

	audit.Success(r, audit.NetflowDownload, "downloaded netflow capture", fields)

	return nil
}
