package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/cache"
	"phenix/web/rbac"
	"phenix/web/weberror"

	bt "phenix/web/broker/brokertypes"

	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
)

// POST /workflow/apply/{branch}
func ApplyWorkflow(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "ApplyWorkflow")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		scope = vars["branch"]
		q     = r.URL.Query()
		tags  = ""
	)

	if !role.Allowed("workflow", "create") {
		err := weberror.NewWebError(nil, "applying phenix workflow is not allowed for user %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	// currently querries only are used to pass tags. However, this
	// is extensible for future fields as well.
	tags = strings.Join(q["tag"], ",")

	var (
		typ = r.Header.Get("Content-Type")
		cfg *store.Config
	)

	switch {
	case typ == "application/json": // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to read request data")
			return err.SetStatus(http.StatusInternalServerError)
		}

		cfg, err = store.NewConfigFromJSON(body, "{{BRANCH_NAME}}", scope)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse phenix workflow config")
		}
	case typ == "application/x-yaml":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}

		cfg, err = store.NewConfigFromYAML(body, "{{BRANCH_NAME}}", scope)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse phenix workflow config")
		}
	default:
		return weberror.NewWebError(nil, "must use application/json or application/x-yaml when providing phenix workflow config")
	}

	var wf workflow

	if err := mapstructure.Decode(cfg.Spec, &wf); err != nil {
		return weberror.NewWebError(err, "unable to parse phenix workflow config")
	}

	experiments, err := experiment.List()
	if err != nil {
		err := weberror.NewWebError(err, "unable to get list of experiments")
		return err.SetStatus(http.StatusInternalServerError)
	}

	var exps []types.Experiment

	for _, exp := range experiments {
		annotations := exp.Metadata.Annotations

		if annotations == nil {
			continue
		}

		if branch, ok := annotations["phenix.workflow/branch"]; ok {
			if branch == scope {
				exps = append(exps, exp)
			}
		}
	}

	switch len(exps) {
	case 0:
		expName := wf.ExperimentName()

		if expName == "" {
			return nil
		}

		if err := cache.LockExperimentForCreation(expName); err != nil {
			err := weberror.NewWebError(err, "unable to create new experiment")
			return err.SetStatus(http.StatusInternalServerError)
		}

		defer cache.UnlockExperiment(expName)

		annotations := map[string]string{"phenix.workflow/branch": scope}

		if tags != "" {
			annotations["phenix.workflow/tags"] = tags
		}

		opts := []experiment.CreateOption{
			experiment.CreateWithName(expName),
			experiment.CreateWithAnnotations(annotations),
			experiment.CreateWithTopology(wf.ExperimentTopology()),
			experiment.CreateWithScenario(wf.ExperimentScenario()),
			experiment.CreateWithVLANAliases(wf.VLANMappings()),
			experiment.CreateWithSchedules(wf.Schedules),
			experiment.CreateWithVLANMin(wf.VLANMin()),
			experiment.CreateWithVLANMax(wf.VLANMax()),
		}

		if err := experiment.Create(ctx, opts...); err != nil {
			err := weberror.NewWebError(err, "unable to create new experiment")
			return err.SetStatus(http.StatusInternalServerError)
		}

		if wf.AutoRestart() {
			cache.UnlockExperiment(expName)

			if _, err := startExperiment(expName); err != nil {
				return err
			}
		}
	case 1:
		exp := &exps[0]
		expName := exp.Metadata.Name

		if !wf.AutoUpdate() {
			return nil
		}

		if exp.Running() {
			if !wf.AutoRestart() {
				return nil
			}

			var err error

			if _, err = stopExperiment(expName); err != nil {
				return err
			}

			// Need to get the experiment again after it's stopped so the spec and
			// status we're working with are accurate (e.g., so when we update the store
			// later we don't write the old status).
			exp, err = experiment.Get(expName)
			if err != nil {
				err := weberror.NewWebError(err, "unable to update experiment %s", expName)
				return err.SetStatus(http.StatusInternalServerError)
			}
		}

		if err := cache.LockExperimentForUpdate(expName); err != nil {
			err := weberror.NewWebError(err, "unable to update experiment %s", expName)
			return err.SetStatus(http.StatusInternalServerError)
		}

		defer cache.UnlockExperiment(expName)

		var (
			annotations  = exp.Metadata.Annotations
			topoName     = wf.ExperimentTopology()
			scenarioName = wf.ExperimentScenario()
		)

		if topoName == "" {
			topoName = annotations["topology"]
		}

		if topoName == "" {
			err := weberror.NewWebError(fmt.Errorf("missing topology annotation"), "unable to update experiment with topology %s", topoName)
			return err.SetStatus(http.StatusInternalServerError)
		}

		topo, _ := store.NewConfig("topology/" + topoName)

		if err := store.Get(topo); err != nil {
			err := weberror.NewWebError(err, "unable to update experiment with topology %s", topoName)
			return err.SetStatus(http.StatusInternalServerError)
		}

		topoSpec, err := types.DecodeTopologyFromConfig(*topo)
		if err != nil {
			err := weberror.NewWebError(err, "unable to update experiment with topology %s", topoName)
			return err.SetStatus(http.StatusInternalServerError)
		}

		exp.Spec.SetTopology(topoSpec)
		exp.Metadata.Annotations["topology"] = topoName

		if scenarioName == "" {
			scenarioName = annotations["scenario"]
		}

		if scenarioName != "" {
			scenario, _ := store.NewConfig("scenario/" + scenarioName)

			if err := store.Get(scenario); err != nil {
				err := weberror.NewWebError(err, "unable to update experiment with scenario %s", scenarioName)
				return err.SetStatus(http.StatusInternalServerError)
			}

			scenSpec, err := types.DecodeScenarioFromConfig(*scenario)
			if err != nil {
				err := weberror.NewWebError(err, "unable to update experiment with scenario %s", scenarioName)
				return err.SetStatus(http.StatusInternalServerError)
			}

			if err := types.MergeScenariosForTopology(scenSpec, topoName); err != nil {
				return weberror.NewWebError(err, "merging scenarios")
			}

			exp.Spec.SetScenario(scenSpec)
			exp.Metadata.Annotations["scenario"] = scenarioName
		}

		// default is to not override existing tags if no new tags are passed
		// TODO: perhaps sorting tags and only updating those that are passed
		// while leaving old tags that have not been overriden
		if tags != "" {
			exp.Metadata.Annotations["phenix.workflow/tags"] = tags
		}

		var (
			aliases   = make(map[string]int)
			wfAliases = wf.VLANMappings()
		)

		// Reset VLAN aliases using information from topology node network
		// interfaces just in case the topology includes updates changing VLAN alias
		// names.
		for _, node := range exp.Spec.Topology().Nodes() {
			// TODO: only consider nodes schedulable by minimega? Or should HIL nodes
			// be taken into account here still as well?
			if node.Network() == nil {
				continue
			}

			for _, iface := range node.Network().Interfaces() {
				alias := iface.VLAN()

				// Use VLAN ID from workflow config if specified. Otherwise, set VLAN ID
				// to 0 so minimega can choose accordingly.
				if id, ok := wfAliases[alias]; ok {
					aliases[alias] = id
				} else {
					aliases[alias] = 0
				}
			}
		}

		exp.Spec.VLANs().SetAliases(aliases)

		var (
			schedules   = make(map[string]string)
			wfSchedules = wf.ScheduleMappings()
		)

		// Reset VM schedules using information from topology nodes just in case the
		// topology includes updates changing node hostnames.
		for _, node := range exp.Spec.Topology().Nodes() {
			if node.External() {
				continue
			}

			hostname := node.General().Hostname()

			// Use cluster host from workflow config if specified.
			if host, ok := wfSchedules[hostname]; ok {
				schedules[hostname] = host
			}
		}

		exp.Spec.SetSchedule(schedules)
		exp.Spec.SetVLANRange(wf.VLANMin(), wf.VLANMax(), true)

		if err := exp.WriteToStore(false); err != nil {
			err := weberror.NewWebError(err, "unable to write updated experiment %s", expName)
			return err.SetStatus(http.StatusInternalServerError)
		}

		if err := experiment.Reconfigure(expName); err != nil {
			return weberror.NewWebError(err, "unable to reconfigure updated experiment %s", expName)
		}

		if wf.AutoRestart() {
			cache.UnlockExperiment(expName)

			if _, err := startExperiment(expName); err != nil {
				return err
			}
		}
	default:
		err := weberror.NewWebError(nil, "more than one experiment is mapped to workflow branch %s", scope)
		return err.SetStatus(http.StatusInternalServerError)
	}

	return nil
}

// POST /workflow/configs/{branch}
func WorkflowUpsertConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "WorkflowUpsertConfig")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		scope = vars["branch"]
	)

	var (
		typ = r.Header.Get("Content-Type")
		cfg *store.Config
	)

	switch {
	case typ == "application/json": // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to read request data")
			return err.SetStatus(http.StatusInternalServerError)
		}

		cfg, err = store.NewConfigFromJSON(body, "{{BRANCH_NAME}}", scope)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse JSON config")
		}
	case typ == "application/x-yaml":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}

		cfg, err = store.NewConfigFromYAML(body, "{{BRANCH_NAME}}", scope)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse YAML config")
		}
	default:
		return weberror.NewWebError(nil, "must use application/json or application/x-yaml when providing topology/scenario config")
	}

	var (
		name      = fmt.Sprintf("%s/%s", cfg.Kind, cfg.Metadata.Name)
		tester, _ = store.NewConfig(name)
		exists    = true
	)

	if err := store.Get(tester); err != nil {
		if !errors.Is(err, store.ErrNotExist) {
			err := weberror.NewWebError(err, "checking store for config")
			return err.SetStatus(http.StatusInternalServerError)
		}

		exists = false
	}

	if exists {
		if !role.Allowed("configs", "update", name) {
			err := weberror.NewWebError(nil, "updating config %s not allowed for %s", name, ctx.Value("user").(string))
			return err.SetStatus(http.StatusForbidden)
		}

		if err := config.Update(name, cfg); err != nil {
			if errors.Is(err, store.ErrNotExist) {
				return weberror.NewWebError(err, "config to update (%s) does not exist", name)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				cause := errors.Unwrap(err)
				lines := strings.Split(cause.Error(), "\n")

				return weberror.NewWebError(cause, lines[0]).WithMetadata("validation", cause.Error(), true)
			}

			if errors.Is(err, store.ErrInvalidFormat) {
				cause := errors.Unwrap(err)
				return weberror.NewWebError(cause, "invalid formatting").WithMetadata("validation", cause.Error(), true)
			}

			return weberror.NewWebError(err, "unable to update config %s", name)
		}
	} else {
		if !role.Allowed("configs", "create") {
			err := weberror.NewWebError(nil, "creating configs not allowed for %s", ctx.Value("user").(string))
			return err.SetStatus(http.StatusForbidden)
		}

		var (
			opts = []config.CreateOption{config.CreateFromConfig(cfg), config.CreateWithValidation()}
			err  error
		)

		cfg, err = config.Create(opts...)
		if err != nil {
			if errors.Is(err, store.ErrExist) {
				return weberror.NewWebError(err, "config to create (%s) already exists", name)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				cause := errors.Unwrap(err)
				lines := strings.Split(cause.Error(), "\n")

				return weberror.NewWebError(cause, lines[0]).WithMetadata("validation", cause.Error(), true)
			}

			if errors.Is(err, store.ErrInvalidFormat) {
				cause := errors.Unwrap(err)
				return weberror.NewWebError(cause, "invalid formatting").WithMetadata("validation", cause.Error(), true)
			}

			if errors.Is(err, version.ErrInvalidKind) {
				return weberror.NewWebError(err, "unknown config kind provided")
			}

			return weberror.NewWebError(err, "unable to create new config %s", name)
		}
	}

	w.Header().Set("Location", strings.ToLower(fmt.Sprintf("/api/v1/configs/%s", name)))
	w.WriteHeader(http.StatusCreated)

	cfg.Spec = nil
	cfg.Status = nil

	body, err := json.Marshal(cfg)
	if err != nil {
		plog.Error("marshaling config", "config", cfg.FullName(), "err", err)
		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", cfg.FullName()),
		bt.NewResource("config", cfg.FullName(), "create"),
		body,
	)

	return nil
}

type workflow struct {
	Auto *struct {
		Create  string `mapstructure:"create"`
		Update  *bool  `mapstructure:"update"`
		Restart *bool  `mapstructure:"restart"`
	} `mapstructure:"auto"`

	Topology  string            `mapstructure:"topology"`
	Scenario  string            `mapstructure:"scenario"`
	VLANs     map[string]int    `mapstructure:"vlans"`
	Schedules map[string]string `mapstructue:"schedules"`

	VLANRange *struct {
		Min int `mapstructure:"min"`
		Max int `mapstructure:"max"`
	} `mapstructure:"vlanRange"`
}

func (this workflow) AutoUpdate() bool {
	if this.Auto == nil {
		return true
	}

	if this.Auto.Update == nil {
		return true
	}

	return *this.Auto.Update
}

func (this workflow) AutoRestart() bool {
	if this.Auto == nil {
		return true
	}

	if this.Auto.Restart == nil {
		return true
	}

	return *this.Auto.Restart
}

func (this workflow) ExperimentName() string {
	if this.Auto == nil {
		return ""
	}

	return this.Auto.Create
}

func (this workflow) ExperimentTopology() string {
	return this.Topology
}

func (this workflow) ExperimentScenario() string {
	return this.Scenario
}

func (this workflow) VLANMappings() map[string]int {
	if this.VLANs == nil {
		return make(map[string]int)
	}

	return this.VLANs
}

func (this workflow) ScheduleMappings() map[string]string {
	if this.Schedules == nil {
		return make(map[string]string)
	}

	return this.Schedules
}

func (this workflow) VLANMin() int {
	if this.VLANRange == nil {
		return 0
	}

	return this.VLANRange.Min
}

func (this workflow) VLANMax() int {
	if this.VLANRange == nil {
		return 0
	}

	return this.VLANRange.Max
}
