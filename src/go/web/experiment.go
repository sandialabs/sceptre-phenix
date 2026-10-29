package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"inet.af/netaddr"

	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

// GetExperimentTopology - GET /experiments/{name}/topology[?ignore=MGMT].
func GetExperimentTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetExperimentTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]

		query  = r.URL.Query()
		ignore = query["ignore"]
	)

	if !role.Allowed("experiments/topology", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment topology not allowed",
			"user",
			user,
			"experiment",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	topo, err := vm.Topology(ctx, name, ignore)
	if requestCanceled(r, err) {
		return nil
	}

	if err != nil {
		return weberror.New(http.StatusBadRequest, "unable to get experiment topology")
	}

	body, err := json.Marshal(topo)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "unable to convert topology")
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// SearchExperimentTopology - GET /experiments/{name}/topology/search?hostname=xyz&vlan=abc.
func SearchExperimentTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SearchExperimentTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = vars["name"]

		query = r.URL.Query()
	)

	if !role.Allowed("experiments/topology", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"searching experiment topology not allowed",
			"user",
			user,
			"experiment",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	search, err := vm.TopologySearchIndex(name)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "error getting experiment topology")
	}

	var nodes []int

	// TODO: how to handle multiple terms? AND or OR?

	for term, values := range query {
		value := values[0]

		switch strings.ToLower(term) {
		case "hostname":
			if node, ok := search.Hostname[value]; ok {
				nodes = append(nodes, node)
			}
		case "disk":
			nodes = append(nodes, search.Disk[value]...)
		case "node-type":
			nodes = append(nodes, search.Type[value]...)
		case "os-type":
			nodes = append(nodes, search.OSType[value]...)
		case "label":
			nodes = append(nodes, search.Label[value]...)
		case "annotation":
			nodes = append(nodes, search.Annotation[value]...)
		case "vlan":
			nodes = append(nodes, search.VLAN[value]...)
		case "ip":
			if net, err := netaddr.ParseIPPrefix(value); err == nil {
				for k, v := range search.IP {
					ip, ipErr := netaddr.ParseIP(k)
					if ipErr != nil {
						continue
					}

					if net.Contains(ip) {
						nodes = append(nodes, v...)
					}
				}
			} else {
				nodes = append(nodes, search.IP[value]...)
			}
		}
	}

	body, err := json.Marshal(util.WithRoot("nodes", nodes))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "error marshaling search results")
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// GetExperiments - GET /experiments.
//
// With vms=false the VMs are left out and nothing is asked of minimega (counts come from each topology); with
// pageNum and perPage, only the experiments on that page are read from minimega.
func GetExperiments(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		noVMs = query.Get("vms") == "false"
	)

	if !role.Allowed("experiments", "list") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"listing experiments not allowed",
			"user",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	page, pageSize, err := util.ParsePage(query.Get("pageNum"), query.Get("perPage"))
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	experiments, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)

		// an empty list would read as no experiments
		if len(experiments) == 0 {
			return weberror.New(http.StatusInternalServerError, "unable to read experiments from the store")
		}
	}

	// whether minimega is busy depends on every experiment, not just those listed
	busy := anyExperimentLocked(experiments)

	experiments = slices.DeleteFunc(experiments, func(exp types.Experiment) bool {
		return !role.Allowed("experiments", "list", exp.Metadata.Name)
	})

	total := len(experiments)
	experiments = util.Page(experiments, page, pageSize)
	allowed := make([]*proto.Experiment, 0, len(experiments))

	// when minimega was read for the VMs listed, and whether it answered
	observed := freshness.Unobserved()

	// some experiments could not be read, so the list is incomplete
	observed.Stale = err != nil

	for _, exp := range experiments {
		status, percent := experimentStatus(exp)

		var pb *proto.Experiment

		if noVMs {
			pb = util.ExperimentSummaryToProtobuf(exp, status)
		} else {
			// TODO: limit per-experiment VMs based on RBAC
			vms, obs := listExperimentVMs(ctx, exp, busy)
			observed = observed.Older(obs)

			pb = util.ExperimentToProtobuf(exp, status, vms)
			pb.VmsUnknown = !obs.Known
		}

		pb.Percent = percent
		allowed = append(allowed, pb)
	}

	if requestGone(r) {
		return nil
	}

	body, err := marshaler.Marshal(&proto.ExperimentList{
		Experiments: allowed,
		Total:       uint32(total), //nolint:gosec // G115 counts fit in a uint32
	})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling experiments", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	freshness.SetObserved(w, observed)
	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// addVMScreenshots sets each running VM's screenshot, at the given size, in
// place, until ctx is done.
func addVMScreenshots(ctx context.Context, expName string, vms []mm.VM, size string) {
	for i, v := range vms {
		if !v.Running {
			continue
		}

		screenshot, err := util.GetScreenshotContext(ctx, expName, v.Name, size)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			plog.Error(plog.TypeSystem, "getting screenshot", "err", err)

			continue
		}

		vms[i].Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
			screenshot,
		)
	}
}

// anyExperimentLocked reports whether a handler holds the lock of any of the
// experiments, as while one starts or stops. Starting or stopping keeps
// minimega busy for as long as it takes, so asking it about VMs meanwhile
// would stall the experiment list until it finishes.
func anyExperimentLocked(experiments []types.Experiment) bool {
	for _, exp := range experiments {
		if cache.IsExperimentLocked(exp.Metadata.Name) != "" {
			return true
		}
	}

	return false
}

// listExperimentVMs lists the experiment's VMs, from its topology alone when
// minimega is busy, and how minimega was read for them. A running
// experiment's VMs listed from the topology are unknown, and stale.
func listExperimentVMs(ctx context.Context, exp types.Experiment, busy bool) ([]mm.VM, freshness.Observation) {
	if busy {
		obs := freshness.Unobserved()

		if exp.Running() {
			obs.Known, obs.Stale = false, true
		}

		return vm.ListConfigured(exp), obs
	}

	return vm.ListForObserved(ctx, &exp)
}

// CreateExperiment - POST /experiments.
//
//nolint:funlen // handler
func CreateExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("experiments", "create") {
		audit.Denied(r, audit.ExperimentCreate, "creating experiments not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	var req proto.CreateExperimentRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		plog.Error(plog.TypeSystem, "unmarshaling request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	annotations, nodeAnnotations, err := createAnnotations(&req)
	if err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if _, ok := nodeAnnotations[autotunnelAnnotation]; ok {
		vms, err := topologyVMs(req.GetTopology())
		if err != nil {
			return weberror.New(http.StatusBadRequest, err.Error())
		}

		if denied := forwardDeniedVM(role, req.GetName(), vms); denied != "" {
			audit.Denied(r, audit.ExperimentCreate, "creating port forwards not allowed", audit.Fields{
				Experiment: req.GetName(), VM: denied, Reason: "port forward forbidden",
			})

			return weberror.New(http.StatusForbidden, "forbidden")
		}
	}

	created := audit.Fields{Experiment: req.GetName()}

	if err := cache.LockExperimentForCreation(req.GetName()); err != nil {
		audit.Failure(r, audit.ExperimentCreate, "creating experiment failed", created, err)
		plog.Error(
			plog.TypeSystem,
			"locking experiment",
			"exp",
			req.GetName(),
			"action",
			"creation",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockExperiment(req.GetName())

	deployMode, err := common.ParseDeployMode(req.GetDeployMode())
	if err != nil {
		plog.Warn(
			plog.TypeSystem,
			fmt.Sprintf(
				"error parsing experiment deploy mode ('%s'): using default of '%s'",
				req.GetDeployMode(),
				common.DeployMode,
			),
		)
		deployMode = common.DeployMode
	}

	opts := []experiment.CreateOption{
		experiment.CreateWithName(req.GetName()),
		experiment.CreateWithTopology(req.GetTopology()),
		experiment.CreateWithScenario(req.GetScenario()),
		experiment.CreateWithVLANMin(int(req.GetVlanMin())),
		experiment.CreateWithVLANMax(int(req.GetVlanMax())),
		experiment.CreatedWithDisabledApplications(req.GetDisabledApps()),
		experiment.CreateWithDeployMode(deployMode),
		experiment.CreateWithDefaultBridge(req.GetDefaultBridge()),
		experiment.CreateWithGREMesh(req.GetUseGreMesh()),
		experiment.CreateWithAnnotations(annotations),
		experiment.CreateWithNodeAnnotations(nodeAnnotations),
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		plog.Error(plog.TypeSystem, "creating experiment", "exp", req.GetName(), "err", err)
		audit.Failure(r, audit.ExperimentCreate, "creating experiment failed", created, err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	audit.Success(r, audit.ExperimentCreate, "new experiment created", created)

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn(plog.TypeSystem, "creating experiment", "warnings", warn)
		}
	}

	exp, err := experiment.Get(req.GetName())
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", req.GetName(), "err", err)

		return weberror.New(http.StatusInternalServerError, "unable to get experiment")
	}

	// broadcast to every client, so read even once this request's client left
	vms := vm.ListFor(context.WithoutCancel(ctx), exp)

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling experiment", "err", req.GetName(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", req.GetName()),
		bt.NewResource("experiment", req.GetName(), "create"),
		body,
	)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// createAnnotations returns the experiment and node annotations a create
// request asks for. The workflow branch is stored as an experiment annotation,
// so the request may not also give that annotation.
func createAnnotations(
	req *proto.CreateExperimentRequest,
) (map[string]string, map[string]any, error) {
	const branchKey = "phenix.workflow/branch"

	annotations := maps.Clone(req.GetAnnotations())

	if branch := req.GetWorkflowBranch(); branch != "" {
		if _, ok := annotations[branchKey]; ok {
			return nil, nil, fmt.Errorf(
				"workflow_branch sets experiment annotation %q. Give only one of them",
				branchKey,
			)
		}

		if annotations == nil {
			annotations = make(map[string]string, 1)
		}

		annotations[branchKey] = branch
	}

	var nodeAnnotations map[string]any

	if len(req.GetNodeAnnotations()) > 0 {
		nodeAnnotations = make(map[string]any, len(req.GetNodeAnnotations()))

		for key, value := range req.GetNodeAnnotations() {
			nodeAnnotations[key] = value.AsInterface()
		}
	}

	return annotations, nodeAnnotations, nil
}

// UpdateExperiment - PATCH /experiments/{name}.
func UpdateExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments", "patch", name) {
		user := middleware.UserFromContext(ctx)
		audit.Denied(r, audit.ExperimentUpdate, "updating experiment not allowed", audit.Fields{Experiment: name})
		err := weberror.NewWebError(
			nil,
			"updating experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	exp, err := experiment.Get(name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotExist) {
			status = http.StatusNotFound
		}

		return weberror.NewWebError(err, "unable to get experiment %s details", name).SetStatus(status)
	}

	if exp.Running() {
		err := weberror.NewWebError(nil, "cannot update running experiment %s", name)

		return err.SetStatus(http.StatusBadRequest)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to read update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	var vlans map[string]int

	// a malformed body is the client's mistake
	if err := json.Unmarshal(body, &vlans); err != nil {
		return weberror.NewWebError(err, "unable to parse update request for experiment %s", name)
	}

	aliases := exp.Spec.VLANs().Aliases()

	if len(vlans) > 0 {
		for alias, id := range vlans {
			if _, ok := aliases[alias]; ok {
				aliases[alias] = id
			}
		}

		exp.Spec.VLANs().SetAliases(aliases)

		err := exp.WriteToStore(false)
		if err != nil {
			audit.Failure(r, audit.ExperimentUpdate, "updating experiment failed", audit.Fields{Experiment: name}, err)
			err := weberror.NewWebError(err, "unable to write updated experiment %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	audit.Success(r, audit.ExperimentUpdate, "experiment updated", audit.Fields{Experiment: name})

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// parseVMQuery reads the VM filter, label, host, sort and page parameters of a
// request.
func parseVMQuery(query url.Values) (util.VMQuery, error) {
	sortDir := query.Get("sortDir")

	page, pageSize, err := util.ParsePage(query.Get("pageNum"), query.Get("perPage"))
	if err != nil {
		return util.VMQuery{}, weberror.NewWebError(nil, "%v", err).SetStatus(http.StatusBadRequest)
	}

	labels := make([]util.VMLabel, 0, len(query["label"]))

	for _, raw := range query["label"] {
		label, ok := util.ParseVMLabel(raw)
		if !ok {
			return util.VMQuery{}, weberror.NewWebError(nil, "label %q is not key=value", raw).SetStatus(http.StatusBadRequest)
		}

		labels = append(labels, label)
	}

	hosts := query["host"]
	if slices.Contains(hosts, "") {
		return util.VMQuery{}, weberror.NewWebError(nil, "host must not be empty").SetStatus(http.StatusBadRequest)
	}

	return util.VMQuery{
		Filter:  query.Get("filter"),
		Labels:  labels,
		Hosts:   hosts,
		ShowDNB: query.Get("show_dnb") != "",
		SortCol: sortColumn(query.Get("sortCol"), sortDir),
		SortAsc: sortDir == sortAsc,
		Page:    page,
		Size:    pageSize,
	}, nil
}

// GetExperiment - GET /experiments/{name}.
//
// With vms=false the VMs are left out and nothing is asked of minimega:
// vm_count and delayed_vms come from the topology. The running experiment page
// uses it, as its VM rows arrive over the websocket (experiment/vms list).
func GetExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		vars  = mux.Vars(r)
		name  = vars["name"]
		query = r.URL.Query()
		size  = query.Get("screenshot")
		noVMs = query.Get("vms") == "false" && size == ""
	)

	if !role.Allowed("experiments", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting experiment not allowed",
			"user",
			user,
			"experiment",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"getting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	vmQuery, err := parseVMQuery(query)
	if err != nil {
		return err
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	var (
		vms      []mm.VM
		observed = freshness.Unobserved()
	)

	if noVMs {
		vms = vm.ListConfigured(*exp)
	} else {
		vms, observed = vm.ListForObserved(ctx, exp)
	}

	// This will happen if another handler is currently acting on the
	// experiment.
	status := cache.IsExperimentLocked(name)

	allowed, total := util.SelectVMs(name, vms, exp.Spec.Topology(), vmQuery, role)

	if size != "" {
		addVMScreenshots(ctx, name, allowed, size)
	}

	if requestGone(r) {
		return nil
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(total) //nolint:gosec // G115 counts fit in a uint32

	if noVMs {
		experiment.Vms = nil
	} else {
		experiment.VmsUnknown = !observed.Known
		freshness.SetObserved(w, observed)
	}

	body, err := marshaler.Marshal(experiment)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s: %v", name, err)

		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// DeleteExperiment - DELETE /experiments/{name}.
func DeleteExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	deleted := audit.Fields{Experiment: name}

	if !role.Allowed("experiments", "delete", name) {
		audit.Denied(r, audit.ExperimentDelete, "deleting experiment not allowed", deleted)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := cache.LockExperimentForDeletion(name)
	if err != nil {
		audit.Failure(r, audit.ExperimentDelete, "deleting experiment failed", deleted, err)
		plog.Error(
			plog.TypeSystem,
			"locking experiment",
			"exp",
			name,
			"action",
			"deletion",
			"err",
			err,
		)

		return weberror.New(http.StatusConflict, err.Error())
	}

	defer cache.UnlockExperiment(name)

	err = experiment.Delete(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "deleting experiment", "exp", name, "err", err)
		audit.Failure(r, audit.ExperimentDelete, "deleting experiment failed", deleted, err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "delete", name),
		bt.NewResource("experiment", name, "delete"),
		nil,
	)

	audit.Success(r, audit.ExperimentDelete, "deleted experiment", deleted)
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// StartExperiment - POST /experiments/{name}/start.
//

func StartExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/start", "update", name) {
		user := middleware.UserFromContext(ctx)
		audit.Denied(r, audit.ExperimentStart, "starting experiment not allowed", audit.Fields{Experiment: name})
		err := weberror.NewWebError(
			nil,
			"starting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if err := startExperiment(name); err != nil {
		audit.Failure(r, audit.ExperimentStart, "starting experiment failed", audit.Fields{Experiment: name}, err)

		return err
	}

	audit.Success(r, audit.ExperimentStart, "experiment started", audit.Fields{Experiment: name})

	// The broadcast leaves the VM list out; API callers still get it.
	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s", name)
	}

	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vm.ListFor(ctx, exp)))
	if err != nil {
		return weberror.NewWebError(err, "unable to marshal experiment %s", name)
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// StopExperiment - POST /experiments/{name}/stop?force=true marks the
// experiment stopped even when minimega cannot kill its VMs.
func StopExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/stop", "update", name) {
		user := middleware.UserFromContext(ctx)
		audit.Denied(r, audit.ExperimentStop, "stopping experiment not allowed", audit.Fields{Experiment: name})
		err := weberror.NewWebError(
			nil,
			"stopping experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	force, err := parseForce(r.URL.Query())
	if err != nil {
		return err
	}

	fields := audit.Fields{Experiment: name}
	if force {
		fields.Extra = []any{"phenix.experiment.force", true}
	}

	body, err := stopExperiment(name, force)
	if err != nil {
		audit.Failure(r, audit.ExperimentStop, "stopping experiment failed", fields, err)

		return err
	}

	audit.Success(r, audit.ExperimentStop, "experiment stopped", fields)

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// parseForce reports whether the force query parameter is set. A bare force
// is refused rather than taken as a request to force.
func parseForce(q url.Values) (bool, error) {
	if !q.Has("force") {
		return false, nil
	}

	value := q.Get("force")

	force, err := strconv.ParseBool(value)
	if err != nil {
		return false, weberror.NewWebError(err, "invalid force value %q", value)
	}

	return force, nil
}
