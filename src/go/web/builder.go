package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
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

type builder struct {
	Topology map[string]any `json:"topology"`
	VLANs    map[string]int `json:"vlans"`
	Scenario string         `json:"scenario"`
	Name     string         `json:"name"`
	XML      string         `json:"builderXML"`
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
	)

	if !role.Allowed("experiments", "create") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.NewWebError(err, "reading request body").
			SetStatus(http.StatusInternalServerError)
	}

	var req builder

	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.NewWebError(err, "unmarshaling request body")
	}

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

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
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "topology with same name already exists").
				WithMetadata("type", "topology", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, "%s", lines[0]).
				WithMetadata("type", "topology", true).
				WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to create new topology").
			WithMetadata("type", "topology", true)
	}

	// publish new topology

	config.Spec = nil
	config.Status = nil

	body, err = json.Marshal(config)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling topology %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", config.FullName()),
		bt.NewResource("config", config.FullName(), "create"),
		body,
	)

	if err := cache.LockExperimentForCreation(req.Name); err != nil {
		err := weberror.NewWebError(err, "locking experiment for creation")

		return err.SetStatus(http.StatusConflict)
	}

	defer cache.UnlockExperiment(req.Name)

	if req.Scenario != "" {
		scenario, err := store.NewConfig("scenario/" + req.Scenario)
		if err != nil {
			return weberror.NewWebError(err, "creating scenario config")
		}

		err = store.Get(scenario)
		if err != nil {
			return weberror.NewWebError(nil, "scenario %s doesn't exist", req.Scenario)
		}

		// add this new topology to the given scenario

		addScenarioTopology(scenario, req.Name)

		err = store.Update(scenario)
		if err != nil {
			err := weberror.NewWebError(err, "updating scenario %s", req.Scenario)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	// create new experiment

	opts := []experiment.CreateOption{
		experiment.CreateWithName(req.Name),
		experiment.CreateWithTopology(req.Name),
		experiment.CreateWithScenario(req.Scenario),
		experiment.CreateWithVLANAliases(req.VLANs),
	}

	if err := experiment.Create(ctx, opts...); err != nil {
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "experiment with same name already exists").
				WithMetadata("type", "experiment", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, "%s", lines[0]).
				WithMetadata("type", "experiment", true).
				WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to create new experiment").
			WithMetadata("type", "experiment", true)
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn(plog.TypeSystem, warn.Error())
		}
	}

	// publish new experiment

	exp, err := experiment.Get(req.Name)
	if err != nil {
		err := weberror.NewWebError(err, "getting experiment %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	config, _ = store.NewConfig("experiment/" + req.Name)
	config.Metadata = exp.Metadata

	body, _ = json.Marshal(config)

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", config.FullName()),
		bt.NewResource("config", config.FullName(), "create"),
		body,
	)

	vms, _ := vm.List(req.Name)

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", req.Name),
		bt.NewResource("experiment", req.Name, "create"),
		body,
	)

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
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
//nolint:funlen,maintidx // handler
func UpdateExperimentFromBuilder(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateExperimentFromBuilder")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("experiments", "update") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"updating experiment from builder not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"updating experiments not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.NewWebError(err, "reading request body").
			SetStatus(http.StatusInternalServerError)
	}

	var req builder

	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.NewWebError(err, "unmarshaling request body")
	}

	if err := validateBuilderRequest(req); err != nil {
		return weberror.NewWebError(err, "invalid builder request")
	}

	// update existing topology

	topo, err := config.Get(store.ConfigFullName("topology", req.Name), false)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "topology with same name doesn't exist yet").
				WithMetadata("type", "topology", true)
		}

		return weberror.NewWebError(err, "getting topology %s", req.Name).
			SetStatus(http.StatusInternalServerError)
	}

	if topo.Metadata.Annotations == nil {
		topo.Metadata.Annotations = make(store.Annotations)
	}

	fileBacked := topo.HasAnnotation(config.BuilderXMLFileAnnotation)
	if !fileBacked {
		topo.Metadata.Annotations[config.BuilderXMLAnnotation] = req.XML
	}
	topo.Spec = req.Topology

	if err := config.Update(topo.FullName(), topo); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "topology with same name doesn't exist yet").
				WithMetadata("type", "topology", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, "%s", lines[0]).
				WithMetadata("type", "topology", true).
				WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to update existing topology").
			WithMetadata("type", "topology", true)
	}

	if fileBacked {
		if err := config.WriteBuilderXMLFile(*topo, []byte(req.XML)); err != nil {
			return weberror.NewWebError(err, "updating builder XML file").
				SetStatus(http.StatusInternalServerError)
		}
	}

	// Grab this now, before we clear the spec from the toplology config, just in
	// case we need it later.
	topoSpec, err := types.DecodeTopologyFromConfig(*topo)
	if err != nil {
		err := weberror.NewWebError(err, "decoding topology %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	// publish updated topology

	topo.Spec = nil
	topo.Status = nil

	body, err = json.Marshal(topo)
	if err != nil {
		err := weberror.NewWebError(err, "marshaling topology %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", topo.FullName()),
		bt.NewResource("config", topo.FullName(), "update"),
		body,
	)

	// Create or update experiment using updated topology. It's possible that the
	// topology already existed (so it's being updated), but an experiment with
	// the same name doesn't exist yet (e.g., they created just the topology the
	// first time around, but after editing the topology they decided to also
	// create an experiment from it). As such, we need to support either creating
	// or updating an experiment here.

	exists := true

	exp, err := experiment.Get(req.Name)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			exists = false
		} else {
			err := weberror.NewWebError(
				err,
				"determining if experiment %s already exists",
				req.Name,
			)

			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	if exists {
		annotations := exp.Metadata.Annotations
		if annotations == nil {
			err := weberror.NewWebError(
				err,
				"unable to determine if experiment uses topology %s",
				req.Name,
			)

			return err.SetStatus(http.StatusInternalServerError)
		}

		t, ok := annotations["topology"]
		if !ok {
			err := weberror.NewWebError(
				err,
				"unable to determine if experiment uses topology %s",
				req.Name,
			)

			return err.SetStatus(http.StatusInternalServerError)
		}

		if t != req.Name {
			return weberror.NewWebError(
				err,
				"existing experiment not created from topology %s",
				req.Name,
			)
		}

		err := cache.LockExperimentForUpdate(req.Name)
		if err != nil {
			err := weberror.NewWebError(err, "locking experiment for update")

			return err.SetStatus(http.StatusConflict)
		}

		defer cache.UnlockExperiment(req.Name)

		// update existing experiment

		exp.Spec.SetTopology(topoSpec)
		exp.Spec.VLANs().SetAliases(req.VLANs)

		if req.Scenario != "" && annotations["scenario"] != req.Scenario {
			scenario, err := store.NewConfig("scenario/" + req.Scenario)
			if err != nil {
				return weberror.NewWebError(err, "creating scenario config")
			}

			if err := store.Get(scenario); err != nil {
				return weberror.NewWebError(err, "getting scenario %s", req.Scenario)
			}

			addScenarioTopology(scenario, req.Name)
			if err := store.Update(scenario); err != nil {
				return weberror.NewWebError(err, "updating scenario %s", req.Scenario).
					SetStatus(http.StatusInternalServerError)
			}

			scenarioSpec, err := types.DecodeScenarioFromConfig(*scenario)
			if err != nil {
				return weberror.NewWebError(err, "decoding scenario %s", req.Scenario)
			}

			if err := types.MergeScenariosForTopology(scenarioSpec, req.Name); err != nil {
				return weberror.NewWebError(err, "merging scenario %s", req.Scenario)
			}

			exp.Spec.SetScenario(scenarioSpec)
			exp.Metadata.Annotations["scenario"] = req.Scenario
		}

		err = exp.WriteToStore(false)
		if err != nil {
			err := weberror.NewWebError(err, "updating experiment %s", req.Name)

			return err.SetStatus(http.StatusInternalServerError)
		}
	} else {
		err := cache.LockExperimentForCreation(req.Name)
		if err != nil {
			err := weberror.NewWebError(err, "locking experiment for creation")

			return err.SetStatus(http.StatusConflict)
		}

		defer cache.UnlockExperiment(req.Name)

		if req.Scenario != "" {
			scenario, err := store.NewConfig("scenario/" + req.Scenario)
			if err != nil {
				return weberror.NewWebError(err, "creating scenario config")
			}

			err = store.Get(scenario)
			if err != nil {
				return weberror.NewWebError(nil, "scenario %s doesn't exist", req.Scenario)
			}

			// add this topology to the given scenario

			addScenarioTopology(scenario, req.Name)

			err = store.Update(scenario)
			if err != nil {
				err := weberror.NewWebError(err, "updating scenario %s", req.Scenario)

				return err.SetStatus(http.StatusInternalServerError)
			}
		}

		// create new experiment

		opts := []experiment.CreateOption{
			experiment.CreateWithName(req.Name),
			experiment.CreateWithTopology(req.Name),
			experiment.CreateWithScenario(req.Scenario),
			experiment.CreateWithVLANAliases(req.VLANs),
		}

		err = experiment.Create(ctx, opts...)
		if err != nil {
			if errors.Is(err, store.ErrExist) {
				return weberror.NewWebError(err, "experiment with same name already exists").
					WithMetadata("type", "experiment", true)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				cause := errors.Unwrap(err)
				lines := strings.Split(cause.Error(), "\n")

				return weberror.NewWebError(cause, "%s", lines[0]).
					WithMetadata("type", "experiment", true).
					WithMetadata("validation", cause.Error(), true)
			}

			return weberror.NewWebError(err, "unable to create new experiment").
				WithMetadata("type", "experiment", true)
		}

		if warns := notes.Warnings(ctx, false); warns != nil {
			for _, warn := range warns {
				plog.Warn(plog.TypeSystem, warn.Error())
			}
		}
	}

	// publish experiment

	exp, err = experiment.Get(req.Name)
	if err != nil {
		err := weberror.NewWebError(err, "getting experiment %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	config, _ := store.NewConfig("experiment/" + req.Name)
	config.Metadata = exp.Metadata

	body, _ = json.Marshal(config)

	action := "create"
	if exists {
		action = "update"
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", config.FullName()),
		bt.NewResource("config", config.FullName(), action),
		body,
	)

	vms, _ := vm.List(req.Name)

	body, err = marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		err := weberror.NewWebError(err, "marshaling experiment %s", req.Name)

		return err.SetStatus(http.StatusInternalServerError)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", req.Name),
		bt.NewResource("experiment", req.Name, action),
		body,
	)

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
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

// SaveBuilderTopology - POST /builder/save.
func SaveBuilderTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "SaveBuilderTopology")

	name := r.FormValue("filename")
	if name == "" {
		name = "export"
	}

	data := r.FormValue("xml")
	if data == "" {
		http.Error(w, "builder topology XML is required", http.StatusBadRequest)
		return
	}

	format := r.FormValue("format")
	if format == "" {
		format = "xml"
	}

	contentTypes := map[string]string{
		"svg": "image/svg+xml; charset=utf-8",
		"xml": "application/xml; charset=utf-8",
	}
	contentType, ok := contentTypes[format]
	if !ok {
		http.Error(w, "unsupported builder download format", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": name,
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

	if !config.HasBuilderXML(*topology) {
		return weberror.NewWebError(
			nil,
			"the %s topology does not include a builder XML config",
			vars["name"],
		)
	}

	body, err := config.BuilderXML(*topology)
	if err != nil {
		return weberror.NewWebError(err, "unable to load builder XML for topology %s", vars["name"]).
			SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(body)

	return nil
}
