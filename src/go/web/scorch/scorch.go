package scorch

import (
	"golang.org/x/net/websocket"
)

type wsRequest struct {
	key  string
	id   string
	ws   *websocket.Conn
	done chan struct{}
}

var (
	ws         map[string]map[string]wsRequest //nolint:gochecknoglobals // global state
	wsRequests chan wsRequest                  //nolint:gochecknoglobals // global state

	basePath string //nolint:gochecknoglobals // global state
)

// addWebSocket streams a component's output to a new client: what it has
// written so far, then (see processComponents) each update until it finishes.
// It runs on the processComponents goroutine, which owns the output state.
func addWebSocket(req wsRequest) {
	out := output[req.key]

	// Component is no longer running, so update and close this client.
	if !running[req.key] {
		if len(out) > 0 {
			_, _ = req.ws.Write(out)
		}

		_, _ = req.ws.Write([]byte("***** COMPONENT FINISHED *****"))
		close(req.done)

		return
	}

	if _, ok := ws[req.key]; !ok {
		ws[req.key] = make(map[string]wsRequest)
	}

	ws[req.key][req.id] = req

	// If there's already data, send it to the new client.
	if len(out) > 0 {
		_, _ = req.ws.Write(out)
	}
}

func Start(base string) {
	basePath = base

	initComponents()
	initPipelines()

	go processComponents()
	go processPipelines()
}
