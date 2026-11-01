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

	"github.com/gofrs/uuid/v5"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/util/plog"
	"phenix/util/pubsub"
	"phenix/web/audit"
	"phenix/web/lifecycle"
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

const (
	appNameScorch       = "scorch"
	TerminalBufferSize  = 32 * 1024
	TerminalInitTimeout = 5 * time.Second
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
	rwTerm  = make(map[int]string)                //nolint:gochecknoglobals // global state
	roTerms = make(map[int]map[string]termClient) //nolint:gochecknoglobals // global state
	history = make(map[int]bytes.Buffer)          //nolint:gochecknoglobals // global state

	termClientIDs = make(map[string]chan struct{}) //nolint:gochecknoglobals // global state

	mu sync.Mutex //nolint:gochecknoglobals // global lock
)

// canView returns a 403 unless the request's role may view the experiment's
// SCORCH output and terminals.
func canView(r *http.Request, exp string) error {
	ctx := r.Context()

	if middleware.RoleFromContext(ctx).Allowed("experiments", "get", exp) {
		return nil
	}

	plog.Warn(
		plog.TypeSecurity,
		"viewing experiment scorch output not allowed",
		"user",
		ctx.Value(middleware.ContextKeyUser),
		"exp",
		exp,
	)

	return weberror.New(http.StatusForbidden, "forbidden")
}

// mayWrite reports whether the request's role may type into the experiment's
// SCORCH terminals and stop them, which, like starting a pipeline, needs
// experiments/trigger create.
func mayWrite(r *http.Request, exp string) bool {
	return middleware.RoleFromContext(r.Context()).Allowed("experiments/trigger", "create", exp)
}

// GetTerminals - GET /experiments/{name}/scorch/terminals.
func GetTerminals(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTerminal")

	var (
		vars = mux.Vars(r)
		exp  = vars["name"]
	)

	if err := canView(r, exp); err != nil {
		return err
	}

	terms, _ := GetExperimentTerminals(exp, -1)

	body, _ := json.Marshal(util.WithRoot("terminals", terms))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// ConnectTerminal - GET /experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}.
func ConnectTerminal(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ConnectTerminal")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	if err := canView(r, exp); err != nil {
		return err
	}

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid run ID provided")
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid loop number provided")
	}

	t, err := initTerminal(exp, run, loop, stage, cmp, mayWrite(r, exp))
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	body, _ := json.Marshal(t)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)

	return nil
}

// StreamTerminal - GET /experiments/{name}/scorch/terminals/{pid}/ws/{id}.
func StreamTerminal(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamTerminal")

	exp := mux.Vars(r)["name"]
	if err := canView(r, exp); err != nil {
		return err
	}

	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])

	t, err := GetTerminalByPID(pid)
	if err != nil {
		return weberror.New(http.StatusNotFound, "no web terminal found")
	}

	if t.Exp != exp {
		return weberror.New(http.StatusNotFound, "no web terminal found")
	}

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	readOnly, status := claimTerminalClient(pid, mux.Vars(r)["id"], mayWrite(r, exp))

	switch status {
	case http.StatusNotFound:
		return weberror.New(status, "terminal client ID invalid")
	case http.StatusForbidden:
		return weberror.New(status, "forbidden")
	}

	t.RO = readOnly

	plog.Debug(plog.TypeSystem, "starting web terminal streamer", "pid", pid)

	websocket.Handler(terminalWsHandler(t)).ServeHTTP(w, r)

	return nil
}

// ExitTerminal - POST /experiments/{name}/scorch/terminals/{pid}/exit/{id}.
func ExitTerminal(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ExitTerminal")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])
	id := mux.Vars(r)["id"]

	if err := canView(r, exp); err != nil {
		return err
	}

	exited := audit.Fields{Experiment: exp, PID: pid}

	if !mayWrite(r, exp) {
		audit.Denied(r, audit.ScorchTerminalExit, "exiting experiment scorch terminal not allowed", exited)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	mu.Lock()
	owner := rwTerm[pid]
	mu.Unlock()

	if owner != id {
		exited.Reason = "not writer"
		audit.Denied(r, audit.ScorchTerminalExit, "exiting experiment scorch terminal not allowed", exited)
		plog.Error(
			plog.TypeSystem,
			"terminal client doesn't own R/W rights to PTY",
			"id",
			id,
			"pid",
			pid,
		)

		return weberror.New(http.StatusForbidden, "terminal client not allowed to exit terminal")
	}

	t, err := GetTerminalByPID(pid)
	if err != nil {
		plog.Error(plog.TypeSystem, "web terminal for PID not found", "pid", pid)

		return weberror.New(http.StatusNotFound, "web terminal not found")
	}

	if t.Exp != exp {
		return weberror.New(http.StatusNotFound, "no web terminal found")
	}

	if err := KillTerminal(t); err != nil {
		plog.Error(plog.TypeSystem, "killing terminal for PID", "pid", pid, "err", err)
		audit.Failure(r, audit.ScorchTerminalExit, "exiting experiment scorch terminal failed", exited, err)

		return weberror.New(http.StatusNotFound, "error exiting terminal")
	}

	audit.Success(r, audit.ScorchTerminalExit, "exited experiment scorch terminal", exited)

	mu.Lock()
	delete(history, pid)
	mu.Unlock()

	w.WriteHeader(http.StatusNoContent)

	return nil
}

//nolint:funlen // handler
func terminalWsHandler(t WebTerm) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("SCORCH terminal", ws)()

		_, err := os.FindProcess(t.Pid)
		if err != nil {
			plog.Error(plog.TypeSystem, "unable to find process", "pid", t.Pid)

			return
		}

		readTerm := func() {
			for {
				buf := make([]byte, TerminalBufferSize)

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

				for _, client := range ro {
					nw, err := client.ws.Write(buf[0:nr])
					if err != nil {
						close(client.done)

						break
					}

					if nw != nr {
						close(client.done)

						break
					}
				}
			}
		}

		writeTerm := func() {
			for {
				buf := make([]byte, TerminalBufferSize)

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
					_, _ = ro.ws.Write([]byte("***** BREAK PROCESS EXITED *****"))
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
				_, _ = tc.ws.Write(h.Bytes())
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

// claimTerminalClient takes the client ID initTerminal gave for the terminal
// pid and reports whether the client reads only. An ID is claimed once, so a
// second websocket with it gets 404; the writer's ID, sent by a role that may
// not write, gets 403 and stays unclaimed for the writer.
func claimTerminalClient(pid int, id string, writable bool) (bool, int) {
	mu.Lock()
	defer mu.Unlock()

	done, ok := termClientIDs[id]
	if !ok {
		return false, http.StatusNotFound
	}

	writer := rwTerm[pid] == id
	if writer && !writable {
		return false, http.StatusForbidden
	}

	delete(termClientIDs, id)
	close(done)

	return !writer, http.StatusOK
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
			// a read-only client that never attaches leaves the writer's rights
			if rwTerm[t.Pid] == id {
				delete(rwTerm, t.Pid)
			}

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

	if err := canView(r, exp); err != nil {
		return err
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
	req := outputRequest{key: key, resp: make(chan outputResponse)}

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
func StreamComponentOutput(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamScorchComponentOutput")

	var (
		vars  = mux.Vars(r)
		exp   = vars["name"]
		stage = vars["stage"]
		cmp   = vars["cmp"]
	)

	if err := canView(r, exp); err != nil {
		return err
	}

	run, err := strconv.Atoi(vars["run"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid run ID provided")
	}

	loop, err := strconv.Atoi(vars["loop"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid loop number provided")
	}

	key := fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, cmp)

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	plog.Debug(plog.TypeSystem, "starting scorch component streamer", "key", key)

	websocket.Handler(scorchComponentWsHandler(key)).ServeHTTP(w, r)

	return nil
}

func scorchComponentWsHandler(key string) func(*websocket.Conn) {
	return func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("SCORCH output", ws)()

		id := uuid.Must(uuid.NewV4()).String()
		req := wsRequest{key: key, id: id, ws: ws, done: make(chan struct{})}

		// add client to list for component
		wsRequests <- req
		// wait for done channel to be closed
		<-req.done
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

	body, _ := json.Marshal(map[string]any{"pipelines": pipelines, "running": runID})

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

// TODO: change this to `scorch/runs`

// StartPipeline - POST /experiments/{name}/scorch/pipelines/{run}.
//
//nolint:funlen // handler
func StartPipeline(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StartPipeline")

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

	if scorchexe.HasCanceler(name, run) {
		return weberror.NewWebError(nil, "Scorch run already executing for experiment %s", name)
	}

	// We don't want to use the HTTP request's context here.
	ctx = scorchexe.AddCanceler(context.Background(), name, run)
	ctx = app.SetContextTriggerUI(ctx)

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
						"failed to execute Scorch run %d for experiment %s",
						run,
						name,
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

	canceled := runFields(name, run)

	if !role.Allowed("experiments/trigger", "delete", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		audit.Denied(r, audit.ScorchCancel, "canceling scorch run not allowed", canceled)
		err := weberror.NewWebError(
			nil,
			"canceling Scorch runs for experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	running := scorchexe.GetCanceler(name, run)
	canceled.Extra = append(canceled.Extra, "phenix.scorch.running", running != nil)
	audit.Success(r, audit.ScorchCancel, "canceled scorch run", canceled)

	if cancel := running; cancel != nil {
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

// runFields are the audit fields of the experiment's SCORCH run.
func runFields(exp string, run int) audit.Fields {
	return audit.Fields{Experiment: exp, Extra: []any{"phenix.scorch.run", run}}
}
