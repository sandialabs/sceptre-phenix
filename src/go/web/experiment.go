package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"inet.af/netaddr"

	"phenix/api/experiment"
	"phenix/api/vm"
	ucache "phenix/util/cache"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/notes"
	"phenix/util/plog"
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

	topo, err := vm.Topology(name, ignore)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "unable to get experiment topology")
	}

	body, err := json.Marshal(topo)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, "unable to convert topology")
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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

	cacheKey := fmt.Sprintf("experiment|%s|search", name)

	val, ok := ucache.Get(cacheKey)
	if !ok {
		if _, err := vm.Topology(name, nil); err != nil {
			return weberror.New(http.StatusBadRequest, "error getting experiment topology")
		}

		val, _ = ucache.Get(cacheKey)
	}

	var (
		search, _ = val.(vm.TopologySearch)
		nodes     []int
	)

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

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// GetExperiments - GET /experiments.
func GetExperiments(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx   = r.Context()
		role  = middleware.RoleFromContext(ctx)
		query = r.URL.Query()
		size  = query.Get("screenshot")
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

	experiments, err := experiment.List()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiments", "err", err)
	}

	allowed := []*proto.Experiment{}

	for _, exp := range experiments {
		if !role.Allowed("experiments", "list", exp.Metadata.Name) {
			continue
		}

		// This will happen if another handler is currently acting on the
		// experiment.
		status := cache.IsExperimentLocked(exp.Metadata.Name)

		if status == "" {
			if exp.Running() {
				status = cache.StatusStarted
			} else {
				status = cache.StatusStopped
			}
		}

		// TODO: limit per-experiment VMs based on RBAC

		vms, err := vm.List(exp.Spec.ExperimentName())
		if err != nil {
			plog.Error(
				plog.TypeSystem,
				"listing VMs for experiment",
				"exp",
				exp.Spec.ExperimentName(),
				"err",
				err,
			)
		}

		if exp.Running() && size != "" {
			for i, v := range vms {
				if !v.Running {
					continue
				}

				screenshot, err := util.GetScreenshot(exp.Spec.ExperimentName(), v.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)

					continue
				}

				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)

				vms[i] = v
			}
		}

		allowed = append(allowed, util.ExperimentToProtobuf(exp, status, vms))
	}

	body, err := marshaler.Marshal(&proto.ExperimentList{Experiments: allowed})
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling experiments", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	_, _ = w.Write(body)

	return nil
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
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"creating experiments not allowed",
			"user",
			user,
		)

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

	if err := cache.LockExperimentForCreation(req.GetName()); err != nil {
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
				"error parsing experiment deploy mode ('%s') - using default of '%s'",
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
	}

	if req.GetWorkflowBranch() != "" {
		annotations := map[string]string{"phenix.workflow/branch": req.GetWorkflowBranch()}
		opts = append(opts, experiment.CreateWithAnnotations(annotations))
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		plog.Error(plog.TypeSystem, "creating experiment", "exp", req.GetName(), "err", err)

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn(plog.TypeSystem, "creating experiment", "warnings", warn)
		}
	}

	exp, err := experiment.Get(req.GetName())
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", req.GetName(), "err", err)

		return weberror.New(http.StatusInternalServerError, "")
	}

	vms, err := vm.List(req.GetName())
	if err != nil {
		plog.Error(plog.TypeSystem, "listing experiment VMs", "exp", req.GetName(), "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

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

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"new experiment created",
		"user",
		user,
		"experiment",
		req.GetName(),
	)
	w.WriteHeader(http.StatusNoContent)

	return nil
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
		plog.Warn(
			plog.TypeSecurity,
			"updating experiment not allowed",
			"user",
			user,
			"experiment",
			name,
		)
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
		err := weberror.NewWebError(err, "unable to get experiment %s details", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	if exp.Running() {
		err := weberror.NewWebError(err, "cannot update running experiment %s", name)

		return err.SetStatus(http.StatusBadRequest)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to parse update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	var vlans map[string]int

	if err := json.Unmarshal(body, &vlans); err != nil {
		err := weberror.NewWebError(err, "unable to parse update request for experiment %s", name)

		return err.SetStatus(http.StatusInternalServerError)
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
			err := weberror.NewWebError(err, "unable to write updated experiment %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment updated",
		"user",
		user,
		"experiment",
		name,
	)

	return nil
}

// GetExperiment - GET /experiments/{name}.
//
//nolint:funlen // handler
func GetExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx          = r.Context()
		role         = middleware.RoleFromContext(ctx)
		vars         = mux.Vars(r)
		name         = vars["name"]
		query        = r.URL.Query()
		size         = query.Get("screenshot")
		sortCol      = query.Get("sortCol")
		sortDir      = query.Get("sortDir")
		pageNum      = query.Get("pageNum")
		perPage      = query.Get("perPage")
		showDNB      = query.Get("show_dnb") != ""
		clientFilter = query.Get("filter")
	)

	if !role.Allowed("experiments", "get", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"getting  experiment not allowed",
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

	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "unable to get experiment %s from store", name)
	}

	vms, err := vm.List(name)
	if err != nil {
		plog.Error(plog.TypeSystem, "listing VMs for experiment", "exp", name, "err", err)
	}

	// This will happen if another handler is currently acting on the
	// experiment.
	status := cache.IsExperimentLocked(name)
	allowed := mm.VMs{}

	// Build a Boolean expression tree and determine
	// the fields that should be searched
	filterTree := mm.BuildTree(clientFilter)

	for _, vm := range vms {
		if vm.DoNotBoot && !showDNB {
			continue
		}

		// If the filter supplied could not be
		// parsed, do not add the VM
		if len(clientFilter) > 0 {
			if filterTree == nil {
				continue
			} else if !filterTree.Evaluate(&vm) {
				// If the search string could be parsed,
				// determine if the VM should be included
				continue
			}
		}

		if role.Allowed("vms", "list", fmt.Sprintf("%s/%s", name, vm.Name)) {
			if vm.Running && size != "" {
				screenshot, err := util.GetScreenshot(name, vm.Name, size)
				if err != nil {
					plog.Error(plog.TypeSystem, "getting screenshot", "err", err)
				} else {
					vm.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
						screenshot,
					)
				}
			}

			allowed = append(allowed, vm)
		}
	}

	if sortCol != "" && sortDir != "" {
		allowed.SortBy(sortCol, sortDir == sortAsc)
	}

	totalBeforePaging := len(allowed)

	if pageNum != "" && perPage != "" {
		n, _ := strconv.Atoi(pageNum)
		s, _ := strconv.Atoi(perPage)

		allowed = allowed.Paginate(n, s)
	}

	experiment := util.ExperimentToProtobuf(*exp, status, allowed)
	experiment.VmCount = uint32(totalBeforePaging) //nolint:gosec // integer overflow conversion int -> uint32

	body, err := marshaler.Marshal(experiment)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s - %v", name, err)

		return err.SetStatus(http.StatusInternalServerError)
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

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

	if !role.Allowed("experiments", "delete", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"deleting experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	err := cache.LockExperimentForDeletion(name)
	if err != nil {
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

		return weberror.New(http.StatusBadRequest, err.Error())
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "delete", name),
		bt.NewResource("experiment", name, "delete"),
		nil,
	)

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"deleted experiment",
		"user",
		user,
		"exp",
		name,
	)
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
		plog.Warn(
			plog.TypeSecurity,
			"starting experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"starting experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	body, err := startExperiment(name)
	if err != nil {
		return err
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment started",
		"user",
		user,
		"exp",
		name,
	)

	return nil
}

// StopExperiment - POST /experiments/{name}/stop.
//

func StopExperiment(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		vars = mux.Vars(r)
		name = vars["name"]
	)

	if !role.Allowed("experiments/stop", "update", name) {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSecurity,
			"stopping experiment not allowed",
			"user",
			user,
			"exp",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"stopping experiment %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	body, err := stopExperiment(name)
	if err != nil {
		return err
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeAction,
		"experiment stopped",
		"user",
		user,
		"exp",
		name,
	)

	return nil
}
