package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/web/broker"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/gofrs/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
)

type termClient struct {
	id   string
	ws   *websocket.Conn
	done chan struct{}
}

func newTermClient(ws *websocket.Conn) termClient {
	return termClient{
		id:   uuid.Must(uuid.NewV4()).String(),
		ws:   ws,
		done: make(chan struct{}),
	}
}

var (
	rwTerm  = make(map[int]string)
	roTerms = make(map[int]map[string]termClient)
	history = make(map[int]bytes.Buffer)

	termClientIDs = make(map[string]chan struct{})

	cancelers = make(map[string]context.CancelFunc)

	mu sync.Mutex
)

// GET /experiments/{name}/scorch/terminals
func GetTerminals(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetTerminal HTTP handler called")

	var (
		vars = mux.Vars(r)
		exp  = vars["name"]
	)

	terms, _ := GetExperimentTerminals(exp, -1)

	body, _ := json.Marshal(util.WithRoot("terminals", terms))
	w.Write(body)
}

// GET /experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}
func ConnectTerminal(w http.ResponseWriter, r *http.Request) {
	log.Debug("ConnectTerminal HTTP handler called")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		http.Error(w, "invalid run ID provided", http.StatusBadRequest)
		return
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		http.Error(w, "invalid loop number provided", http.StatusBadRequest)
		return
	}

	t, err := initTerminal(exp, run, loop, stage, cmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, _ := json.Marshal(t)
	w.Write(body)
}

// GET /experiments/{name}/scorch/terminals/{pid}/ws/{id}
func StreamTerminal(w http.ResponseWriter, r *http.Request) {
	log.Debug("StreamTerminal HTTP handler called")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])

	t, err := GetTerminalByPID(pid)
	if err != nil {
		http.Error(w, "no web terminal found", http.StatusNotFound)
		return
	}

	if t.Exp != exp {
		http.Error(w, "no web terminal found", http.StatusNotFound)
		return
	}

	id := mux.Vars(r)["id"]

	mu.Lock()
	done, ok := termClientIDs[id]
	mu.Unlock()

	if !ok {
		http.Error(w, "terminal client ID invalid", http.StatusNotFound)
		return
	}

	close(done)

	t.RO = rwTerm[pid] != id

	log.Debug("starting web terminal streamer for PID %d", pid)

	websocket.Handler(terminalWsHandler(t)).ServeHTTP(w, r)
}

// POST /experiments/{name}/scorch/terminals/{pid}/exit/{id}
func ExitTerminal(w http.ResponseWriter, r *http.Request) {
	log.Debug("ExitTerminal HTTP handler called")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])
	id := mux.Vars(r)["id"]

	if rwTerm[pid] != id {
		log.Error("terminal client with ID %s doesn't own R/W rights to PTY %d", id, pid)
		http.Error(w, "terminal client not allowed to exit terminal", http.StatusForbidden)
		return
	}

	t, err := GetTerminalByPID(pid)
	if err != nil {
		log.Error("web terminal for PID %d not found", pid)
		http.Error(w, "web terminal not found", http.StatusNotFound)
		return
	}

	if t.Exp != exp {
		http.Error(w, "no web terminal found", http.StatusNotFound)
		return
	}

	if err := KillTerminal(t); err != nil {
		log.Error("killing terminal for PID %d: %v", pid, err)
		http.Error(w, "error exiting terminal", http.StatusNotFound)
		return
	}

	mu.Lock()
	delete(history, pid)
	mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func terminalWsHandler(t WebTerm) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		_, err := os.FindProcess(t.Pid)
		if err != nil {
			log.Error("unable to find process: %v", t.Pid)
			return
		}

		readTerm := func() {
			for {
				buf := make([]byte, 32*1024)

				nr, err := t.Pty.Read(buf)
				if err != nil {
					break
				}

				nw, err := ws.Write(buf[0:nr])
				if err != nil {
					break
				}

				if nw != nr {
					break
				}

				mu.Lock()
				h := history[t.Pid]
				h.Write(buf[0:nr])
				history[t.Pid] = h

				ro := roTerms[t.Pid]
				mu.Unlock()

				for _, t := range ro {
					nw, err := t.ws.Write(buf[0:nr])
					if err != nil {
						close(t.done)
						break
					}

					if nw != nr {
						close(t.done)
						break
					}
				}
			}
		}

		writeTerm := func() {
			for {
				buf := make([]byte, 32*1024)

				nr, err := ws.Read(buf)
				if err != nil {
					break
				}

				nw, err := t.Pty.Write(buf[0:nr])
				if err != nil {
					break
				}

				if nw != nr {
					break
				}
			}
		}

		waitTerm := func(tc termClient) {
			select {
			case <-tc.done:
				mu.Lock()
				terms := roTerms[t.Pid]
				delete(terms, tc.id)
				roTerms[t.Pid] = terms
				mu.Unlock()
			case <-t.Done:
				mu.Lock()
				for _, ro := range roTerms[t.Pid] {
					// notify read-only clients that R/W terminal has exited
					ro.ws.Write([]byte("***** BREAK PROCESS EXITED *****"))
				}
				delete(roTerms, t.Pid)
				mu.Unlock()
			}
		}

		if t.RO {
			tc := newTermClient(ws)

			mu.Lock()

			terms, ok := roTerms[t.Pid]
			if !ok {
				terms = make(map[string]termClient)
			}

			terms[tc.id] = tc
			roTerms[t.Pid] = terms

			if h, ok := history[t.Pid]; ok {
				tc.ws.Write(h.Bytes())
			}

			mu.Unlock()

			waitTerm(tc)
		} else {
			go readTerm()
			writeTerm()

			mu.Lock()
			delete(rwTerm, t.Pid)
			mu.Unlock()
		}
	}
}

func initTerminal(exp string, run, loop int, stage, cmp string) (WebTerm, error) {
	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)

	t, err := GetTerminalByExperiment(key)
	if err != nil {
		return WebTerm{}, fmt.Errorf("no web terminal found")
	}

	if t.Exp != exp {
		return WebTerm{}, fmt.Errorf("no web terminal found")
	}

	id := uuid.Must(uuid.NewV4()).String()
	t.Loc = fmt.Sprintf("%sapi/v1/experiments/%s/scorch/terminals/%d/ws/%s", basePath, exp, t.Pid, id)

	mu.Lock()
	defer mu.Unlock()

	if _, ok := rwTerm[t.Pid]; ok {
		t.RO = true
	} else {
		rwTerm[t.Pid] = id
		t.Exit = fmt.Sprintf("%sapi/v1/experiments/%s/scorch/terminals/%d/exit/%s", basePath, exp, t.Pid, id)
	}

	done := make(chan struct{})
	termClientIDs[id] = done

	go func() {
		select {
		case <-time.After(5 * time.Second):
			mu.Lock()
			delete(rwTerm, t.Pid)
			delete(termClientIDs, id)
			mu.Unlock()
		case <-done:
			mu.Lock()
			delete(termClientIDs, id)
			mu.Unlock()
		}
	}()

	return t, nil
}

// GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}
func GetComponentOutput(w http.ResponseWriter, r *http.Request) error {
	log.Debug("GetScorchComponentOutput HTTP handler called")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		return weberror.NewWebError(err, "invalid loop number provided")
	}

	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)
	req := outputRequest{key: key, resp: make(chan outputResponse)}

	outputRequests <- req
	resp := <-req.resp

	if resp.running {
		if resp.terminal {
			t, err := initTerminal(exp, run, loop, stage, cmp)
			if err != nil {
				return weberror.NewWebError(err, "unable to initialize terminal")
			}

			body, _ := json.Marshal(util.WithRoot("terminal", t))

			w.Header().Set("Content-Type", "application/json")
			w.Write(body)

			return nil
		}

		body, _ := json.Marshal(util.WithRoot("stream", fmt.Sprintf("/api/v1/experiments/%s/scorch/components/%d/%d/%s/%s/ws", exp, run, loop, stage, cmp)))

		w.Header().Set("Content-Type", "application/json")
		w.Write(body)

		return nil
	}

	body, err := json.Marshal(util.WithRoot("output", string(resp.output)))
	if err != nil {
		err := weberror.NewWebError(err, "unable to process component %s output", cmp)
		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}

// GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}/ws
func StreamComponentOutput(w http.ResponseWriter, r *http.Request) {
	log.Debug("StreamScorchComponentOutput HTTP handler called")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		http.Error(w, "invalid run ID provided", http.StatusBadRequest)
		return
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		http.Error(w, "invalid loop number provided", http.StatusBadRequest)
		return
	}

	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)

	log.Debug("starting scorch component streamer for %s", key)

	websocket.Handler(scorchComponentWsHandler(key)).ServeHTTP(w, r)
}

func scorchComponentWsHandler(key string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		id := uuid.Must(uuid.NewV4()).String()
		req := wsRequest{key: key, id: id, ws: ws, done: make(chan struct{})}

		// add client to list for component
		wsRequests <- req
		// wait for done channel to be closed
		<-req.done
	}
}

// GET /experiments/{name}/scorch/pipelines
func GetPipelines(w http.ResponseWriter, r *http.Request) error {
	log.Debug("GetPipelines HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "get", name) {
		err := weberror.NewWebError(nil, "getting experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	md, err := scorchmd.DecodeMetadata(exp)
	if err != nil {
		err := weberror.NewWebError(err, "unable to decode scorch metadata for experiment %s", name)
		return err.SetStatus(http.StatusInternalServerError)
	}

	var pipelines []*pipeline

	for run := range md.Runs {
		pipeline, err := RequestPipeline(name, run, 0)
		if err != nil {
			return weberror.NewWebError(err, "unable to get pipeline %d for experiment %s", run, name)
		}

		pipelines = append(pipelines, pipeline)
	}

	var running bool

	// first make sure Scorch app is running
	for app, status := range exp.Status.AppRunning() {
		if app == "scorch" && status {
			running = true
			break
		}
	}

	runID := -1

	// if Scorch app is running, find out which run is currently being executed
	if running {
		// TODO: this should never be nil if Scorch is running...
		if exp.Status.AppStatus() != nil {
			if status, ok := exp.Status.AppStatus()["scorch"].(map[string]interface{}); ok {
				if id, ok := status["runID"].(float64); ok {
					runID = int(id)
				}
			}
		}
	}

	body, _ := json.Marshal(map[string]interface{}{"pipelines": pipelines, "running": runID})

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}

// GET /experiments/{name}/scorch/pipelines/{run}/{loop}
func GetPipeline(w http.ResponseWriter, r *http.Request) error {
	log.Debug("GetPipeline HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		exp  = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		return weberror.NewWebError(err, "invalid loop number '%s' provided", vars["loop"])
	}

	if !role.Allowed("experiments", "get", exp) {
		err := weberror.NewWebError(nil, "getting experiment %s not allowed for %s", exp, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	pipeline, err := RequestPipeline(exp, run, loop)
	if err != nil {
		return weberror.NewWebError(err, "unable to get pipeline %d for experiment %s", run, exp)
	}

	body, _ := json.Marshal(pipeline)

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}

// TODO: change this to `scorch/runs`

// POST /experiments/{name}/scorch/pipelines/{run}
func StartPipeline(w http.ResponseWriter, r *http.Request) error {
	log.Debug("StartPipeline HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	if !role.Allowed("experiments/trigger", "create", name) {
		err := weberror.NewWebError(nil, "starting Scorch runs for experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	if !role.Allowed("experiments", "get", name) {
		err := weberror.NewWebError(nil, "getting experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	key := fmt.Sprintf("%s/%d", name, run)

	// protect `cancelers` map
	mu.Lock()
	defer mu.Unlock()

	// TODO (btr): we some how got stuck here at least once where a scorch run was
	// started, then the experiment was killed, but the scorch run key stayed in
	// the cancelers map. I'm still not entirely sure how this could happen, but
	// if the mutex lock isn't blocked then we could do something like trigger
	// reaping of scorch runs for experiments that have been stopped. We could
	// also base the cancel context for a scorch run off the cancel context for
	// the experiment, but in order to do this we'll need to refactor code to
	// avoid an import loop.

	if _, ok := cancelers[key]; ok {
		return weberror.NewWebError(nil, "Scorch run already executing for experiment %s", name)
	}

	// We don't want to use the HTTP request's context here.
	ctx, cancel := context.WithCancel(context.Background())
	ctx = app.SetContextTriggerUI(ctx)
	cancelers[key] = cancel

	go func() {
		log.Debug("executing Scorch run %d for experiment %s", run, name)

		broker.Broadcast(
			broker.NewRequestPolicy("experiments/trigger", "create", name),
			broker.NewResource("apps/scorch", key, "start"),
			nil,
		)

		if err := scorchexe.Execute(ctx, exp, run); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Error("executing Scorch run %d for experiment %s: %v", run, name, err)

				broker.Broadcast(
					broker.NewRequestPolicy("experiments/trigger", "create", name),
					broker.NewResource("apps/scorch", key, "error"),
					[]byte(fmt.Sprintf(`{"error": "failed to execute Scorch run %d for experiment %s"}`, run, name)),
				)
			}
		} else {
			log.Debug("Scorch run %d for experiment %s executed successfully", run, name)

			broker.Broadcast(
				broker.NewRequestPolicy("experiments/trigger", "create", name),
				broker.NewResource("apps/scorch", key, "success"),
				nil,
			)
		}

		// protect `cancelers` map
		mu.Lock()
		defer mu.Unlock()

		// Ensure context is canceled to avoid leakage. It's okay to call the
		// `cancel` function multiple times. It's a no-op after the first time it's
		// called.
		cancel()
		delete(cancelers, key)
	}()

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// TODO: change this to `scorch/runs`

// DELETE /experiments/{name}/scorch/pipelines/{run}
func CancelPipeline(w http.ResponseWriter, r *http.Request) error {
	log.Debug("CancelPipeline HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	if !role.Allowed("experiments/trigger", "delete", name) {
		err := weberror.NewWebError(nil, "canceling Scorch runs for experiment %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	key := fmt.Sprintf("%s/%d", name, run)

	// protect `cancelers` map
	mu.Lock()
	defer mu.Unlock()

	if cancel, ok := cancelers[key]; ok {
		log.Debug("canceling Scorch run %d for experiment %s", run, name)

		cancel()
		delete(cancelers, key)

		broker.Broadcast(
			broker.NewRequestPolicy("experiments/trigger", "delete", name),
			broker.NewResource("apps/scorch", key, "success"),
			nil,
		)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
