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
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	"github.com/gofrs/uuid"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
)

func init() {
	experiment.RegisterHook("stop", func(stage, name string) {
		for _, cancel := range scorchexe.GetExperimentCancelers(name) {
			cancel()
		}
	})
}

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

	mu sync.Mutex
)

// GET /experiments/{name}/scorch/terminals
func GetTerminals(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetTerminal")

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
	plog.Debug("HTTP handler called", "handler", "ConnectTerminal")

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
	plog.Debug("HTTP handler called", "handler", "StreamTerminal")

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

	plog.Debug("starting web terminal streamer", "pid", pid)

	websocket.Handler(terminalWsHandler(t)).ServeHTTP(w, r)
}

// POST /experiments/{name}/scorch/terminals/{pid}/exit/{id}
func ExitTerminal(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "ExitTerminal")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])
	id := mux.Vars(r)["id"]

	if rwTerm[pid] != id {
		plog.Error("terminal client doesn't own R/W rights to PTY", "id", id, "pid", pid)
		http.Error(w, "terminal client not allowed to exit terminal", http.StatusForbidden)
		return
	}

	t, err := GetTerminalByPID(pid)
	if err != nil {
		plog.Error("web terminal for PID not found", "pid", pid)
		http.Error(w, "web terminal not found", http.StatusNotFound)
		return
	}

	if t.Exp != exp {
		http.Error(w, "no web terminal found", http.StatusNotFound)
		return
	}

	if err := KillTerminal(t); err != nil {
		plog.Error("killing terminal for PID", "pid", pid, "err", err)
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
			plog.Error("unable to find process", "pid", t.Pid)
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
	plog.Debug("HTTP handler called", "handler", "GetScorchComponentOutput")

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
	plog.Debug("HTTP handler called", "handler", "StreamScorchComponentOutput")

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

	plog.Debug("starting scorch component streamer", "key", key)

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
	plog.Debug("HTTP handler called", "handler", "GetPipelines")

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
	plog.Debug("HTTP handler called", "handler", "GetPipeline")

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
	plog.Debug("HTTP handler called", "handler", "StartPipeline")

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

	if scorchexe.HasCanceler(name, run) {
		return weberror.NewWebError(nil, "Scorch run already executing for experiment %s", name)
	}

	// We don't want to use the HTTP request's context here.
	ctx = scorchexe.AddCanceler(context.Background(), name, run)
	ctx = app.SetContextTriggerUI(ctx)

	go func() {
		plog.Debug("executing Scorch run for experiment", "exp", name, "run", run)

		key := fmt.Sprintf("%s/%d", name, run)

		pubsub.Publish("trigger-app", app.TriggerPublication{
			Experiment: name, App: "scorch", Resource: key, State: "start",
		})

		if err := scorchexe.Execute(ctx, exp, run); err != nil {
			if !errors.Is(err, context.Canceled) {
				plog.Error("executing Scorch run for experiment", "exp", name, "run", run, "err", err)

				pubsub.Publish("trigger-app", app.TriggerPublication{
					Experiment: name, App: "scorch", Resource: key, State: "error",
					Error: fmt.Errorf("failed to execute Scorch run %d for experiment %s", run, name),
				})
			}
		} else {
			plog.Debug("Scorch run for experiment executed successfully", "exp", name, "run", run)

			pubsub.Publish("trigger-app", app.TriggerPublication{
				Experiment: name, App: "scorch", Resource: key, State: "success",
			})
		}

		// Ensure context is canceled to avoid leakage. It's okay to call the
		// `cancel` function multiple times. It's a no-op after the first time it's
		// called.
		if cancel := scorchexe.GetCanceler(name, run); cancel != nil {
			cancel()
		}
	}()

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// TODO: change this to `scorch/runs`

// DELETE /experiments/{name}/scorch/pipelines/{run}
func CancelPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "CancelPipeline")

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

	if cancel := scorchexe.GetCanceler(name, run); cancel != nil {
		plog.Debug("canceling Scorch run for experiment", "exp", name, "run", run)

		cancel()

		key := fmt.Sprintf("%s/%d", name, run)

		pubsub.Publish("trigger-app", app.TriggerPublication{
			Experiment: name, Verb: "delete", App: "scorch", Resource: key, State: "success",
		})
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}
