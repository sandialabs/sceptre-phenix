package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/job"
	"phenix/api/vm"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

const (
	// maxBulkVMs is the most VMs one bulk action may name.
	maxBulkVMs = 1000
	// bulkVMWorkers is how many VMs a bulk action acts on at once.
	bulkVMWorkers = 4
	// vmActionKind is the kind of the bulk VM action jobs.
	vmActionKind = "vms.action"
)

// vmAction is one of the power actions on a VM, as its single-VM handler and
// the bulk action job both take it.
type vmAction struct {
	// whether the role may take the action on the VM <exp>/<vm>
	allowed func(role rbac.Role, fullName string) bool
	// the RBAC resource and verb of the action's broadcasts
	resource, verb string
	// what is logged when the role may not
	denied string

	// the lock taken while the action runs, if any, and the status the lock
	// error log names
	lock       func(exp, name string) error
	lockStatus string

	// the experiment/vm broadcasts sent before acting, when acting fails, and
	// with the VM after acting; "" sends none
	before, failed, after string

	// does the action
	do func(exp, name string) error

	// the action ends with the VM (else with no body): its screenshot is
	// taken at this size ("" for none), and it is reported stopped when
	// stopped is set
	withVM     bool
	screenshot string
	stopped    bool

	// the experiment must be running, and is read before acting
	needsRunning bool

	// what is logged once done
	done string
	// the title of a bulk action job, before its VM count
	title string
}

// vmActions are the VM power actions, by the name the bulk action takes.
//
//nolint:gochecknoglobals // the action table; tests replace its functions
var vmActions = map[string]*vmAction{
	"start": {
		allowed:    func(role rbac.Role, n string) bool { return role.Allowed("vms/start", "update", n) },
		resource:   "vms/start",
		verb:       "update",
		denied:     "starting vm not allowed",
		lock:       cache.LockVMForStarting,
		lockStatus: "starting",
		before:     "starting",
		failed:     "errorStarting",
		after:      "start",
		do:         func(exp, name string) error { return mm.StartVM(mm.NS(exp), mm.VMName(name)) },
		withVM:     true,
		screenshot: defaultScreenshotSize,
		done:       "vm started",
		title:      "Start",
	},
	"pause": {
		allowed:    func(role rbac.Role, n string) bool { return role.Allowed("vms/stop", "update", n) },
		resource:   "vms/stop",
		verb:       "update",
		denied:     "stopping vm not allowed",
		lock:       cache.LockVMForStopping,
		lockStatus: "stopping",
		before:     "stopping",
		failed:     "errorStopping",
		after:      "stop",
		do:         func(exp, name string) error { return mm.StopVM(mm.NS(exp), mm.VMName(name)) },
		withVM:     true,
		done:       "vm stopped",
		title:      "Pause",
	},
	"restart": {
		allowed:    func(role rbac.Role, n string) bool { return role.Allowed("vms/restart", "update", n) },
		resource:   "vms/restart",
		verb:       "update",
		denied:     "restarting vm not allowed",
		lock:       cache.LockVMForStarting,
		lockStatus: "starting",
		before:     "restarting",
		after:      "update",
		do:         vm.Restart,
		withVM:     true,
		screenshot: defaultScreenshotSize,
		done:       "vm restarted",
		title:      "Restart",
	},
	"shutdown": {
		allowed:    func(role rbac.Role, n string) bool { return role.Allowed("vms/shutdown", "update", n) },
		resource:   "vms/shutdown",
		verb:       "update",
		denied:     "shutting down vm not allowed",
		lock:       cache.LockVMForStopping,
		lockStatus: "stopping",
		after:      "shutdown",
		do:         vm.Shutdown,
		withVM:     true,
		stopped:    true,
		done:       "vm shutdown",
		title:      "Shut down",
	},
	"resetDisk": {
		allowed:    func(role rbac.Role, n string) bool { return role.Allowed("vms/reset", "update", n) },
		resource:   "vms/reset",
		verb:       "update",
		denied:     "resetting vm not allowed",
		lock:       cache.LockVMForStopping,
		lockStatus: "stopping",
		after:      "reset",
		do:         vm.ResetDiskState,
		withVM:     true,
		done:       "vm reset",
		title:      "Reset the disk of",
	},
	"kill": {
		allowed:      func(role rbac.Role, n string) bool { return role.Allowed("vms", "delete", n) },
		resource:     "vms",
		verb:         "delete",
		denied:       "deleting vm not allowed",
		after:        "delete",
		do:           func(exp, name string) error { return mm.KillVM(mm.NS(exp), mm.VMName(name)) },
		needsRunning: true,
		done:         "vm deleted",
		title:        "Kill",
	},
}

// vmActionError is why a VM action failed, with the HTTP status its
// single-VM handler answers.
type vmActionError struct {
	status int
	err    error
}

func (e *vmActionError) Error() string {
	return e.err.Error()
}

func (e *vmActionError) Unwrap() error {
	return e.err
}

// vmRequest is a VM action asked for by user with role, from a bulk action
// job when jobID is not "".
type vmRequest struct {
	role  rbac.Role
	user  string
	exp   string
	name  string
	jobID string
}

// runVMAction takes the action on the VM, with the RBAC check, lock,
// broadcasts and logs of its single-VM handler, and returns the VM as JSON
// when the action ends with one. A role that may not act fails with 403,
// wrapping job.ErrForbidden; a VM another action holds with 409, wrapping
// its *cache.LockedError.
func runVMAction(req vmRequest, a *vmAction) ([]byte, error) {
	fullName := req.exp + "/" + req.name

	if !a.allowed(req.role, fullName) {
		plog.Warn(plog.TypeSecurity, a.denied, "user", req.user, "exp", req.exp, "vm", req.name)

		return nil, &vmActionError{status: http.StatusForbidden, err: job.ErrForbidden}
	}

	if a.needsRunning {
		exp, err := experiment.Get(req.exp)
		if err != nil {
			return nil, &vmActionError{status: http.StatusBadRequest, err: err}
		}

		if !exp.Running() {
			return nil, &vmActionError{status: http.StatusBadRequest, err: errors.New("experiment not running")}
		}
	}

	if a.lock != nil {
		if err := a.lock(req.exp, req.name); err != nil {
			plog.Error(
				plog.TypeSystem, "locking VM", "exp", req.exp, "vm", req.name, "action", a.lockStatus, "err", err,
			)

			return nil, &vmActionError{status: http.StatusConflict, err: err}
		}

		defer cache.UnlockVM(req.exp, req.name)
	}

	policy := bt.NewRequestPolicy(a.resource, a.verb, fullName)

	broadcast := func(action string, body []byte) {
		if action != "" {
			broker.Broadcast(policy, bt.NewResource("experiment/vm", fullName, action), body)
		}
	}

	// fail sends the failure broadcast, if the action has one
	fail := func(status int, err error) error {
		broadcast(a.failed, nil)

		return &vmActionError{status: status, err: err}
	}

	broadcast(a.before, nil)

	if err := a.do(req.exp, req.name); err != nil {
		return nil, fail(http.StatusInternalServerError, err)
	}

	var body []byte

	if a.withVM {
		exp, err := experiment.Get(req.exp)
		if err != nil {
			return nil, fail(http.StatusBadRequest, err)
		}

		v, err := vm.GetFor(exp, req.name)
		if err != nil {
			return nil, fail(http.StatusInternalServerError, err)
		}

		if a.screenshot != "" {
			screenshot, err := util.GetScreenshot(req.exp, req.name, a.screenshot)
			if err != nil {
				plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
			} else {
				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
			}
		}

		if a.stopped {
			v.Running = false
		}

		body, err = marshaler.Marshal(util.VMToProtobuf(req.exp, *v, exp.Spec.Topology()))
		if err != nil {
			return nil, &vmActionError{status: http.StatusInternalServerError, err: err}
		}
	}

	broadcast(a.after, body)

	logArgs := []any{"user", req.user, "exp", req.exp, "vm", req.name}
	if req.jobID != "" {
		logArgs = append(logArgs, "job", req.jobID)
	}

	plog.Info(plog.TypeAction, a.done, logArgs...)

	return body, nil
}

// serveVMAction takes the named action on the request's VM and answers as
// the single-VM handlers always have: the VM, or 204 when the action ends
// without one.
func serveVMAction(w http.ResponseWriter, r *http.Request, action string) {
	var (
		ctx  = r.Context()
		vars = mux.Vars(r)
	)

	body, err := runVMAction(vmRequest{
		role:  middleware.RoleFromContext(ctx),
		user:  middleware.UserFromContext(ctx),
		exp:   vars["exp"],
		name:  vars["name"],
		jobID: "",
	}, vmActions[action])

	var actionErr *vmActionError

	switch {
	case errors.As(err, &actionErr) && actionErr.status == http.StatusForbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.As(err, &actionErr):
		http.Error(w, actionErr.Error(), actionErr.status)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case body == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
	}
}

// vmActionsRequest is the body of a bulk VM action.
type vmActionsRequest struct {
	Action string   `json:"action"`
	VMs    []string `json:"vms"`
}

// RunVMActions - POST /experiments/{exp}/vms/actions.
//
// Takes one power action (start, pause, restart, shutdown, kill or
// resetDisk) on several VMs in a background job, a few at a time, each VM as
// its single-VM route would. A VM the role may not act on is skipped; when
// the role may act on none of them, the request is refused with 403 and no
// job starts.
func RunVMActions(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		user = middleware.UserFromContext(ctx)
		exp  = mux.Vars(r)["exp"]
		req  vmActionsRequest
	)

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)

		return
	}

	action, ok := vmActions[req.Action]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown action %q", req.Action), http.StatusBadRequest)

		return
	}

	names := uniqueNames(req.VMs)

	switch {
	case len(names) == 0:
		http.Error(w, "no VMs given", http.StatusBadRequest)

		return
	case len(names) > maxBulkVMs:
		http.Error(w, fmt.Sprintf("%d VMs given, at most %d allowed", len(names), maxBulkVMs), http.StatusBadRequest)

		return
	}

	var permitted bool

	for _, name := range names {
		if action.allowed(role, exp+"/"+name) {
			permitted = true

			break
		}
	}

	if !permitted {
		plog.Warn(plog.TypeSecurity, action.denied, "user", user, "exp", exp, "vms", len(names))
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	startJob(w, r, job.Spec{
		Kind:       vmActionKind,
		Owner:      "",
		Experiment: exp,
		Title:      action.title + " " + countOf(len(names), "VM"),
		Params:     map[string]string{"action": req.Action},
		Items:      names,
		BytesTotal: 0,
		Cancelable: true,
		Run: func(ctx context.Context, run *job.Run) error {
			runVMActions(ctx, run, vmRequest{role: role, user: user, exp: exp, name: "", jobID: run.ID()}, action, names)

			return nil
		},
	})
}

// runVMActions takes the action on each VM, bulkVMWorkers at a time, until
// ctx is done: then it starts no more, and the VMs in progress finish.
func runVMActions(ctx context.Context, run *job.Run, req vmRequest, action *vmAction, names []string) {
	var (
		queue = make(chan string)
		wg    sync.WaitGroup
	)

	for range min(bulkVMWorkers, len(names)) {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for name := range queue {
				one := req
				one.name = name

				run.ItemStarted(name)

				body, err := runVMAction(one, action)

				switch {
				case errors.Is(err, job.ErrForbidden):
					run.ItemSkipped(name, job.ErrForbidden.Error())
				case err != nil:
					run.ItemFailed(name, err)
				case body == nil:
					run.ItemSucceeded(name, nil)
				default:
					run.ItemSucceeded(name, json.RawMessage(body))
				}
			}
		}()
	}

dispatch:
	for _, name := range names {
		// select picks at random when a worker is free too
		if ctx.Err() != nil {
			break
		}

		select {
		case <-ctx.Done():
			break dispatch
		case queue <- name:
		}
	}

	close(queue)
	wg.Wait()
}

// uniqueNames returns the names that are not empty, once each, in order.
func uniqueNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))

	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}

		seen[name] = true
		out = append(out, name)
	}

	return out
}
