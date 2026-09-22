package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	builderFilenameForm = "filename"
	builderXMLFormat    = "xml"
)

type builder struct {
	Topology map[string]any `json:"topology"`
	VLANs    map[string]int `json:"vlans"`
	Scenario string         `json:"scenario"`
	Name     string         `json:"name"`
	XML      string         `json:"builderXML"`
}

// decodeBuilderRequest reads a Builder payload from the request body. The
// caller validates it, because the topology endpoints take the topology name
// from the URL rather than from the payload.
func decodeBuilderRequest(r *http.Request) (builder, *weberror.WebError) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return builder{}, weberror.NewWebError(err, "reading request body").
			SetStatus(http.StatusInternalServerError)
	}

	var req builder
	if err := json.Unmarshal(body, &req); err != nil {
		return builder{}, weberror.NewWebError(err, "unmarshaling request body")
	}

	return req, nil
}

func validateBuilderRequest(req builder) error {
	switch {
	case strings.TrimSpace(req.Name) == "":
		return errors.New("topology name is required")
	case req.Topology == nil:
		return errors.New("topology spec is required")
	case strings.TrimSpace(req.XML) == "":
		return errors.New("builder XML is required")
	default:
		return nil
	}
}

func addScenarioTopology(scenario *store.Config, topology string) {
	if scenario.Metadata.Annotations == nil {
		scenario.Metadata.Annotations = make(store.Annotations)
	}

	topologies := strings.Split(scenario.Metadata.Annotations["topology"], ",")
	for _, existing := range topologies {
		if strings.TrimSpace(existing) == topology {
			return
		}
	}

	topologies = append(topologies, topology)
	nonempty := topologies[:0]
	for _, name := range topologies {
		if name = strings.TrimSpace(name); name != "" {
			nonempty = append(nonempty, name)
		}
	}

	scenario.Metadata.Annotations["topology"] = strings.Join(nonempty, ",")
}

func getBuilderScenario(role rbac.Role, user, name string) (*store.Config, error) {
	fullName := store.ConfigFullName("scenario", name)
	if !role.Allowed("configs", "get", fullName) ||
		!role.Allowed("configs", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"using scenario from builder not allowed",
			"user",
			user,
			"scenario",
			name,
		)

		return nil, weberror.NewWebError(
			nil,
			"using scenario %s not allowed for %s",
			name,
			user,
		).SetStatus(http.StatusForbidden)
	}

	scenario, err := store.NewConfig(fullName)
	if err != nil {
		return nil, weberror.NewWebError(err, "creating scenario config")
	}

	if err := store.Get(scenario); err != nil {
		return nil, weberror.NewWebError(err, "getting scenario %s", name)
	}

	return scenario, nil
}

func rejectRunningBuilderExperiment(exp *types.Experiment, name string) *weberror.WebError {
	if exp.Running() {
		return weberror.NewWebError(
			nil,
			"cannot update running experiment %s",
			name,
		).SetStatus(http.StatusBadRequest)
	}

	return nil
}

// rejectRunningTopologyExperiment refuses a topology write while a running
// experiment was built from that topology.
func rejectRunningTopologyExperiment(name string) *weberror.WebError {
	exp, err := experiment.Get(name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil
		}

		return weberror.NewWebError(err, "checking experiment %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	return rejectRunningExperimentForTopology(exp, name)
}

// rejectRunningExperimentForTopology refuses a write to the named topology when
// exp is running and was built from it. Experiments snapshot their topology at
// creation, so only the same-named experiment -- the one Builder would go on to
// update -- is worth guarding.
func rejectRunningExperimentForTopology(
	exp *types.Experiment,
	topology string,
) *weberror.WebError {
	if exp.Metadata.Annotations["topology"] != topology {
		return nil
	}

	return rejectRunningBuilderExperiment(exp, topology)
}

// updateBuilderTopology writes a Builder diagram and its generated spec onto an
// existing topology config, returning the stored config.
func updateBuilderTopology(req builder) (*store.Config, error) {
	topo, err := config.Get(store.ConfigFullName("topology", req.Name), false)
	if err != nil {
		return nil, err
	}

	if topo.Metadata.Annotations == nil {
		topo.Metadata.Annotations = make(store.Annotations)
	}

	// Mutate only the diagram and the spec. Replacing the annotation map here
	// would drop every other annotation the topology carries.
	topo.Metadata.Annotations[config.BuilderXMLAnnotation] = req.XML
	topo.Spec = req.Topology

	// config.Update validates too, but validating up front keeps a bad spec
	// from reaching the store and gives the caller the schema error.
	if err := types.ValidateConfigSpec(*topo); err != nil {
		return nil, err
	}

	if err := config.Update(topo.FullName(), topo); err != nil {
		return nil, err
	}

	return topo, nil
}

// builderValidationCause returns the error carrying the schema detail behind a
// validation failure. types.ValidateConfigSpec joins its sentinel and the
// schema error into a multi-error that [errors.Unwrap] does not traverse, so
// fall back to the error itself when it wraps no single cause.
func builderValidationCause(err error) error {
	if cause := errors.Unwrap(err); cause != nil {
		return cause
	}

	return err
}

// builderCreateError maps a create failure onto the web error the Builder UI
// expects, tagged with the resource kind ("topology" or "experiment") so the
// dialog can report which half of the create failed.
func builderCreateError(err error, kind string) *weberror.WebError {
	if errors.Is(err, store.ErrExist) {
		return weberror.NewWebError(err, "%s with same name already exists", kind).
			WithMetadata("type", kind, true)
	}

	if errors.Is(err, types.ErrValidationFailed) {
		cause := builderValidationCause(err)
		lines := strings.Split(cause.Error(), "\n")

		return weberror.NewWebError(cause, "%s", lines[0]).
			WithMetadata("type", kind, true).
			WithMetadata("validation", cause.Error(), true)
	}

	return weberror.NewWebError(err, "unable to create new %s", kind).
		WithMetadata("type", kind, true)
}

// loadBuilderExperiment returns the experiment Builder would update for the
// given topology name, or nil when no such experiment exists yet. It refuses
// experiments that are running or that were not created from this topology.
func loadBuilderExperiment(name string) (*types.Experiment, *weberror.WebError) {
	exp, err := experiment.Get(name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil, nil
		}

		return nil, weberror.NewWebError(
			err,
			"determining if experiment %s already exists",
			name,
		).SetStatus(http.StatusInternalServerError)
	}

	if err := rejectRunningBuilderExperiment(exp, name); err != nil {
		return nil, err
	}

	topology, ok := exp.Metadata.Annotations["topology"]
	if !ok {
		return nil, weberror.NewWebError(
			nil,
			"unable to determine if experiment uses topology %s",
			name,
		).SetStatus(http.StatusInternalServerError)
	}

	if topology != name {
		return nil, weberror.NewWebError(
			nil,
			"existing experiment not created from topology %s",
			name,
		)
	}

	return exp, nil
}

// publishBuilderExperiment broadcasts the experiment config and its protobuf
// representation after a Builder create or update.
func publishBuilderExperiment(name, action string) *weberror.WebError {
	exp, err := experiment.Get(name)
	if err != nil {
		return weberror.NewWebError(err, "getting experiment %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	expConfig, err := store.NewConfig("experiment/" + name)
	if err != nil {
		return weberror.NewWebError(err, "creating experiment config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	expConfig.Metadata = exp.Metadata

	body, err := json.Marshal(expConfig)
	if err != nil {
		return weberror.NewWebError(err, "marshaling experiment config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", expConfig.FullName()),
		bt.NewResource("config", expConfig.FullName(), action),
		body,
	)

	vms, _ := vm.List(name)

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		return weberror.NewWebError(err, "marshaling experiment %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", name),
		bt.NewResource("experiment", name, action),
		body,
	)

	return nil
}

// createBuilderExperiment creates the experiment that accompanies a Builder
// topology of the same name, logging any warnings the create produced.
func createBuilderExperiment(ctx context.Context, req builder, popWarnings bool) *weberror.WebError {
	opts := []experiment.CreateOption{
		experiment.CreateWithName(req.Name),
		experiment.CreateWithTopology(req.Name),
		experiment.CreateWithScenario(req.Scenario),
		experiment.CreateWithVLANAliases(req.VLANs),
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		return builderCreateError(err, "experiment")
	}

	for _, warn := range notes.Warnings(ctx, popWarnings) {
		plog.Warn(plog.TypeSystem, warn.Error())
	}

	return nil
}

func builderTopologyUpdateError(err error, name string) *weberror.WebError {
	if errors.Is(err, store.ErrNotExist) {
		return weberror.NewWebError(err, "topology %s doesn't exist yet", name).
			WithMetadata("type", "topology", true)
	}

	if errors.Is(err, types.ErrValidationFailed) {
		cause := builderValidationCause(err)
		lines := strings.Split(cause.Error(), "\n")

		return weberror.NewWebError(cause, "%s", lines[0]).
			WithMetadata("type", "topology", true).
			WithMetadata("validation", cause.Error(), true)
	}

	return weberror.NewWebError(err, "unable to update topology %s", name).
		WithMetadata("type", "topology", true)
}

// mergeBuilderVLANAliases rebuilds the experiment VLAN alias map from the
// aliases the saved topology names. Builder only submits aliases that carry an
// explicit VLAN ID, so IDs it leaves out are taken from the experiment rather
// than reset, and aliases the topology no longer references are dropped.
func mergeBuilderVLANAliases(
	vlans ifaces.VLANSpec,
	topo ifaces.TopologySpec,
	aliases map[string]int,
) {
	existing := vlans.Aliases()

	merged := make(map[string]int, len(existing))

	for _, node := range topo.Nodes() {
		for _, iface := range node.Network().Interfaces() {
			alias := iface.VLAN()
			if alias == "" {
				continue
			}

			if id, ok := aliases[alias]; ok {
				merged[alias] = id
			} else {
				merged[alias] = existing[alias]
			}
		}
	}

	vlans.SetAliases(merged)
}

// builderTopologyLocation returns the absolute path a created Builder topology
// is readable at. o.basePath is the prefix the UI is served behind and is
// normalized to start and end with "/", so prefer it over assuming the API is
// mounted at the server root.
func builderTopologyLocation(name string) string {
	base := o.basePath
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}

	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	return base + "api/v1/builder/topologies/" + url.PathEscape(name)
}

// publishBuilderTopology broadcasts a topology config summary after a Builder
// create or update. action is "create" or "update".
func publishBuilderTopology(topo *store.Config, action string) error {
	summary := *topo
	summary.Spec = nil
	summary.Status = nil

	body, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshaling topology %s: %w", topo.Metadata.Name, err)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", topo.FullName()),
		bt.NewResource("config", topo.FullName(), action),
		body,
	)

	return nil
}

// GetBuilder - GET /builder.
func GetBuilder(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetBuilder")

	if o.unbundled {
		tmpl := template.Must(template.New("builder.html").ParseFiles("web/public/builder.html"))
		_ = tmpl.Execute(w, o.basePath)
	} else {
		assets, err := GetAssets()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		bfs := util.NewBinaryFileSystem(assets)
		bfs.ServeTemplate(w, "builder.html", o.basePath)
	}
}

// CreateExperimentFromBuilder - POST /experiments/builder.
//
//nolint:funlen // handler
func CreateExperimentFromBuilder(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateExperimentFromBuilder")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
	)

	if !role.Allowed("experiments", "create") {
		plog.Warn(
			plog.TypeSecurity,
			"creating experiment from builder not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"creating experiments not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	req, webErr := decodeBuilderRequest(r)
	if webErr != nil {
		return webErr
	}

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

	var (
		scenario *store.Config
		err      error
	)

	if req.Scenario != "" {
		scenario, err = getBuilderScenario(role, user, req.Scenario)
		if err != nil {
			return err
		}
	}

	if err := cache.LockExperimentForCreation(req.Name); err != nil {
		err := weberror.NewWebError(err, "locking experiment for creation")

		return err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(req.Name)

	// create new topology

	topo, err := store.NewConfig("topology/" + req.Name)
	if err != nil {
		return weberror.NewWebError(err, "creating topology config").
			WithMetadata("type", "topology", true)
	}

	topo.Metadata.Annotations = store.Annotations{config.BuilderXMLAnnotation: req.XML}
	topo.Spec = req.Topology

	config, err := config.Create(config.CreateFromConfig(topo), config.CreateWithValidation())
	if err != nil {
		return builderCreateError(err, "topology")
	}

	// publish new topology

	config.Spec = nil
	config.Status = nil

	body, err := json.Marshal(config)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling topology %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", config.FullName()),
		bt.NewResource("config", config.FullName(), "create"),
		body,
	)

	if scenario != nil {
		// add this new topology to the given scenario

		addScenarioTopology(scenario, req.Name)

		err = store.Update(scenario)
		if err != nil {
			err := weberror.NewWebError(err, "updating scenario %s", req.Scenario)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	// create new experiment

	if err := createBuilderExperiment(ctx, req, true); err != nil {
		return err
	}

	// publish new experiment

	if err := publishBuilderExperiment(req.Name, "create"); err != nil {
		return err
	}

	plog.Info(
		plog.TypeAction,
		"created experiment from builder",
		"user",
		user,
		"experiment",
		req.Name,
	)

	return nil
}

// UpdateExperimentFromBuilder - PUT /experiments/builder.
//
//nolint:funlen // handler
func UpdateExperimentFromBuilder(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateExperimentFromBuilder")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
	)

	if !role.Allowed("experiments", "update") {
		plog.Warn(
			plog.TypeSecurity,
			"updating experiment from builder not allowed",
			"user",
			user,
		)

		return weberror.NewWebError(
			nil,
			"updating experiments not allowed for %s",
			user,
		).SetStatus(http.StatusForbidden)
	}

	req, webErr := decodeBuilderRequest(r)
	if webErr != nil {
		return webErr
	}

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

	if err := cache.LockExperimentForUpdate(req.Name); err != nil {
		return weberror.NewWebError(err, "locking experiment for update").
			SetStatus(http.StatusConflict)
	}
	defer cache.UnlockExperiment(req.Name)

	exp, webErr := loadBuilderExperiment(req.Name)
	if webErr != nil {
		return webErr
	}

	exists := exp != nil

	var (
		scenario     *store.Config
		scenarioSpec ifaces.ScenarioSpec
		err          error
	)

	scenarioChanged := req.Scenario != "" &&
		(!exists || exp.Metadata.Annotations["scenario"] != req.Scenario)
	if scenarioChanged {
		scenario, err = getBuilderScenario(role, user, req.Scenario)
		if err != nil {
			return err
		}

		addScenarioTopology(scenario, req.Name)

		if exists {
			scenarioSpec, err = types.DecodeScenarioFromConfig(*scenario)
			if err != nil {
				return weberror.NewWebError(err, "decoding scenario %s", req.Scenario)
			}
			if err := types.MergeScenariosForTopology(scenarioSpec, req.Name); err != nil {
				return weberror.NewWebError(err, "merging scenario %s", req.Scenario)
			}
		}
	}

	topo, err := updateBuilderTopology(req)
	if err != nil {
		return builderTopologyUpdateError(err, req.Name)
	}
	if err := publishBuilderTopology(topo, "update"); err != nil {
		return weberror.NewWebError(err, "publishing topology %s", req.Name).
			SetStatus(http.StatusInternalServerError)
	}

	if scenario != nil {
		if err := store.Update(scenario); err != nil {
			return weberror.NewWebError(err, "updating scenario %s", req.Scenario).
				SetStatus(http.StatusInternalServerError)
		}
	}

	if exists {
		topoSpec, err := types.DecodeTopologyFromConfig(*topo)
		if err != nil {
			return weberror.NewWebError(err, "decoding topology %s", req.Name).
				SetStatus(http.StatusInternalServerError)
		}

		exp.Spec.SetTopology(topoSpec)

		// Init() applies defaults to the topology just assigned and guarantees
		// the VLAN spec exists. Without it ExperimentSpec.VLANs() hands back a
		// throwaway whenever the stored experiment carries no vlans key, and
		// the merged aliases below would be written to that and lost.
		if err := exp.Spec.Init(); err != nil {
			return weberror.NewWebError(err, "initializing experiment %s", req.Name).
				SetStatus(http.StatusInternalServerError)
		}

		mergeBuilderVLANAliases(exp.Spec.VLANs(), topoSpec, req.VLANs)

		if scenario != nil {
			exp.Spec.SetScenario(scenarioSpec)
			exp.Metadata.Annotations["scenario"] = req.Scenario
		}

		if err := exp.WriteToStore(false); err != nil {
			return weberror.NewWebError(err, "updating experiment %s", req.Name).
				SetStatus(http.StatusInternalServerError)
		}
	} else if err := createBuilderExperiment(ctx, req, false); err != nil {
		return err
	}

	action := "create"
	if exists {
		action = "update"
	}

	if err := publishBuilderExperiment(req.Name, action); err != nil {
		return err
	}

	plog.Info(
		plog.TypeAction,
		"experiment updated from builder",
		"user",
		user,
		"experiment",
		req.Name,
	)

	return nil
}

// CreateBuilderTopology - POST /builder/topologies.
func CreateBuilderTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateBuilderTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		user, _ = ctx.Value(middleware.ContextKeyUser).(string)
	)

	req, webErr := decodeBuilderRequest(r)
	if webErr != nil {
		return webErr
	}

	// Authorize before validating the payload so an unauthorized caller always
	// gets 403, and name the resource the way every other Builder check does so
	// a role scoped to particular topologies is enforced on create too.
	if !role.Allowed("configs", "create", store.ConfigFullName("topology", req.Name)) {
		plog.Warn(
			plog.TypeSecurity,
			"creating topology from builder not allowed",
			"user",
			user,
			"topology",
			req.Name,
		)

		return weberror.NewWebError(
			nil,
			"creating topology %s not allowed for %s",
			req.Name,
			user,
		).SetStatus(http.StatusForbidden)
	}

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

	if err := cache.LockBuilderTopology(req.Name); err != nil {
		return weberror.NewWebError(err, "locking topology for creation").
			SetStatus(http.StatusConflict)
	}
	defer cache.UnlockBuilderTopology(req.Name)

	if webErr := rejectRunningTopologyExperiment(req.Name); webErr != nil {
		return webErr
	}

	topo, err := store.NewConfig(store.ConfigFullName("topology", req.Name))
	if err != nil {
		return weberror.NewWebError(err, "creating topology config").
			WithMetadata("type", "topology", true)
	}

	topo.Metadata.Annotations = store.Annotations{config.BuilderXMLAnnotation: req.XML}
	topo.Spec = req.Topology

	created, err := config.Create(config.CreateFromConfig(topo), config.CreateWithValidation())
	if err != nil {
		return builderCreateError(err, "topology")
	}

	w.Header().Set("Location", builderTopologyLocation(created.Metadata.Name))
	w.WriteHeader(http.StatusCreated)

	if err := publishBuilderTopology(created, "create"); err != nil {
		plog.Error(
			plog.TypeSystem,
			"publishing topology created from builder",
			"topology",
			req.Name,
			"err",
			err,
		)
	}

	plog.Info(
		plog.TypeAction,
		"topology created from builder",
		"user",
		user,
		"topology",
		req.Name,
	)

	return nil
}

// UpdateBuilderTopology - PUT /builder/topologies/{name}.
func UpdateBuilderTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateBuilderTopology")

	var (
		ctx      = r.Context()
		role, _  = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		user, _  = ctx.Value(middleware.ContextKeyUser).(string)
		name     = mux.Vars(r)["name"]
		fullName = store.ConfigFullName("topology", name)
	)

	if !role.Allowed("configs", "update", fullName) {
		plog.Warn(
			plog.TypeSecurity,
			"updating topology from builder not allowed",
			"user",
			user,
			"topology",
			name,
		)

		return weberror.NewWebError(
			nil,
			"updating topology %s not allowed for %s",
			name,
			user,
		).SetStatus(http.StatusForbidden)
	}

	req, webErr := decodeBuilderRequest(r)
	if webErr != nil {
		return webErr
	}
	req.Name = name

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

	if err := cache.LockBuilderTopology(name); err != nil {
		return weberror.NewWebError(err, "locking topology for update").
			SetStatus(http.StatusConflict)
	}
	defer cache.UnlockBuilderTopology(name)

	if webErr := rejectRunningTopologyExperiment(name); webErr != nil {
		return webErr
	}

	topo, err := updateBuilderTopology(req)
	if err != nil {
		return builderTopologyUpdateError(err, name)
	}
	if err := publishBuilderTopology(topo, "update"); err != nil {
		return weberror.NewWebError(err, "publishing topology %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	plog.Info(
		plog.TypeAction,
		"topology updated from builder",
		"user",
		user,
		"topology",
		name,
	)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// SaveBuilderTopology - POST /builder/save.
func SaveBuilderTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SaveBuilderTopology")

	// The editor posts the document and its file name through a hidden form
	// whose fields already hold encodeURIComponent() results, so r.FormValue
	// leaves one layer of percent-encoding behind on each of them.
	name, err := url.QueryUnescape(r.FormValue(builderFilenameForm))
	if err != nil {
		http.Error(w, "unable to decode builder file name", http.StatusBadRequest)
		return
	}
	if name == "" {
		name = "export"
	}

	data, err := url.QueryUnescape(r.FormValue("xml"))
	if err != nil {
		http.Error(w, "unable to decode builder topology XML", http.StatusBadRequest)
		return
	}
	if data == "" {
		http.Error(w, "builder topology XML is required", http.StatusBadRequest)
		return
	}

	format := r.FormValue("format")
	if format == "" {
		format = builderXMLFormat
	}

	contentTypes := map[string]string{
		"svg":            "image/svg+xml; charset=utf-8",
		builderXMLFormat: "application/xml; charset=utf-8",
	}
	contentType, ok := contentTypes[format]
	if !ok {
		http.Error(w, "unsupported builder download format", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		builderFilenameForm: name,
	}))
	plog.Info(plog.TypeAction, "downloading builder file", "file", name, "format", format)
	http.ServeContent(w, r, "", time.Now(), bytes.NewReader([]byte(data)))
}

// GetBuilderTopologies - GET /builder/topologies.
func GetBuilderTopologies(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetBuilderTopologies")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("configs", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting builder topologies not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"listing topologies not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	topologies, err := config.List("topology")
	if err != nil {
		err := weberror.NewWebError(err, "unable to get topologies from store")

		return err.SetStatus(http.StatusInternalServerError)
	}

	allowed := []string{}

	for _, topo := range topologies {
		if role.Allowed("configs", "get", topo.FullName()) && config.HasBuilderXML(topo) {
			allowed = append(allowed, topo.Metadata.Name)
		}
	}

	body, err := json.Marshal(util.WithRoot("topologies", allowed))
	if err != nil {
		err := weberror.NewWebError(err, "marshaling list of builder topologies")

		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")

	_, _ = w.Write(body)

	return nil
}

// GetBuilderTopology - GET /builder/topologies/{name}.
func GetBuilderTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetBuilderTopology")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = store.ConfigFullName("topology", vars["name"])
	)

	if !role.Allowed("configs", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting builder topology not allowed",
			"user",
			user,
			"topo",
			vars["name"],
		)
		err := weberror.NewWebError(
			nil,
			"getting topology %s not allowed for %s",
			vars["name"],
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	topology, err := config.Get(name, false)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "unable to get topology %s from store", vars["name"]).
				SetStatus(http.StatusNotFound)
		}

		return weberror.NewWebError(err, "unable to get topology %s from store", vars["name"]).
			SetStatus(http.StatusInternalServerError)
	}

	body, ok := config.BuilderXML(*topology)
	if !ok {
		return weberror.NewWebError(
			nil,
			"the %s topology does not include a builder XML config",
			vars["name"],
		)
	}

	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(body)

	return nil
}
