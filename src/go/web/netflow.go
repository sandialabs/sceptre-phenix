package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"phenix/api/experiment"
	putil "phenix/util"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
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
func GetNetflow(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	if flow := experiment.GetNetflow(exp); flow != nil {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.WriteHeader(http.StatusNotFound)
}

// StartNetflow - POST /experiments/{exp}/netflow.
func StartNetflow(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	if !role.Allowed("experiments/netflow", "create", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"starting netflow capture not allowed",
			"user",
			user,
			"exp",
			exp,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := experiment.StartNetflow(exp)
	if err != nil {
		plog.Error(plog.TypeSystem, "starting netflow capture", "exp", exp, "err", err)

		if errors.Is(err, experiment.ErrNetflowAlreadyStarted) {
			http.Error(w, "neflow already started for experiment", http.StatusBadRequest)

			return
		}

		if errors.Is(err, experiment.ErrExperimentNotFound) {
			http.Error(w, "unable to find experiment", http.StatusBadRequest)

			return
		}

		if errors.Is(err, experiment.ErrExperimentNotRunning) {
			http.Error(w, "cannot start netflow on stopped experiment", http.StatusConflict)

			return
		}

		if errors.Is(err, experiment.ErrNetflowPhenixBridge) {
			http.Error(
				w,
				"cannot start netflow on experiment with default bridge set to 'phenix'",
				http.StatusConflict,
			)

			return
		}

		http.Error(w, "unable to start netflow capture", http.StatusInternalServerError)

		return
	}

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"netflow capture started",
		"user",
		user,
		"exp",
		exp,
	)
	w.WriteHeader(http.StatusNoContent)
}

// StopNetflow - DELETE /experiments/{exp}/netflow.
func StopNetflow(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["exp"]
	)

	if !role.Allowed("experiments/netflow", "delete", exp) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"stopping netflow capture not allowed",
			"user",
			user,
			"exp",
			exp,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	err := experiment.StopNetflow(exp)
	if err != nil {
		plog.Error(plog.TypeSystem, "stopping netflow capture", "exp", exp, "err", err)

		if errors.Is(err, experiment.ErrNetflowNotStarted) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		http.Error(w, "unable to stop netflow capture", http.StatusInternalServerError)

		return
	}

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"netflow capture stopped",
		"user",
		user,
		"exp",
		exp,
	)
	w.WriteHeader(http.StatusNoContent)
}

// GetNetflowWebSocket - GET /experiments/{exp}/netflow/ws.
//
//nolint:funlen // handler
func GetNetflowWebSocket(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	flow := experiment.GetNetflow(exp)
	if flow == nil {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}

	var (
		endpoint = flow.Conn.LocalAddr().String()

		id = putil.RandomString(netflowIDLength)
		cb = flow.NewChannel(id)

		upgrader = websocket.Upgrader{ //nolint:exhaustruct // partial initialization
			ReadBufferSize:  netflowBufferSize,
			WriteBufferSize: netflowBufferSize,
		}

		done = make(chan struct{})
	)

	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		plog.Error(plog.TypeSystem, "upgrading connection to WebSocket", "err", err)

		return
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

	go func() { // reader (for pong and close messages)
		defer close(done) // stop writer

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
				}
			case <-ticker.C:
				deadline := time.Now().Add(netflowWriteDeadline)

				err := conn.WriteControl(websocket.PingMessage, nil, deadline)
				if err != nil {
					plog.Error(plog.TypeSystem, "writing ping message", "client", id, "err", err)
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
}
