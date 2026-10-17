package scorch

import (
	"phenix/util/plog"
)

// finishedMarker ends a component's output stream.
const finishedMarker = "***** COMPONENT FINISHED *****"

// wsRequest asks processComponents to stream a component's output to v.
type wsRequest struct {
	key string
	v   *viewer
}

var (
	ws         map[string]map[string]*viewer //nolint:gochecknoglobals // global state
	wsRequests chan wsRequest                //nolint:gochecknoglobals // global state

	basePath string //nolint:gochecknoglobals // global state
)

// addWebSocket streams a component's output to a new viewer: what it has
// written so far, then (see processComponents) each update until it finishes.
// It runs on the processComponents goroutine, which owns the output state, so
// it only queues output for the viewer.
func addWebSocket(req wsRequest) {
	v := req.v

	// the history is one chunk, so it fits the viewer's empty queue
	if out := output[req.key]; len(out) > 0 {
		v.offer(out)
	}

	// Component is no longer running, so the viewer has it all.
	if !running[req.key] {
		v.offer([]byte(finishedMarker))
		v.finish()

		return
	}

	if _, ok := ws[req.key]; !ok {
		ws[req.key] = make(map[string]*viewer)
	}

	ws[req.key][v.id] = v
}

// streamTo queues chunk for each viewer of key, forgetting those that left
// and dropping those too far behind.
func streamTo(key string, chunk []byte) {
	for id, v := range ws[key] {
		if v.isGone() {
			v.finish()
			delete(ws[key], id)

			continue
		}

		if !v.offer(chunk) {
			v.finish()
			delete(ws[key], id)

			go plog.Warn(plog.TypeSystem, "dropping SCORCH output viewer that fell behind", "component", key)
		}
	}
}

func Start(base string) {
	basePath = base

	initComponents()
	initPipelines()

	go processComponents()
	go processPipelines()
}
