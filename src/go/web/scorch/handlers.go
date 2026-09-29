package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	appNameScorch       = "scorch"
	TerminalBufferSize  = 32 * 1024
	TerminalInitTimeout = 5 * time.Second
)

func allowScorch(w http.ResponseWriter, r *http.Request, resource, action string) bool {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if role.Spec == nil || !role.Allowed(resource, action, mux.Vars(r)["name"]) {
		http.Error(w, "Scorch access denied", http.StatusForbidden)
		return false
	}
	return true
}

type termClient struct {
	closed *sync.Once
	id     string
	ws     *websocket.Conn
	done   chan struct{}
}

func newTermClient(ws *websocket.Conn) termClient {
	return termClient{
		id:     uuid.Must(uuid.NewV4()).String(),
		ws:     ws,
		done:   make(chan struct{}),
		closed: new(sync.Once),
	}
}

var (
	rwTermOwners = make(map[int]string)                //nolint:gochecknoglobals // terminal writer identities
	rwTerm       = make(map[int]string)                //nolint:gochecknoglobals // global state
	roTerms      = make(map[int]map[string]termClient) //nolint:gochecknoglobals // global state
	history      = make(map[int]bytes.Buffer)          //nolint:gochecknoglobals // global state

	termClientOwners = make(map[string]string)        //nolint:gochecknoglobals // terminal initialization tokens
	termClientPIDs   = make(map[string]int)           //nolint:gochecknoglobals // terminal initialization tokens
	termClientIDs    = make(map[string]chan struct{}) //nolint:gochecknoglobals // global state

	mu sync.Mutex //nolint:gochecknoglobals // global lock
)

// GetTerminals - GET /experiments/{name}/scorch/terminals.
func GetTerminals(w http.ResponseWriter, r *http.Request) {
	if !allowScorch(w, r, "experiments", "get") {
		return
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTerminal")

	var (
		vars = mux.Vars(r)
		exp  = vars["name"]
	)

	terms, _ := GetExperimentTerminals(exp, -1)

	body, _ := json.Marshal(util.WithRoot("terminals", terms))
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ConnectTerminal - GET /experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}.
func ConnectTerminal(w http.ResponseWriter, r *http.Request) {
	if !allowScorch(w, r, "experiments", "get") {
		return
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ConnectTerminal")

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

	t, err := initTerminal(r.Context(), exp, run, loop, stage, cmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	body, _ := json.Marshal(t)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// StreamTerminal - GET /experiments/{name}/scorch/terminals/{pid}/ws/{id}.
func StreamTerminal(w http.ResponseWriter, r *http.Request) {
	if !allowScorch(w, r, "experiments", "get") {
		return
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamTerminal")

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
	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	ok = ok && termClientOwners[id] == user && termClientPIDs[id] == pid
	if ok {
		delete(termClientIDs, id)
		close(done)
	}
	t.clientID = id
	t.RO = rwTerm[pid] != id
	mu.Unlock()

	if !ok {
		http.Error(w, "terminal client ID invalid", http.StatusNotFound)

		return
	}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !t.RO && !role.Allowed("experiments/trigger", "create", exp) {
		http.Error(w, "terminal write not allowed", http.StatusForbidden)
		return
	}

	plog.Debug(plog.TypeSystem, "starting web terminal streamer", "pid", pid)

	websocket.Handler(terminalWsHandler(t)).ServeHTTP(w, r)
}

// ExitTerminal - POST /experiments/{name}/scorch/terminals/{pid}/exit/{id}.
func ExitTerminal(w http.ResponseWriter, r *http.Request) {
	if !allowScorch(w, r, "experiments/trigger", "create") {
		return
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ExitTerminal")

	exp := mux.Vars(r)["name"]
	pid, _ := strconv.Atoi(mux.Vars(r)["pid"])
	id := mux.Vars(r)["id"]

	mu.Lock()
	owner := rwTerm[pid]
	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	ownerUser := rwTermOwners[pid]
	mu.Unlock()
	if owner != id || ownerUser != user {
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
		tc := newTermClient(ws)
		mu.Lock()
		if roTerms[t.Pid] == nil {
			roTerms[t.Pid] = make(map[string]termClient)
		}
		roTerms[t.Pid][tc.id] = tc
		if h, ok := history[t.Pid]; ok {
			_ = ws.SetWriteDeadline(time.Now().Add(TerminalInitTimeout))
			_, _ = ws.Write(h.Bytes())
		}
		mu.Unlock()
		received := make(chan struct{})
		go func() {
			defer close(received)
			for {
				buf := make([]byte, TerminalBufferSize)
				n, err := ws.Read(buf)
				if err != nil {
					return
				}
				if !t.RO {
					if _, err := t.Pty.Write(buf[:n]); err != nil {
						return
					}
				}
			}
		}()
		select {
		case <-t.Done:
		case <-tc.done:
		case <-received:
		}
		_ = ws.Close()
		<-received
		mu.Lock()
		delete(roTerms[t.Pid], tc.id)
		if !t.RO && rwTerm[t.Pid] == t.clientID {
			delete(rwTerm, t.Pid)
			delete(rwTermOwners, t.Pid)
		}
		mu.Unlock()
	}
}

// A single reader belongs to the PTY, independent of visible browser windows.
// Hiding and reopening a terminal therefore never creates competing readers.
func publishTerminalOutput(t WebTerm, output []byte) {
	mu.Lock()
	defer mu.Unlock()
	h := history[t.Pid]
	_, _ = h.Write(output)
	history[t.Pid] = h
	for id, client := range roTerms[t.Pid] {
		_ = client.ws.SetWriteDeadline(time.Now().Add(TerminalInitTimeout))
		if _, err := client.ws.Write(output); err != nil {
			client.closed.Do(func() { close(client.done) })
			delete(roTerms[t.Pid], id)
		}
	}
}

func initTerminal(ctx context.Context, exp string, run, loop int, stage, cmp string) (WebTerm, error) {
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

	role, _ := ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	if _, ok := rwTerm[t.Pid]; ok || !role.Allowed("experiments/trigger", "create", exp) {
		t.RO = true
	} else {
		rwTerm[t.Pid] = id
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		rwTermOwners[t.Pid] = user
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
	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	termClientOwners[id] = user
	termClientPIDs[id] = t.Pid

	go func() {
		select {
		case <-time.After(TerminalInitTimeout):
			mu.Lock()
			if _, pending := termClientIDs[id]; pending && rwTerm[t.Pid] == id {
				delete(rwTerm, t.Pid)
				delete(rwTermOwners, t.Pid)
			}
			delete(termClientIDs, id)
			delete(termClientOwners, id)
			delete(termClientPIDs, id)
			mu.Unlock()
		case <-done:
			mu.Lock()
			delete(termClientIDs, id)
			delete(termClientOwners, id)
			delete(termClientPIDs, id)
			mu.Unlock()
		}
	}()

	return t, nil
}

// GetComponentOutput - GET /experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}.
func GetComponentOutput(w http.ResponseWriter, r *http.Request) error {
	if !allowScorch(w, r, "experiments", "get") {
		return nil
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetScorchComponentOutput")

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
			t, err := initTerminal(r.Context(), exp, run, loop, stage, cmp)
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
	if !allowScorch(w, r, "experiments", "get") {
		return
	}
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "StreamScorchComponentOutput")

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

	plog.Debug(plog.TypeSystem, "starting scorch component streamer", "key", key)

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

	if err := scorchexe.Reconcile(name); err != nil {
		return weberror.NewWebError(err, "unable to reconcile execution owners")
	}
	if err := exp.Reload(); err != nil {
		return weberror.NewWebError(err, "unable to reload execution state")
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

	status, err := scorchmd.Status(exp)
	if err != nil {
		return weberror.NewWebError(err, "unable to decode Scorch execution status")
	}
	runs := status.ActiveRuns()
	runID := -1
	if len(runs) == 1 {
		runID = runs[0]
	}
	body, _ := json.Marshal(
		map[string]any{
			"pipelines":   pipelines,
			"running":     runID,
			"runningRuns": runs,
			"executions":  status.Executions,
			"stopping":    status.Stopping,
		},
	)

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

	// Reserve before acknowledging; request cancellation cannot release this claim.
	ctx = app.SetContextTriggerUI(context.Background())
	task, err := scorchexe.Prepare(ctx, exp, run)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, scorchexe.ErrAlreadyRunning) || errors.Is(err, scorchexe.ErrStopping) ||
			errors.Is(err, scorchexe.ErrResourceConflict) {
			status = http.StatusConflict
		}
		return weberror.NewWebError(err, "unable to start Scorch run %d", run).SetStatus(status)
	}

	go func() {
		plog.Debug(plog.TypeSystem, "executing Scorch run for experiment", "exp", name, "run", run)

		key := fmt.Sprintf("%s/%d", name, run)

		pubsub.Publish("trigger-app", app.TriggerPublication{ //nolint:exhaustruct // partial initialization
			Experiment: name, App: appNameScorch, Resource: key, State: "start",
		})

		err := task.Run()
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

	if err := scorchexe.RequestCancel(name, run, r.URL.Query().Get("executionID")); err != nil {
		return weberror.NewWebError(err, "unable to cancel Scorch run").SetStatus(http.StatusConflict)
	}

	w.WriteHeader(http.StatusNoContent)

	return nil
}

func RecoverPipeline(w http.ResponseWriter, r *http.Request) error {
	if !allowScorch(w, r, "experiments/trigger", "create") {
		return nil
	}
	if !allowScorch(w, r, "experiments", "get") {
		return nil
	}
	run, err := strconv.Atoi(mux.Vars(r)["run"])
	if err != nil {
		return weberror.NewWebError(err, "invalid run ID")
	}
	var request struct {
		ExecutionID     string `json:"executionID"`
		CleanupComplete bool   `json:"cleanupComplete"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return weberror.NewWebError(err, "invalid recovery request")
	}
	if err := scorchexe.Recover(mux.Vars(r)["name"], run, request.ExecutionID, request.CleanupComplete); err != nil {
		return weberror.NewWebError(err, "unable to recover run").SetStatus(http.StatusConflict)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
