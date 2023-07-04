package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/cache"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	bt "phenix/web/broker/brokertypes"

	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/mux"
)

type builder struct {
	Topology map[string]interface{} `json:"topology"`
	VLANs    map[string]int         `json:"vlans"`
	Scenario string                 `json:"scenario"`
	Name     string                 `json:"name"`
	XML      string                 `json:"builderXML"`
}

// GET /builder
func GetBuilder(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "GetBuilder")

	if o.unbundled {
		tmpl := template.Must(template.New("builder.html").ParseFiles("web/public/builder.html"))
		tmpl.Execute(w, o.basePath)
	} else {
		bfs := util.NewBinaryFileSystem(
			&assetfs.AssetFS{
				Asset:     Asset,
				AssetDir:  AssetDir,
				AssetInfo: AssetInfo,
			},
		)

		bfs.ServeTemplate(w, "builder.html", o.basePath)
	}
}

// POST /experiments/builder
func CreateExperimentFromBuilder(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "CreateExperimentFromBuilder")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("experiments", "create") {
		err := weberror.NewWebError(nil, "creating experiments not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.NewWebError(err, "reading request body").SetStatus(http.StatusInternalServerError)
	}

	var req builder

	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.NewWebError(err, "unmarshaling request body")
	}

	// create new topology

	topo, _ := store.NewConfig("topology/" + req.Name)

	topo.Metadata.Annotations = store.Annotations{"builder-xml": req.XML}
	topo.Spec = req.Topology

	config, err := config.Create(config.CreateFromConfig(topo), config.CreateWithValidation())
	if err != nil {
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "topology with same name already exists").WithMetadata("type", "topology", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, lines[0]).WithMetadata("type", "topology", true).WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to create new topology").WithMetadata("type", "topology", true)
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
		scenario, _ := store.NewConfig("scenario/" + req.Scenario)

		if err := store.Get(scenario); err != nil {
			return weberror.NewWebError(nil, "scenario %s doesn't exist", req.Scenario)
		}

		// add this new topology to the given scenario

		topo := scenario.Metadata.Annotations["topology"]
		topo = fmt.Sprintf("%s,%s", topo, req.Name)

		scenario.Metadata.Annotations["topology"] = topo

		if err := store.Update(scenario); err != nil {
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
			return weberror.NewWebError(err, "experiment with same name already exists").WithMetadata("type", "experiment", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, lines[0]).WithMetadata("type", "experiment", true).WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to create new experiment").WithMetadata("type", "experiment", true)
	}

	if warns := notes.Warnings(ctx, true); warns != nil {
		for _, warn := range warns {
			plog.Warn("%v", warn)
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

	return nil
}

// PUT /experiments/builder
func UpdateExperimentFromBuilder(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "UpdateExperimentFromBuilder")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("experiments", "update") {
		err := weberror.NewWebError(nil, "updating experiments not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return weberror.NewWebError(err, "reading request body").SetStatus(http.StatusInternalServerError)
	}

	var req builder

	if err := json.Unmarshal(body, &req); err != nil {
		return weberror.NewWebError(err, "unmarshaling request body")
	}

	// update existing topology

	topo, _ := store.NewConfig("topology/" + req.Name)

	topo.Metadata.Annotations = store.Annotations{"builder-xml": req.XML}
	topo.Spec = req.Topology

	if err := config.Update(topo.FullName(), topo); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "topology with same name doesn't exist yet").WithMetadata("type", "topology", true)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, lines[0]).WithMetadata("type", "topology", true).WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to update existing topology").WithMetadata("type", "topology", true)
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
			err := weberror.NewWebError(err, "determining if experiment %s already exists", req.Name)
			return err.SetStatus(http.StatusInternalServerError)
		}
	}

	if exists {
		annotations := exp.Metadata.Annotations
		if annotations == nil {
			err := weberror.NewWebError(err, "unable to determine if experiment uses topology %s", req.Name)
			return err.SetStatus(http.StatusInternalServerError)
		}

		t, ok := annotations["topology"]
		if !ok {
			err := weberror.NewWebError(err, "unable to determine if experiment uses topology %s", req.Name)
			return err.SetStatus(http.StatusInternalServerError)
		}

		if t != req.Name {
			return weberror.NewWebError(err, "existing experiment not created from topology %s", req.Name)
		}

		if err := cache.LockExperimentForUpdate(req.Name); err != nil {
			err := weberror.NewWebError(err, "locking experiment for update")
			return err.SetStatus(http.StatusConflict)
		}

		defer cache.UnlockExperiment(req.Name)

		// update existing experiment

		exp.Spec.SetTopology(topoSpec)

		if err := exp.WriteToStore(false); err != nil {
			err := weberror.NewWebError(err, "updating experiment %s", req.Name)
			return err.SetStatus(http.StatusInternalServerError)
		}
	} else {
		if err := cache.LockExperimentForCreation(req.Name); err != nil {
			err := weberror.NewWebError(err, "locking experiment for creation")
			return err.SetStatus(http.StatusConflict)
		}

		defer cache.UnlockExperiment(req.Name)

		if req.Scenario != "" {
			scenario, _ := store.NewConfig("scenario/" + req.Scenario)

			if err := store.Get(scenario); err != nil {
				return weberror.NewWebError(nil, "scenario %s doesn't exist", req.Scenario)
			}

			// add this topology to the given scenario

			topo := scenario.Metadata.Annotations["topology"]
			topo = fmt.Sprintf("%s,%s", topo, req.Name)

			scenario.Metadata.Annotations["topology"] = topo

			if err := store.Update(scenario); err != nil {
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
				return weberror.NewWebError(err, "experiment with same name already exists").WithMetadata("type", "experiment", true)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				cause := errors.Unwrap(err)
				lines := strings.Split(cause.Error(), "\n")

				return weberror.NewWebError(cause, lines[0]).WithMetadata("type", "experiment", true).WithMetadata("validation", cause.Error(), true)
			}

			return weberror.NewWebError(err, "unable to create new experiment").WithMetadata("type", "experiment", true)
		}

		if warns := notes.Warnings(ctx, false); warns != nil {
			for _, warn := range warns {
				plog.Warn("%v", warn)
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

	return nil
}

// POST /builder/save
func SaveBuilderTopology(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "SaveBuilderTopology")

	name := r.FormValue("filename")
	if name == "" {
		name = "export"
	}

	format := r.FormValue("format")
	if format == "" {
		format = "xml"
	}

	plog.Info("saving builder file", "file", name, "format", format)

	data, err := url.QueryUnescape(r.FormValue("xml"))
	if err != nil {
		msg := "unable to decode builder topology XML"
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	http.ServeContent(w, r, "", time.Now(), bytes.NewReader([]byte(data)))
}

// GET /builder/topologies
func GetBuilderTopologies(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetBuilderTopologies")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("configs", "list") {
		err := weberror.NewWebError(nil, "listing topologies not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	topologies, err := config.List("topology")
	if err != nil {
		err := weberror.NewWebError(err, "unable to get topologies from store")
		return err.SetStatus(http.StatusInternalServerError)
	}

	allowed := []string{}
	for _, topo := range topologies {
		if role.Allowed("topologies", "list", topo.Metadata.Name) {
			if topo.HasAnnotation("builder-xml") {
				allowed = append(allowed, topo.Metadata.Name)
			}
		}
	}

	body, err := json.Marshal(util.WithRoot("topologies", allowed))
	if err != nil {
		err := weberror.NewWebError(err, "marshaling list of builder topologies")
		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

	return nil
}

// GET /builder/topologies/{name}
func GetBuilderTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetBuilderTopology")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = store.ConfigFullName("topology", vars["name"])
	)

	if !role.Allowed("configs", "list", name) {
		err := weberror.NewWebError(nil, "getting topology %s not allowed for %s", vars["name"], ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	topology, err := config.Get(name, false)
	if err != nil {
		err := weberror.NewWebError(err, "unable to get topology %s from store", vars["name"])
		return err.SetStatus(http.StatusInternalServerError)
	}

	if !topology.HasAnnotation("builder-xml") {
		return weberror.NewWebError(nil, "the %s topology does not include a builder XML config", vars["name"])
	}

	body := []byte(topology.Metadata.Annotations["builder-xml"])

	w.Header().Set("Content-Type", "application/xml")
	w.Write(body)

	return nil
}
