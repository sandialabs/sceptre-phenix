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
	ws         map[string]map[string]wsRequest
	wsRequests chan wsRequest
)

func processWebSockets() {
	ws = make(map[string]map[string]wsRequest)
	wsRequests = make(chan wsRequest)

	for req := range wsRequests {
		out := output[req.key]

		// Component is no longer running, so update and close this client.
		if !running[req.key] {
			if len(out) > 0 {
				req.ws.Write(out)
			}

			req.ws.Write([]byte("***** COMPONENT FINISHED *****"))
			close(req.done)

			continue
		}

		if _, ok := ws[req.key]; !ok {
			ws[req.key] = make(map[string]wsRequest)
		}

		ws[req.key][req.id] = req

		// If there's already data, send it to the new client.
		if len(out) > 0 {
			req.ws.Write(out)
		}
	}
}

func Start() {
	go processWebSockets()
	go processComponents()
	go processPipelines()
}
