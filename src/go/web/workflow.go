package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/api/experiment"
	"phenix/api/workflow"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// ApplyWorkflow - POST /workflow/apply/{branch}.
//
// It validates the workflow config, plans what applying it to the branch does
// and prepares the plan, all before anything changes; refuses with 409 when
// the expect query parameter names another action; and carries out the plan
// unless dryRun is true. Either way it responds with the plan as JSON. A dry
// run may name, in repeated pending query parameters, the refs its config dry
// runs returned for configs the client upserts before the real apply.
func ApplyWorkflow(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "ApplyWorkflow")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		branch  = mux.Vars(r)["branch"]
		q       = r.URL.Query()
	)

	if !role.Allowed("workflow", "create") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"applying phenix workflow not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"applying phenix workflow is not allowed for user %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	// Repeated tag query parameters become the experiment's workflow tags.
	tags := strings.Join(q["tag"], ",")

	doc, src, err := readWorkflowConfig(r, branch)
	if err != nil {
		return err
	}

	spec, err := decodeWorkflowConfig(doc, src)
	if err != nil {
		return err
	}

	dryRun, err := parseDryRun(q)
	if err != nil {
		return err
	}

	expect := q.Get("expect")
	if _, err := workflow.ParseAction(expect); expect != "" && err != nil {
		return weberror.NewWebError(err, "invalid expect value %q", expect)
	}

	// A real apply runs after the client's upsert, so it looks every config up.
	pending := q["pending"]
	if len(pending) > 0 && !dryRun {
		return weberror.NewWebError(nil, "pending is only allowed with dryRun=true")
	}

	plan, target, err := planWorkflow(spec, branch)
	if err != nil {
		return err
	}

	prep, err := workflow.Prepare(spec, plan, target, pending...)
	if err != nil {
		return workflowWebError(err, "unable to prepare phenix workflow config")
	}

	if err := workflow.CheckExpected(plan, expect); err != nil {
		return workflowWebError(err, "unable to apply phenix workflow config")
	}

	if !dryRun {
		if err := executeWorkflowPlan(ctx, plan, spec, branch, tags, prep, target); err != nil {
			return err
		}
	}

	// A Result holds only strings and a bool, so marshaling it cannot fail.
	body, _ := json.Marshal(workflow.Result{Plan: plan, DryRun: dryRun})

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}

// readWorkflowConfig returns the request body parsed with its ${VAR}
// references expanded, and the body itself, which explains schema errors. It
// parses into a map rather than a store config so the schema sees every
// top-level key the body has.
func readWorkflowConfig(r *http.Request, branch string) (map[string]any, []byte, error) {
	var (
		typ = r.Header.Get("Content-Type")
		doc map[string]any
		src []byte
		err error
	)

	// ${BRANCH_NAME} expands to the branch, as in NewConfigFromYAML.
	_ = os.Setenv("BRANCH_NAME", branch)

	switch typ {
	case mimeJSON: // default to JSON if not set
		src, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to read request data")

			return nil, nil, err.SetStatus(http.StatusInternalServerError)
		}

		err = json.Unmarshal([]byte(common.ParseEnv(string(src))), &doc)
	case mimeYAML:
		src, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return nil, nil, err.SetStatus(http.StatusInternalServerError)
		}

		err = yaml.Unmarshal([]byte(common.ParseEnv(string(src))), &doc)
	default:
		return nil, nil, weberror.NewWebError(
			nil,
			"must use application/json or application/x-yaml when providing phenix workflow config",
		)
	}

	if err != nil {
		cause := fmt.Errorf("%w: %w", store.ErrInvalidFormat, err)

		return nil, nil, weberror.NewWebError(cause, "unable to parse phenix workflow config")
	}

	return doc, src, nil
}

// decodeWorkflowConfig checks doc against the Workflow schema, then decodes
// and validates its spec. src, the body doc was parsed from, explains schema
// errors.
func decodeWorkflowConfig(doc map[string]any, src []byte) (workflow.Spec, error) {
	if err := workflow.ValidateDocument(doc); err != nil {
		if errors.Is(err, types.ErrValidationFailed) {
			return workflow.Spec{}, validationWebError(src, err)
		}

		err := weberror.NewWebError(err, "unable to validate phenix workflow config")

		return workflow.Spec{}, err.SetStatus(http.StatusInternalServerError)
	}

	// The schema requires spec, as a mapping.
	raw, _ := doc["spec"].(map[string]any)

	spec, err := workflow.Decode(raw)
	if err != nil {
		return workflow.Spec{}, weberror.NewWebError(err, "unable to parse phenix workflow config")
	}

	if err := spec.Validate(); err != nil {
		return workflow.Spec{}, weberror.NewWebError(err, "invalid phenix workflow config")
	}

	return spec, nil
}

// planWorkflow plans applying spec to branch from the stored experiments and
// checks the plan's default bridge against them. It also returns the
// experiment mapped to branch, or nil when none is.
func planWorkflow(spec workflow.Spec, branch string) (workflow.Plan, *types.Experiment, error) {
	experiments, err := experiment.List()
	if err != nil {
		err := weberror.NewWebError(err, "unable to get list of experiments")

		return workflow.Plan{}, nil, err.SetStatus(http.StatusInternalServerError)
	}

	existing := make([]string, 0, len(experiments))
	for _, exp := range experiments {
		existing = append(existing, exp.Metadata.Name)
	}

	mapped := workflow.Mapped(experiments, branch)

	plan, err := workflow.NewPlan(spec, branch, mapped, existing)
	if err != nil {
		return workflow.Plan{}, nil, workflowWebError(err, "unable to apply phenix workflow config")
	}

	if err := workflow.CheckBridge(spec, plan, experiments, common.BridgeMode); err != nil {
		return workflow.Plan{}, nil, workflowWebError(err, "unable to prepare phenix workflow config")
	}

	var target *types.Experiment
	if len(mapped) == 1 {
		target = &mapped[0]
	}

	return plan, target, nil
}

// workflowWebError wraps a workflow API error in a web error: 409 for a
// conflict or a changed plan, 400 for an invalid config or an unresolved
// reference, and 500 for anything else.
func workflowWebError(err error, message string) *weberror.WebError {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, workflow.ErrConflict), errors.Is(err, workflow.ErrPlanChanged):
		status = http.StatusConflict
	case errors.Is(err, workflow.ErrInvalidSpec), errors.Is(err, workflow.ErrUnresolved):
		status = http.StatusBadRequest
	}

	return weberror.NewWebError(err, "%s", message).SetStatus(status)
}

// executeWorkflowPlan carries out plan with what prep resolved. exp is the
// experiment mapped to branch, which the update actions act on.
func executeWorkflowPlan(
	ctx context.Context,
	plan workflow.Plan,
	spec workflow.Spec,
	branch, tags string,
	prep workflow.Prepared,
	exp *types.Experiment,
) error {
	switch plan.Action {
	case workflow.ActionNone:
		// Nothing to carry out; the response gives the reason.
	case workflow.ActionCreate, workflow.ActionCreateAndStart:
		return createFromWorkflow(ctx, spec, branch, tags, prep, plan.Action == workflow.ActionCreateAndStart)
	case workflow.ActionUpdate, workflow.ActionUpdateAndStart, workflow.ActionRestart:
		return updateFromWorkflow(exp, spec, tags, prep, plan.Action)
	}

	return nil
}

// createFromWorkflow creates the experiment auto.create names from what prep
// resolved, maps it to branch, tags it and starts it when start is true.
func createFromWorkflow(
	ctx context.Context,
	spec workflow.Spec,
	branch, tags string,
	prep workflow.Prepared,
	start bool,
) error {
	expName := spec.ExperimentName()

	err := cache.LockExperimentForCreation(expName)
	if err != nil {
		err := weberror.NewWebError(err, "unable to create new experiment")

		return err.SetStatus(http.StatusInternalServerError)
	}

	defer cache.UnlockExperiment(expName)

	annotations := map[string]string{workflow.BranchAnnotation: branch}

	if tags != "" {
		annotations[workflow.TagsAnnotation] = tags
	}

	opts := []experiment.CreateOption{
		experiment.CreateWithName(expName),
		experiment.CreateWithAnnotations(annotations),
		experiment.CreateWithTopology(prep.TopologyName),
		experiment.CreateWithScenario(prep.ScenarioName),
		experiment.CreateWithVLANAliases(spec.VLANMappings()),
		experiment.CreateWithSchedules(spec.Schedules),
		experiment.CreateWithVLANMin(spec.VLANMin()),
		experiment.CreateWithVLANMax(spec.VLANMax()),
		experiment.CreateWithDeployMode(spec.ExperimentDeployMode()),
		experiment.CreateWithDefaultBridge(spec.DefaultBridgeName()),
		experiment.CreateWithGREMesh(spec.UseGREMesh),
	}

	err = experiment.Create(ctx, opts...)
	if err != nil {
		err := weberror.NewWebError(err, "unable to create new experiment")

		return err.SetStatus(http.StatusInternalServerError)
	}

	if start {
		cache.UnlockExperiment(expName)

		if _, err := startExperiment(expName); err != nil {
			return err
		}
	}

	return nil
}

// updateFromWorkflow applies the workflow to exp using only what prep
// resolved, so nothing is looked up after a running experiment is stopped. A
// restart stops the experiment first; a restart or an updateAndStart starts
// it afterward.
func updateFromWorkflow(
	exp *types.Experiment,
	spec workflow.Spec,
	tags string,
	prep workflow.Prepared,
	action workflow.Action,
) error {
	expName := exp.Metadata.Name

	if action == workflow.ActionRestart {
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

	exp.Spec.SetTopology(prep.Topology)
	exp.Metadata.Annotations["topology"] = prep.TopologyName

	if prep.ScenarioName != "" {
		exp.Spec.SetScenario(prep.Scenario)
		exp.Metadata.Annotations["scenario"] = prep.ScenarioName
	}

	// default is to not override existing tags if no new tags are passed
	// TODO: perhaps sorting tags and only updating those that are passed
	// while leaving old tags that have not been overridden
	if tags != "" {
		exp.Metadata.Annotations[workflow.TagsAnnotation] = tags
	}

	exp.Spec.VLANs().SetAliases(prep.Aliases)
	exp.Spec.SetDefaultBridge(spec.DefaultBridgeName())
	exp.Spec.SetSchedule(prep.Schedules)
	exp.Spec.SetDeployMode(string(spec.ExperimentDeployMode()))
	_ = exp.Spec.SetVLANRange(spec.VLANMin(), spec.VLANMax(), true)
	exp.Spec.SetUseGREMesh(spec.UseGREMesh)

	if err := exp.WriteToStore(false); err != nil {
		err := weberror.NewWebError(err, "unable to write updated experiment %s", expName)

		return err.SetStatus(http.StatusInternalServerError)
	}

	if err := experiment.Reconfigure(expName); err != nil {
		return weberror.NewWebError(err, "unable to reconfigure updated experiment %s", expName)
	}

	if action == workflow.ActionRestart || action == workflow.ActionUpdateAndStart {
		cache.UnlockExperiment(expName)

		if _, err := startExperiment(expName); err != nil {
			return err
		}
	}

	return nil
}

// WorkflowUpsertConfig - POST /workflow/configs/{branch}.
//
// It creates or updates a config, with ${BRANCH_NAME} in the body expanded to
// the branch. With dryRun=true it runs every check but stores nothing, runs
// no config hook and responds 200 with what the upsert would do.
//
//nolint:funlen // handler
func WorkflowUpsertConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "WorkflowUpsertConfig")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		scope   = vars["branch"]
	)

	// Parsing substitutes ${NAME} references with the server's environment
	// variables, and a parse error quotes the text it failed on, so only a
	// caller who may create or update configs gets as far as parsing. The
	// checks for the named config below still apply.
	if !role.Allowed("configs", "create") && !role.Allowed("configs", "update") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"creating or updating config not allowed",
			"user",
			user,
		)

		err := weberror.NewWebError(
			nil,
			"creating or updating configs not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	dryRun, err := parseDryRun(r.URL.Query())
	if err != nil {
		return err
	}

	var (
		typ = r.Header.Get("Content-Type")
		cfg *store.Config
		src []byte
	)

	// set branch name in environment variable so it can be used in
	// NewConfigFromJSON and NewConfigFromYAML
	_ = os.Setenv("BRANCH_NAME", scope)

	switch typ {
	case mimeJSON: // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to read request data")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		cfg, err = store.NewConfigFromJSON(body)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse JSON config")
		}
	case mimeYAML:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		cfg, err = store.NewConfigFromYAML(body)
		if err != nil {
			return weberror.NewWebError(err, "unable to parse YAML config")
		}
	default:
		return weberror.NewWebError(
			nil,
			"must use application/json or application/x-yaml when providing topology/scenario config",
		)
	}

	var (
		name   = fmt.Sprintf("%s/%s", cfg.Kind, cfg.Metadata.Name)
		exists = true
	)

	// NewConfig rejects a kind the store does not hold, such as Workflow, and
	// a name containing a slash; its nil result would make store.Get panic.
	tester, nameErr := store.NewConfig(name)
	if nameErr != nil {
		return weberror.NewWebError(nameErr, "invalid config %s", name)
	}

	if err := store.Get(tester); err != nil {
		if !errors.Is(err, store.ErrNotExist) {
			err := weberror.NewWebError(err, "checking store for config")

			return err.SetStatus(http.StatusInternalServerError)
		}

		exists = false
	}

	if exists {
		if !role.Allowed("configs", "update", name) {
			user, _ := ctx.Value(middleware.ContextKeyUser).(string)
			plog.Warn(
				plog.TypeSecurity,
				"updating config not allowed",
				"user",
				user,
			)

			err := weberror.NewWebError(
				nil,
				"updating config %s not allowed for %s",
				name,
				user,
			)

			return err.SetStatus(http.StatusForbidden)
		}

		err = updateOrValidate(name, cfg, dryRun)
		if err != nil {
			if errors.Is(err, store.ErrNotExist) {
				return weberror.NewWebError(err, "config to update (%s) does not exist", name)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				return validationWebError(src, err)
			}

			if errors.Is(err, store.ErrInvalidFormat) {
				cause := errors.Unwrap(err)

				return weberror.NewWebError(cause, "invalid formatting").
					WithMetadata("validation", cause.Error(), true)
			}

			return weberror.NewWebError(err, "unable to update config %s", name)
		}
	} else {
		if !role.Allowed("configs", "create", name) {
			return configForbidden(ctx, "creating", name)
		}

		cfg, err = createOrValidate(cfg, dryRun)
		if err != nil {
			if errors.Is(err, store.ErrExist) {
				return weberror.NewWebError(err, "config to create (%s) already exists", name)
			}

			if errors.Is(err, types.ErrValidationFailed) {
				return validationWebError(src, err)
			}

			if errors.Is(err, store.ErrInvalidFormat) {
				cause := errors.Unwrap(err)

				return weberror.NewWebError(cause, "invalid formatting").
					WithMetadata("validation", cause.Error(), true)
			}

			if errors.Is(err, version.ErrInvalidKind) {
				return weberror.NewWebError(err, "unknown config kind provided")
			}

			return weberror.NewWebError(err, "unable to create new config %s", name)
		}
	}

	if dryRun {
		writeConfigDryRun(w, exists, cfg)

		return nil
	}

	w.Header().Set("Location", strings.ToLower("/api/v1/configs/"+name))
	w.WriteHeader(http.StatusCreated)

	cfg.Spec = nil
	cfg.Status = nil

	body, err := json.Marshal(cfg)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", cfg.FullName(), "err", err)

		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", cfg.FullName()),
		bt.NewResource("config", cfg.FullName(), "create"),
		body,
	)

	return nil
}
