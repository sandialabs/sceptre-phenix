package scorch

import (
	"sync"

	"golang.org/x/net/websocket"
)

type wsRequest struct {
	key  string
	id   string
	ws   *websocket.Conn
	done chan struct{}
}

var (
	componentMu sync.Mutex                      //nolint:gochecknoglobals // lock shared component streams
	ws          map[string]map[string]wsRequest //nolint:gochecknoglobals // global state
	wsRequests  chan wsRequest                  //nolint:gochecknoglobals // global state

	basePath string //nolint:gochecknoglobals // global state
)

func processWebSockets() {
	for req := range wsRequests {
		componentMu.Lock()
		out := output[req.key]

		// Component is no longer running, so update and close this client.
		if !running[req.key] {
			if len(out) > 0 {
				_, _ = req.ws.Write(out)
			}

			_, _ = req.ws.Write([]byte("***** COMPONENT FINISHED *****"))
			close(req.done)

			componentMu.Unlock()
			continue
		}

		if _, ok := ws[req.key]; !ok {
			ws[req.key] = make(map[string]wsRequest)
		}

		ws[req.key][req.id] = req

		// If there's already data, send it to the new client.
		if len(out) > 0 {
			_, _ = req.ws.Write(out)
		}
		componentMu.Unlock()
	}
}

func Start(base string) {
	basePath = base
	ws = make(map[string]map[string]wsRequest)
	wsRequests = make(chan wsRequest)
	componentUpdates = make(chan ComponentUpdate)
	outputRequests = make(chan outputRequest)
	cmpType = make(map[string]string)
	running = make(map[string]bool)
	output = make(map[string][]byte)
	pipelines = make(map[string]map[int]map[int]*pipeline)
	pipelineUpdates = make(chan PipelineUpdate)
	pipelineRequests = make(chan pipelineRequest)
	pipelineDeletes = make(chan pipelineDelete)

	go processWebSockets()
	go processComponents()
	go processPipelines()
}
