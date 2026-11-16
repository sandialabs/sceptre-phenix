package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"phenix/api/experiment"
	"phenix/api/scorch"
	"phenix/api/scorch/scorchmd"
	"phenix/scheduler"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// GetExperimentSchedulerInput - GET /experiments/{name}/schedule/input. It
// answers the JSON that a user scheduler gets on stdin, as `phenix util
// scheduler-json` does. It needs experiments get, configs get on the
// experiment's config, and hosts list on each host.
func GetExperimentSchedulerInput(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		name = mux.Vars(r)["name"]
	)

	// the input holds the whole spec, scenario app metadata included
	if !role.Allowed("experiments", "get", name) || !role.Allowed("configs", "get", "Experiment/"+name) ||
		!role.Allowed("hosts", "list") {
		return inputDenied(r, "scheduler", name)
	}

	data, err := experiment.SchedulerInput(ctx, name)
	if requestCanceled(r, err) {
		return nil
	}

	if errors.Is(err, experiment.ErrSchedulerHosts) {
		plog.Error(plog.TypeSystem, "reading the scheduler input of experiment", "exp", name, "err", err)

		switch {
		case errors.Is(err, scheduler.ErrTimeout):
			return weberror.New(http.StatusGatewayTimeout, "could not read the cluster hosts from minimega within "+
				scheduler.UserTimeout.String())
		case errors.Is(err, mm.ErrMeshUnavailable):
			return weberror.New(http.StatusServiceUnavailable, "could not read the cluster hosts: the mesh peers did not answer")
		default:
			return weberror.New(http.StatusBadGateway, "could not read the cluster hosts from minimega")
		}
	}

	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name).SetStatus(storeStatus(err))
	}

	var input struct {
		Hosts []struct {
			Name string `json:"name"`
		} `json:"hosts"`
	}

	if err := json.Unmarshal(data, &input); err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	// GET /hosts leaves out each host the role may not list; this input cannot
	for _, host := range input.Hosts {
		if !role.Allowed("hosts", "list", host.Name) {
			return inputDenied(r, "scheduler", name)
		}
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(data) //nolint:gosec // G705 JSON, not HTML

	return nil
}

// GetExperimentScorchComponentInput - GET
// /experiments/{name}/scorch/components/{cmp}/input. It answers the JSON that
// the program of a SCORCH user component gets on stdin, as `phenix util
// scorch-json` does. The query parameters run, loop, stage, count, replace
// and runtime are the flags of the command. It needs experiments get and
// configs get on the experiment's config, and with runtime, vms list on each
// of its VMs.
func GetExperimentScorchComponentInput(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
		cmp  = vars["cmp"]
	)

	// the input holds the whole stored config, scenario app metadata included
	if !role.Allowed("experiments", "get", name) || !role.Allowed("configs", "get", "Experiment/"+name) {
		return inputDenied(r, "SCORCH component", name)
	}

	opts, werr := scorchInputQuery(r)
	if werr != nil {
		return werr
	}

	if opts.Runtime && !role.Allowed("vms", "list", name+"/*") {
		return inputDenied(r, "SCORCH component", name)
	}

	input, err := scorch.UserComponentInput(ctx, name, cmp, opts)
	if requestCanceled(r, err) {
		return nil
	}

	switch {
	case errors.Is(err, experiment.ErrAppRuntime):
		plog.Error(plog.TypeSystem, "reading the SCORCH component input of experiment", "exp", name, "err", err)

		// a partial list of hosts or VMs would read as VMs missing
		return weberror.New(http.StatusBadGateway, "could not read the experiment's hosts and VMs from minimega")
	case errors.Is(err, store.ErrNotExist):
		return weberror.NewWebError(err, "unable to get experiment %s from store", name).SetStatus(http.StatusNotFound)
	case errors.Is(err, scorch.ErrComponentNotFound), errors.Is(err, scorchmd.ErrScorchNotConfigured):
		return weberror.New(http.StatusNotFound, err.Error())
	case errors.Is(err, scorch.ErrBuiltinComponent), errors.Is(err, scorch.ErrComponentNotRun),
		errors.Is(err, scorch.ErrUnknownReplacement):
		return weberror.New(http.StatusBadRequest, err.Error())
	case err != nil:
		return weberror.NewWebError(err, "unable to get the input of SCORCH component %s of experiment %s", cmp, name).
			SetStatus(http.StatusInternalServerError)
	}

	if opts.Runtime {
		names, err := inputVMNames(input.Stdin)
		if err != nil {
			return weberror.New(http.StatusInternalServerError, err.Error())
		}

		// a pattern such as "exp/?" or a negated name can allow "<exp>/*" and not every VM
		for _, vm := range names {
			if !role.Allowed("vms", "list", name+"/"+vm) {
				return inputDenied(r, "SCORCH component", name)
			}
		}
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(input.Stdin) //nolint:gosec // G705 JSON, not HTML

	return nil
}

// scorchInputQuery returns the input options of the query of r, or a 400.
func scorchInputQuery(r *http.Request) (scorch.InputOptions, *weberror.WebError) {
	var (
		query = r.URL.Query()
		opts  scorch.InputOptions
		err   error
	)

	for _, param := range []string{"run", "loop", "count"} {
		if !query.Has(param) {
			continue
		}

		value, convErr := strconv.Atoi(query.Get(param))
		if convErr != nil {
			return opts, weberror.New(http.StatusBadRequest, param+" must be a whole number")
		}

		switch param {
		case "run":
			opts.Run = &value
		case "loop":
			opts.Loop = &value
		default:
			opts.Count = value
		}
	}

	if query.Has("stage") {
		if opts.Stage, err = scorch.ParseInputStage(query.Get("stage")); err != nil {
			return opts, weberror.New(http.StatusBadRequest, err.Error())
		}
	}

	if opts.Replace, err = scorch.ParseReplacements(query["replace"]); err != nil {
		return opts, weberror.New(http.StatusBadRequest, err.Error())
	}

	if query.Has("runtime") {
		if opts.Runtime, err = strconv.ParseBool(query.Get("runtime")); err != nil {
			return opts, weberror.New(http.StatusBadRequest, "runtime must be true or false")
		}
	}

	return opts, nil
}

// inputVMNames returns the names of the topology nodes and of the VMs in
// stdin, the experiment JSON of a user app or component.
func inputVMNames(stdin []byte) ([]string, error) {
	type named struct {
		Name string `json:"name"`
	}

	var exp struct {
		Spec struct {
			Topology struct {
				Nodes []struct {
					General struct {
						Hostname string `json:"hostname"`
					} `json:"general"`
				} `json:"nodes"`
			} `json:"topology"`
		} `json:"spec"`
		VMs []named `json:"vms"`
	}

	if err := json.Unmarshal(stdin, &exp); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(exp.Spec.Topology.Nodes)+len(exp.VMs))

	for _, node := range exp.Spec.Topology.Nodes {
		names = append(names, node.General.Hostname)
	}

	for _, vm := range exp.VMs {
		names = append(names, vm.Name)
	}

	return names, nil
}

// inputDenied logs, and returns the 403 for, a request for the input of a
// user program of kind for the experiment name.
func inputDenied(r *http.Request, kind, name string) error {
	plog.Warn(plog.TypeSecurity, "getting experiment "+kind+" input not allowed", "user", middleware.UserFromContext(r.Context()),
		"exp", name)

	return weberror.New(http.StatusForbidden, "getting the "+kind+" input of experiment "+name+" not allowed")
}
