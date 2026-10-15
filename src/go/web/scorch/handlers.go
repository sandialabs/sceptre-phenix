package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

func init() { //nolint:gochecknoinits // hook registration
	experiment.RegisterHook("stop", func(stage, name string) {
		for _, cancel := range scorchexe.GetExperimentCancelers(name) {
			cancel()
		}
	})
}

// RegisterRoutes serves the SCORCH API from api, the router of the /api/v1
// paths.
func RegisterRoutes(api *mux.Router) {
	const base = "/experiments/{name}/scorch/"

	api.Handle(base+"components/{run}/{loop}/{stage}/{cmp}", weberror.ErrorHandler(GetComponentOutput)).
		Methods("GET", "OPTIONS")
	api.HandleFunc(base+"components/{run}/{loop}/{stage}/{cmp}/ws", StreamComponentOutput).
		Methods("GET", "OPTIONS")
	api.Handle(base+"pipelines", weberror.ErrorHandler(GetPipelines)).Methods("GET", "OPTIONS")
	api.Handle(base+"pipelines/{run}/{loop}", weberror.ErrorHandler(GetPipeline)).Methods("GET", "OPTIONS")
	api.Handle(base+"pipelines/{run}/cleanup", weberror.ErrorHandler(CleanupPipeline)).Methods("POST", "OPTIONS")
	api.Handle(base+"pipelines/{run}/clear", weberror.ErrorHandler(ClearPipeline)).Methods("POST", "OPTIONS")
	api.Handle(base+"pipelines/{run}", weberror.ErrorHandler(StartPipeline)).Methods("POST", "OPTIONS")
	api.Handle(base+"pipelines/{run}", weberror.ErrorHandler(CancelPipeline)).Methods("DELETE", "OPTIONS")
	api.HandleFunc(base+"terminals", GetTerminals).Methods("GET", "OPTIONS")
	api.HandleFunc(base+"terminals/{pid}", ConnectTerminal).Methods("GET", "OPTIONS")
	api.HandleFunc(base+"terminals/{pid}/exit/{id}", ExitTerminal).Methods("POST", "OPTIONS")
	api.HandleFunc(base+"terminals/{pid}/ws/{id}", StreamTerminal).Methods("GET", "OPTIONS")
	api.HandleFunc(base+"terminals/{run}/{loop}/{stage}/{cmp}", ConnectTerminal).Methods("GET", "OPTIONS")
}

const (
	appNameScorch       = "scorch"
	TerminalBufferSize  = 32 * 1024
	TerminalInitTimeout = 5 * time.Second
)

// exitedMarker ends a read-only terminal's stream.
const exitedMarker = "***** BREAK PROCESS EXITED *****"

var (
	rwTerm = make(map[int]string) //nolint:gochecknoglobals // global state
	// read-only viewers of each terminal; output is queued for them under mu.
	roTerms = make(map[int]map[string]*viewer) //nolint:gochecknoglobals // global state
	history = make(map[int]bytes.Buffer)       //nolint:gochecknoglobals // global state

	termClientIDs = make(map[string]chan struct{}) //nolint:gochecknoglobals // global state

	mu sync.Mutex //nolint:gochecknoglobals // global lock
)

// canView reports whether the request's role may view the experiment's SCORCH
// output and terminals, answering 403 when it may not.
func canView(w http.ResponseWriter, r *http.Request, exp string) bool {
	ctx := r.Context()

	if middleware.RoleFromContext(ctx).Allowed("experiments", "get", exp) {
		return true
	}

	plog.Warn(
		plog.TypeSecurity,
		"viewing experiment scorch output not allowed",
		"user",
		ctx.Value(middleware.ContextKeyUser),
		"exp",
		exp,
	)
	http.Error(w, "forbidden", http.StatusForbidden)

	return false
}

// mayWrite reports whether the request's role may type into the experiment's
// SCORCH terminals and stop them, which, like starting a pipeline, needs
// experiments/trigger create.
func mayWrite(r *http.Request, exp string) bool {
	return middleware.RoleFromContext(r.Context()).Allowed("experiments/trigger", "create", exp)
}

// GetTerminals - GET /experiments/{name}/scorch/terminals.
func GetTerminals(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTerminal")

	var (
		vars = mux.Vars(r)
		exp  = vars["name"]
	)

	if !canView(w, r, exp) {
		return
	}

	terms, _ := GetExperimentTerminals(exp, -1)

	body, _ := json.Marshal(util.WithRoot("terminals", terms))
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ConnectTerminal - GET /experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}.
func ConnectTerminal(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ConnectTerminal")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	if !canView(w, r, exp) {
		return
	}

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

	t, err := initTerminal(exp, run, loop, stage, cmp, mayWrite(r, exp))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	body, _ := json.Marshal(t)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// StreamTerminal - GET /experiments/{name}/scorch/terminals/{pid}/ws/{id}.
func StreamTerminal(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamTerminal")

	exp := mux.Vars(r)["name"]
	if !canView(w, r, exp) {
		return
	}

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

	mu.Lock()
	t.RO = rwTerm[pid] != id
	mu.Unlock()

	if !t.RO && !mayWrite(r, exp) {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	close(done)

	plog.Debug(plog.TypeSystem, "starting web terminal streamer", "pid", pid)

	websocket.Handler(terminalWsHandler(t)).ServeHTTP(w, r)
}

// ExitTerminal - POST /experiments/{name}/scorch/terminals/{pid}/exit/{id}.
func ExitTerminal(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ExitTerminal")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])
	id := mux.Vars(r)["id"]

	if !canView(w, r, exp) {
		return
	}

	if !mayWrite(r, exp) {
		plog.Warn(
			plog.TypeSecurity,
			"exiting experiment scorch terminal not allowed",
			"user",
			r.Context().Value(middleware.ContextKeyUser),
			"exp",
			exp,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	mu.Lock()
	owner := rwTerm[pid]
	mu.Unlock()

	if owner != id {
		plog.Error(
			plog.TypeSystem,
			"terminal client doesn't own R/W rights to PTY",
			"id",
			id,
			"pid",
			pid,
		)
		http.Error(w, "terminal client not allowed to exit terminal", http.StatusForbidden)

		return
	}

	t, err := GetTerminalByPID(pid)
	if err != nil {
		plog.Error(plog.TypeSystem, "web terminal for PID not found", "pid", pid)
		http.Error(w, "web terminal not found", http.StatusNotFound)

		return
	}

	if t.Exp != exp {
		http.Error(w, "no web terminal found", http.StatusNotFound)

		return
	}

	if err := KillTerminal(t); err != nil {
		plog.Error(plog.TypeSystem, "killing terminal for PID", "pid", pid, "err", err)
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
			plog.Error(plog.TypeSystem, "unable to find process", "pid", t.Pid)

			return
		}

		if t.RO {
			v := attachReadOnly(t, ws)

			go v.watch(ws)
			go waitReadOnly(t, v)

			v.serve()

			return
		}

		go readTerm(t, ws)

		writeTerm(t, ws)

		mu.Lock()
		delete(rwTerm, t.Pid)
		mu.Unlock()
	}
}

// readTerm copies the terminal's output to its read/write client, which it
// waits on, and queues it for the read-only viewers, which it does not.
func readTerm(t WebTerm, rw io.Writer) {
	for {
		buf := make([]byte, TerminalBufferSize)

		nr, err := t.Pty.Read(buf)
		if err != nil {
			break
		}

		nw, err := rw.Write(buf[0:nr])
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

		for id, v := range roTerms[t.Pid] {
			if !v.offer(buf[0:nr]) {
				v.finish()
				delete(roTerms[t.Pid], id)

				go plog.Warn(plog.TypeSystem, "dropping SCORCH terminal viewer that fell behind", "pid", t.Pid)
			}
		}
		mu.Unlock()
	}
}

// writeTerm copies the read/write client's input to the terminal.
func writeTerm(t WebTerm, rw io.Reader) {
	for {
		buf := make([]byte, TerminalBufferSize)

		nr, err := rw.Read(buf)
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

// attachReadOnly adds a read-only viewer of the terminal, with the output so
// far queued for it first.
func attachReadOnly(t WebTerm, conn frameConn) *viewer {
	v := newViewer(conn)

	mu.Lock()
	defer mu.Unlock()

	// the history is one chunk, so it fits the viewer's empty queue
	if h, ok := history[t.Pid]; ok {
		v.offer(h.Bytes())
	}

	terms, ok := roTerms[t.Pid]
	if !ok {
		terms = make(map[string]*viewer)
	}

	terms[v.id] = v
	roTerms[t.Pid] = terms

	return v
}

// waitReadOnly forgets a read-only viewer once it leaves, or tells every
// read-only viewer the terminal exited, finishing their output.
func waitReadOnly(t WebTerm, v *viewer) {
	select {
	case <-v.gone:
		mu.Lock()
		delete(roTerms[t.Pid], v.id)
		mu.Unlock()
	case <-t.Done:
		mu.Lock()
		for _, ro := range roTerms[t.Pid] {
			// notify read-only clients that R/W terminal has exited
			ro.offer([]byte(exitedMarker))
			ro.finish()
		}

		delete(roTerms, t.Pid)
		mu.Unlock()
	}
}

// initTerminal hands a client of the terminal its stream location. The first
// client that may write (rw) gets the terminal's read/write side and its exit
// location; every other client gets a read-only stream.
func initTerminal(exp string, run, loop int, stage, cmp string, rw bool) (WebTerm, error) {
	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)

	t, err := GetTerminalByExperiment(key)
	if err != nil {
		return WebTerm{}, errors.New("no web terminal found")
	}

	if t.Exp != exp {
		return WebTerm{}, errors.New("no web terminal found")
	}

	id := uuid.Must(uuid.NewV4()).String()
	t.Loc = fmt.Sprintf(
		"%sapi/v1/experiments/%s/scorch/terminals/%d/ws/%s",
		basePath,
		exp,
		t.Pid,
		id,
	)

	mu.Lock()
	defer mu.Unlock()

	if _, ok := rwTerm[t.Pid]; ok || !rw {
		t.RO = true
	} else {
		rwTerm[t.Pid] = id
		t.Exit = fmt.Sprintf(
			"%sapi/v1/experiments/%s/scorch/terminals/%d/exit/%s",
			basePath,
			exp,
			t.Pid,
			id,
		)
	}

	done := make(chan struct{})
	termClientIDs[id] = done

	go func() {
		select {
		case <-time.After(TerminalInitTimeout):
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

// GetComponentOutput - GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}.
func GetComponentOutput(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetScorchComponentOutput")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	if !canView(w, r, exp) {
		return nil
	}

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		return weberror.NewWebError(err, "invalid loop number provided")
	}

	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)
	req := outputRequest{key: key, resp: make(chan outputResponse, 1)}

	outputRequests <- req

	resp := <-req.resp

	if resp.running {
		if resp.terminal {
			t, err := initTerminal(exp, run, loop, stage, cmp, mayWrite(r, exp))
			if err != nil {
				return weberror.NewWebError(err, "unable to initialize terminal")
			}

			body, _ := json.Marshal(util.WithRoot("terminal", t))

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

			return nil
		}

		body, _ := json.Marshal(
			util.WithRoot(
				"stream",
				fmt.Sprintf(
					"%sapi/v1/experiments/%s/scorch/components/%d/%d/%s/%s/ws",
					basePath,
					exp,
					run,
					loop,
					stage,
					cmp,
				),
			),
		)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

		return nil
	}

	body, err := json.Marshal(util.WithRoot("output", string(resp.output)))
	if err != nil {
		err := weberror.NewWebError(err, "unable to process component %s output", cmp)

		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// StreamComponentOutput - GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}/ws.
func StreamComponentOutput(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamScorchComponentOutput")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	if !canView(w, r, exp) {
		return
	}

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

	plog.Debug(plog.TypeSystem, "starting scorch component streamer", "key", key)

	websocket.Handler(scorchComponentWsHandler(key)).ServeHTTP(w, r)
}

func scorchComponentWsHandler(key string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		v := newViewer(ws)

		// add client to list for component
		wsRequests <- wsRequest{key: key, v: v}

		go v.watch(ws)

		v.serve()
	}
}

// GetPipelines - GET /experiments/{name}/scorch/pipelines.
func GetPipelines(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetPipelines")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]
	)

	if !role.Allowed("experiments", "get", name) {
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment scorch pipelines not allowed",
			"user",

			ctx.Value(middleware.ContextKeyUser),
			"exp",
			name,
		)
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"getting experiment %s not allowed for %s",
			name,
			user,
		)

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
			return weberror.NewWebError(
				err,
				"unable to get pipeline %d for experiment %s",
				run,
				name,
			)
		}

		pipelines = append(pipelines, pipeline)
	}

	var running bool

	// first make sure Scorch app is running
	for app, status := range exp.Status.AppRunning() {
		if app == appNameScorch && status {
			running = true

			break
		}
	}

	runID := -1

	// if Scorch app is running, find out which run is currently being executed
	if running {
		// TODO: this should never be nil if Scorch is running...
		if exp.Status.AppStatus() != nil {
			if status, ok := exp.Status.AppStatus()[appNameScorch].(map[string]any); ok {
				if id, ok := status["runID"].(float64); ok {
					runID = int(id)
				}
			}
		}
	}

	// the experiment's own state, so the pipelines page can show it
	status := cache.ExperimentStatus(name, exp.Running())

	body, _ := json.Marshal(map[string]any{
		"pipelines": pipelines,
		"running":   runID,
		// whether the SCORCH app itself is running, so the SCORCH page needs no
		// separate experiment apps request
		"app_running": running,
		"experiment":  map[string]any{"status": status},
	})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// GetPipeline - GET /experiments/{name}/scorch/pipelines/{run}/{loop}.
func GetPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetPipeline")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		exp     = vars["name"]
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
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment scorch pipeline not allowed",
			"user",

			ctx.Value(middleware.ContextKeyUser),
			"exp",
			exp,
		)
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"getting experiment %s not allowed for %s",
			exp,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	pipeline, err := RequestPipeline(exp, run, loop)
	if err != nil {
		return weberror.NewWebError(err, "unable to get pipeline %d for experiment %s", run, exp)
	}

	body, _ := json.Marshal(pipeline)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// ClearPipeline - POST /experiments/{name}/scorch/pipelines/{run}/clear.
//
// Forgets the component statuses of a run that is not executing, for every
// loop, so its pipeline shows as never run.
func ClearPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ClearPipeline")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	if !role.Allowed("experiments/trigger", "create", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"clearing Scorch runs for experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	md, err := scorchRuns(exp)
	if err != nil {
		err := weberror.NewWebError(err, "unable to decode scorch metadata for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	if run < 0 || run >= len(md.Runs) {
		err := weberror.NewWebError(nil, "Scorch run %d not found for experiment %s", run, name)

		return err.SetStatus(http.StatusNotFound)
	}

	if scorchexe.HasCanceler(name, run) {
		err := weberror.NewWebError(nil, "Scorch run %d for experiment %s is executing", run, name)

		return err.SetStatus(http.StatusConflict)
	}

	// rebuilding broadcasts each loop's cleared pipeline
	DeletePipeline(name, run, -1, true)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// scorchRuns decodes the experiment's scorch app metadata, which has no runs
// when the experiment has no scorch app.
func scorchRuns(exp *types.Experiment) (scorchmd.ScorchMetadata, error) {
	md, err := scorchmd.DecodeMetadata(exp)
	if errors.Is(err, scorchmd.ErrScorchNotConfigured) {
		return scorchmd.ScorchMetadata{}, nil
	}

	return md, err //nolint:wrapcheck // the callers say what failed
}

// TODO: change this to `scorch/runs`

// StartPipeline - POST /experiments/{name}/scorch/pipelines/{run}.
func StartPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StartPipeline")

	return startPipeline(w, r, false)
}

// CleanupPipeline - POST /experiments/{name}/scorch/pipelines/{run}/cleanup.
//
// Runs only the run's cleanup stage, for example to tear down what a canceled
// run left behind.
func CleanupPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CleanupPipeline")

	return startPipeline(w, r, true)
}

//nolint:funlen // handler
func startPipeline(w http.ResponseWriter, r *http.Request, cleanupOnly bool) error {
	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	if !role.Allowed("experiments/trigger", "create", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"starting Scorch runs for experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if !role.Allowed("experiments", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"getting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	if cleanupOnly {
		md, err := scorchRuns(exp)
		if err != nil {
			err := weberror.NewWebError(err, "unable to decode scorch metadata for experiment %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}

		if run < 0 || run >= len(md.Runs) || len(md.Runs[run].Cleanup) == 0 {
			err := weberror.NewWebError(
				nil,
				"Scorch run %d for experiment %s has no cleanup components",
				run,
				name,
			)

			return err.SetStatus(http.StatusBadRequest)
		}
	}

	if scorchexe.HasCanceler(name, run) {
		return weberror.NewWebError(nil, "Scorch run already executing for experiment %s", name)
	}

	// We don't want to use the HTTP request's context here.
	ctx = scorchexe.AddCanceler(context.Background(), name, run)
	ctx = app.SetContextTriggerUI(ctx)

	if cleanupOnly {
		ctx = scorchexe.SetCleanupOnly(ctx)
	}

	go func() {
		plog.Debug(plog.TypeSystem, "executing Scorch run for experiment", "exp", name, "run", run)

		key := fmt.Sprintf("%s/%d", name, run)

		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: appNameScorch, Resource: key, State: "start",
		})

		err := scorchexe.Execute(ctx, exp, run)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				plog.Error(
					plog.TypeSystem,
					"executing Scorch run for experiment",
					"exp",
					name,
					"run",
					run,
					"err",
					err,
				)

				pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
					Experiment: name,
					App:        appNameScorch,
					Resource:   key,
					State:      "error",
					Error: fmt.Errorf(
						"failed to execute Scorch run %d for experiment %s: %w",
						run,
						name,
						err,
					),
				})
			}
		} else {
			plog.Debug(
				plog.TypeSystem,
				"Scorch run for experiment executed successfully",
				"exp",
				name,
				"run",
				run,
			)

			pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
				Experiment: name, App: appNameScorch, Resource: key, State: "success",
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

// CancelPipeline - DELETE /experiments/{name}/scorch/pipelines/{run}.
func CancelPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CancelPipeline")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]
	)

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID '%s' provided", vars["run"])
	}

	if !role.Allowed("experiments/trigger", "delete", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		err := weberror.NewWebError(
			nil,
			"canceling Scorch runs for experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if cancel := scorchexe.GetCanceler(name, run); cancel != nil {
		plog.Debug(plog.TypeSystem, "canceling Scorch run for experiment", "exp", name, "run", run)

		cancel()

		key := fmt.Sprintf("%s/%d", name, run)

		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, Verb: "delete", App: appNameScorch, Resource: key, State: "success",
		})
	}

	w.WriteHeader(http.StatusNoContent)

	return nil
}
