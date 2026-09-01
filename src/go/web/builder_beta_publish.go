package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/activeshadow/structs"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/vm"
	"phenix/store"
	"phenix/types"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	builderPublishActionCreate    = "create"
	builderPublishActionUpdate    = "update"
	builderPublishActionUse       = "use"
	builderPublishSucceeded       = "succeeded"
	builderPublishPartial         = "partial"
	builderPublishFailed          = "failed"
	builderPublishSkipped         = "skipped"
	builderPublishStageDocument   = "document"
	builderPublishStageTopology   = "topology"
	builderPublishStageScenario   = "scenario"
	builderPublishStageExperiment = "experiment"
	builderPublishStageDraft      = "draft"
)

// Config storage does not expose compare-and-swap. This lock prevents two
// publications in this process from passing the same preflight concurrently;
// multi-process deployments still rely on source digests and explicit actions.
var builderPublishLock sync.Mutex //nolint:gochecknoglobals // process-wide publication transaction boundary

type builderPublishTarget struct {
	Name           string `json:"name"`
	Action         string `json:"action"`
	ExpectedDigest string `json:"expectedDigest,omitempty"`
}

type builderPublishRequest struct {
	Mode       bapi.PublishMode      `json:"mode"`
	Topology   builderPublishTarget  `json:"topology"`
	Scenario   *builderPublishTarget `json:"scenario,omitempty"`
	Experiment *builderPublishTarget `json:"experiment,omitempty"`
}

type builderPublishStage struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Config  string `json:"config,omitempty"`
}

type builderPublishResponse struct {
	Status     string                `json:"status"`
	Stages     []builderPublishStage `json:"stages"`
	Warnings   []string              `json:"warnings"`
	Errors     []string              `json:"errors"`
	Topology   *builderPublishTarget `json:"topology,omitempty"`
	Scenario   *builderPublishTarget `json:"scenario,omitempty"`
	Experiment *builderPublishTarget `json:"experiment,omitempty"`
	Draft      builderDraftResponse  `json:"draft"`
}

type builderBetaPublishOps struct {
	createConfig        func(*store.Config) (*store.Config, error)
	updateConfig        func(string, *store.Config) error
	createExperiment    func(context.Context, string, string, string, map[string]int) error
	lockExperiment      func(string, string) error
	unlockExperiment    func(string)
	broadcastConfig     func(*store.Config, string) error
	broadcastExperiment func(string, string) error
}

func newBuilderBetaPublishOps() builderBetaPublishOps {
	return builderBetaPublishOps{
		createConfig: func(cfg *store.Config) (*store.Config, error) {
			return config.Create(config.CreateFromConfig(cfg), config.CreateWithValidation())
		},
		updateConfig: config.Update,
		createExperiment: func(
			ctx context.Context,
			name, topology, scenario string,
			aliases map[string]int,
		) error {
			return experiment.Create(
				ctx,
				experiment.CreateWithName(name),
				experiment.CreateWithTopology(topology),
				experiment.CreateWithScenario(scenario),
				experiment.CreateWithVLANAliases(aliases),
			)
		},
		lockExperiment: func(name, action string) error {
			if action == builderPublishActionCreate {
				return cache.LockExperimentForCreation(name)
			}

			return cache.LockExperimentForUpdate(name)
		},
		unlockExperiment:    cache.UnlockExperiment,
		broadcastConfig:     builderBetaBroadcastConfig,
		broadcastExperiment: builderBetaBroadcastExperiment,
	}
}

func builderBetaBroadcastConfig(cfg *store.Config, action string) error {
	summary := *cfg
	summary.Spec = nil
	summary.Status = nil

	body, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encoding config broadcast: %w", err)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", cfg.FullName()),
		bt.NewResource("config", cfg.FullName(), action),
		body,
	)

	return nil
}

func builderBetaBroadcastExperiment(name, action string) error {
	exp, err := experiment.Get(name)
	if err != nil {
		return fmt.Errorf("loading experiment for broadcast: %w", err)
	}

	vms, _ := vm.List(name)

	body, err := marshaler.Marshal(util.ExperimentToProtobuf(*exp, "", vms))
	if err != nil {
		return fmt.Errorf("encoding experiment broadcast: %w", err)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", name),
		bt.NewResource(builderBetaSourceExperiment, name, action),
		body,
	)

	return nil
}

// publishDraft is the only Builder Beta handler that mutates phenix configs.
// The request carries intent only; document bytes always come from the current,
// ETag-protected draft snapshot.
//
//nolint:funlen,maintidx // ordered publication stages and partial results are kept together
func (b *builderBetaAPI) publishDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderBetaPublishDraft")

	actor, ok := builderBetaRequestActor(r)
	if !ok {
		return builderBetaForbidden(actor, "publishing a builder draft")
	}

	ifMatch, err := builderBetaIfMatch(r)
	if err != nil {
		return err
	}

	var request builderPublishRequest
	if err := builderBetaDecode(w, r, &request); err != nil {
		return err
	}

	if err := validateBuilderPublishRequest(request); err != nil {
		return err
	}

	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	meta, err := b.draftFor(r, actor, builderBetaVerbUpdate, "publishing a builder draft")
	if err != nil {
		return err
	}

	if ifMatch != meta.ETag() {
		if builderPublishRetry(meta, request, ifMatch) {
			return b.writePublishRetry(w, meta, request)
		}

		return builderBetaCheckIfMatch(ifMatch, meta)
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderBetaWebError(err, "unable to load the current draft snapshot")
	}

	document, err := snapshot.Decode()
	if err != nil {
		return builderBetaWebError(err, "unable to decode the current draft snapshot")
	}

	topology, warnings, err := document.PublishTopologyConfig(request.Topology.Name)
	if err != nil {
		return weberror.NewWebError(err, "builder document cannot be published as topology %s", request.Topology.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	projection, err := document.ToTopology()
	if err != nil {
		return weberror.NewWebError(err, "builder document cannot be projected").
			SetStatus(http.StatusUnprocessableEntity)
	}

	plan, err := b.preflightPublish(actor, meta, snapshot, document, topology, projection, request)
	if err != nil {
		return err
	}

	response := builderPublishResponse{
		Status:     builderPublishSucceeded,
		Stages:     []builderPublishStage{},
		Warnings:   warnings,
		Errors:     []string{},
		Topology:   &request.Topology,
		Scenario:   request.Scenario,
		Experiment: request.Experiment,
		Draft:      newBuilderDraftResponse(meta),
	}

	published, err := b.drafts.PutPublishedDocument(r.Context(), bapi.PutPublishedDocumentRequest{
		Target:     request.Topology.Name,
		Kind:       builderBetaKindTopology,
		Actor:      actor.user,
		Document:   snapshot.Data,
		DraftID:    meta.ID,
		SnapshotID: snapshot.Manifest.ID,
	})
	if err != nil {
		return builderBetaWebError(err, "unable to store the published builder document")
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDocument, Status: "created", Message: "immutable builder document stored", Config: "",
	})

	reference, err := published.Reference().EncodeReference()
	if err != nil {
		return b.writePublishPartial(w, meta, &response, "document reference", err)
	}

	if topology.Metadata.Annotations == nil {
		topology.Metadata.Annotations = store.Annotations{}
	}

	topology.Metadata.Annotations[bapi.DocumentAnnotation] = reference

	topology, err = b.writePublishedConfig(plan.topology, topology)
	if err != nil {
		return b.writePublishPartial(w, meta, &response, builderPublishStageTopology, err)
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageTopology, Status: plan.topology.status(), Message: "", Config: topology.FullName(),
	})

	if !plan.topology.applied {
		if err := b.publish.broadcastConfig(topology, plan.topology.action); err != nil {
			response.Warnings = append(response.Warnings, "topology was stored but its live update could not be broadcast")
			plog.Error(plog.TypeSystem, "broadcasting published topology", "err", err)
		}
	}

	var scenarioName string
	if plan.scenario != nil {
		scenarioName = plan.scenario.config.Metadata.Name

		scenario, writeErr := b.writePublishedConfig(*plan.scenario, plan.scenario.config)
		if writeErr != nil {
			return b.writePublishPartial(w, meta, &response, builderPublishStageScenario, writeErr)
		}

		response.Stages = append(response.Stages, builderPublishStage{
			Name: builderPublishStageScenario, Status: plan.scenario.status(), Message: "", Config: scenario.FullName(),
		})

		if !plan.scenario.applied {
			if err := b.publish.broadcastConfig(scenario, plan.scenario.action); err != nil {
				response.Warnings = append(response.Warnings, "scenario was stored but its live update could not be broadcast")
				plog.Error(plog.TypeSystem, "broadcasting published scenario", "err", err)
			}
		}
	}

	if plan.experiment != nil {
		if err := b.writeExperiment(r.Context(), *plan.experiment, topology, projection.VLANAliases, scenarioName); err != nil {
			return b.writePublishPartial(w, meta, &response, builderPublishStageExperiment, err)
		}

		response.Stages = append(response.Stages, builderPublishStage{
			Name: builderPublishStageExperiment, Status: plan.experiment.status(), Message: "",
			Config: kindExperiment + "/" + plan.experiment.name,
		})

		if !plan.experiment.applied {
			if err := b.publish.broadcastExperiment(plan.experiment.name, plan.experiment.action); err != nil {
				response.Warnings = append(response.Warnings, "experiment was stored but its live update could not be broadcast")
				plog.Error(plog.TypeSystem, "broadcasting published experiment", "err", err)
			}
		}
	}

	updated, err := b.drafts.MarkPublished(r.Context(), bapi.MarkPublishedRequest{
		DraftID:          meta.ID,
		Actor:            actor.user,
		ExpectedRevision: meta.Revision,
		SnapshotID:       snapshot.Manifest.ID,
		Mode:             request.Mode,
		TopologyTarget:   request.Topology.Name,
		TopologyAction:   bapi.TopologyAction(request.Topology.Action),
		ExperimentTarget: targetName(request.Experiment),
		ScenarioTarget:   targetName(request.Scenario),
		DocumentID:       published.ID,
	})
	if err != nil {
		return b.writePublishPartial(w, meta, &response, "draft", err)
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDraft, Status: "ok", Message: "", Config: "",
	})
	response.Draft = newBuilderDraftResponse(updated)

	if _, err := b.drafts.DeleteSupersededDocuments(r.Context(), request.Topology.Name, published.ID); err != nil {
		response.Warnings = append(response.Warnings, "publication succeeded but superseded builder documents could not be removed")
		plog.Error(plog.TypeSystem, "cleaning superseded builder documents", "err", err)
	}

	plog.Info(
		plog.TypeAction,
		"published builder draft",
		"user", actor.user,
		"draft", meta.ID,
		"topology", request.Topology.Name,
	)

	return builderBetaWriteJSON(w, http.StatusOK, updated.ETag(), response)
}

type builderPublishConfigPlan struct {
	action   string
	existing *store.Config
	config   *store.Config
	applied  bool
}

func (p builderPublishConfigPlan) status() string {
	if p.applied {
		return builderPublishSkipped
	}

	return p.action + "d"
}

type builderPublishExperimentPlan struct {
	action   string
	name     string
	existing *store.Config
	applied  bool
}

func (p builderPublishExperimentPlan) status() string {
	if p.applied {
		return builderPublishSkipped
	}

	return p.action + "d"
}

type builderPublishPlan struct {
	topology   builderPublishConfigPlan
	scenario   *builderPublishConfigPlan
	experiment *builderPublishExperimentPlan
}

func (b *builderBetaAPI) preflightPublish(
	actor builderBetaActor,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
	topology *store.Config,
	projection *bdoc.Topology,
	request builderPublishRequest,
) (*builderPublishPlan, error) {
	if err := b.authorizePublishTargets(actor, request); err != nil {
		return nil, err
	}

	topologyPlan, err := b.preflightTopology(meta, snapshot, document, topology, request.Topology)
	if err != nil {
		return nil, err
	}

	plan := &builderPublishPlan{topology: topologyPlan, scenario: nil, experiment: nil}

	if request.Scenario != nil {
		scenario, scenarioErr := b.preflightScenario(document, request.Topology.Name, *request.Scenario)
		if scenarioErr != nil {
			return nil, scenarioErr
		}

		plan.scenario = scenario
	}

	if request.Experiment != nil {
		experimentPlan, experimentErr := b.preflightExperiment(
			meta,
			document,
			projection,
			request.Topology.Name,
			targetName(request.Scenario),
			plan.scenario,
			*request.Experiment,
		)
		if experimentErr != nil {
			return nil, experimentErr
		}

		plan.experiment = experimentPlan
	}

	resuming := plan.topology.applied &&
		(plan.scenario == nil || plan.scenario.applied) &&
		(plan.experiment == nil || plan.experiment.applied)
	if !resuming {
		if err := b.checkSourceFreshness(actor, meta, document); err != nil {
			return nil, err
		}
	}

	return plan, nil
}

func (b *builderBetaAPI) authorizePublishTargets(actor builderBetaActor, request builderPublishRequest) error {
	topologyVerb := builderBetaVerbUpdate
	if request.Topology.Action == builderPublishActionCreate {
		topologyVerb = builderBetaVerbCreate
	}
	if !builderBetaBaseAllowed(
		actor.role,
		topologyVerb,
		store.ConfigFullName(builderBetaKindTopology, request.Topology.Name),
	) {
		return builderBetaForbidden(actor, "publishing topology "+request.Topology.Name)
	}

	if request.Scenario != nil {
		scenarioVerb := builderBetaVerbUpdate
		if request.Scenario.Action == builderPublishActionCreate {
			scenarioVerb = builderBetaVerbCreate
		}
		if !builderBetaBaseAllowed(
			actor.role,
			scenarioVerb,
			store.ConfigFullName(builderBetaKindScenario, request.Scenario.Name),
		) {
			return builderBetaForbidden(actor, "publishing scenario "+request.Scenario.Name)
		}
	}

	if request.Experiment != nil &&
		(!builderBetaExperimentAllowed(actor.role, request.Experiment.Action, request.Experiment.Name) ||
			!builderBetaBaseAllowed(
				actor.role,
				builderBetaVerb(request.Experiment.Action),
				store.ConfigFullName(kindExperiment, request.Experiment.Name),
			)) {
		return builderBetaForbidden(actor, "publishing experiment config "+request.Experiment.Name)
	}

	return nil
}

func builderBetaExperimentAllowed(role rbac.Role, action, name string) bool {
	switch action {
	case builderPublishActionCreate:
		return role.Allowed("experiments", "create", name)
	case builderPublishActionUpdate:
		return role.Allowed("experiments", "update", name)
	}

	return false
}

func (b *builderBetaAPI) checkSourceFreshness(
	actor builderBetaActor,
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
) error {
	source := document.Source
	if source == nil || source.Kind == bdoc.SourceKindManual {
		return nil
	}

	if strings.HasPrefix(meta.SourceToken, "uploaded/") {
		return nil
	}

	var kind string
	switch source.Kind {
	case bdoc.SourceKindTopology:
		kind = builderBetaKindTopology
	case bdoc.SourceKindExperiment:
		kind = kindExperiment
	case bdoc.SourceKindManual:
		return nil
	default:
		return weberror.NewWebError(nil, "builder source kind %s cannot be published", source.Kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	fullName := store.ConfigFullName(kind, source.Name)
	if !builderBetaBaseAllowed(actor.role, builderBetaVerbGet, fullName) ||
		!builderBetaSourceGetAllowed(actor.role, kind, source.Name) {
		return builderBetaForbidden(actor, "checking builder source "+fullName)
	}

	current, err := b.getConfig(fullName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(nil, "builder source %s no longer exists", fullName).
				SetStatus(http.StatusConflict)
		}

		return weberror.NewWebError(err, "unable to reload builder source %s", fullName).
			SetStatus(http.StatusInternalServerError)
	}

	digest, err := bdoc.SourceDigest(*current)
	if err != nil {
		return weberror.NewWebError(err, "unable to digest builder source %s", fullName).
			SetStatus(http.StatusInternalServerError)
	}

	if digest != source.Digest {
		return weberror.NewWebError(nil, "builder source %s changed after this draft was generated", fullName).
			SetStatus(http.StatusConflict)
	}

	return nil
}

func builderBetaSourceGetAllowed(role rbac.Role, kind, name string) bool {
	switch kind {
	case builderBetaKindTopology:
		return role.Allowed("topologies", "get", name)
	case kindExperiment:
		return role.Allowed("experiments", "get", name)
	default:
		return false
	}
}

func (b *builderBetaAPI) preflightTopology(
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
	topology *store.Config,
	target builderPublishTarget,
) (builderPublishConfigPlan, error) {
	existing, exists, err := b.configIfExists(builderBetaKindTopology, target.Name)
	if err != nil {
		return builderPublishConfigPlan{}, err
	}

	if exists && existing.HasAnnotation(builderBetaXMLAnnotation) {
		return builderPublishConfigPlan{}, weberror.NewWebError(
			nil,
			"topology %s belongs to the legacy XML Builder and cannot be updated here",
			target.Name,
		).SetStatus(http.StatusConflict)
	}

	applied := existingBuilderDocumentMatches(existing, snapshot.Manifest.Digest)
	if err := requirePublishAction(target, exists, applied); err != nil {
		return builderPublishConfigPlan{}, err
	}

	if target.Action == builderPublishActionUpdate && !applied &&
		!topologyUpdateMatchesSource(meta, document, target.Name, existing) {
		return builderPublishConfigPlan{}, weberror.NewWebError(
			nil,
			"topology %s is not the source this draft was loaded from",
			target.Name,
		).SetStatus(http.StatusConflict)
	}

	if exists {
		topology.Metadata = existing.Metadata
		topology.Metadata.Annotations = maps.Clone(existing.Metadata.Annotations)
		topology.Status = existing.Status
	}

	return builderPublishConfigPlan{
		action: target.Action, existing: existing, config: topology, applied: applied,
	}, nil
}

func topologyUpdateMatchesSource(
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
	target string,
	existing *store.Config,
) bool {
	if strings.HasPrefix(meta.SourceToken, "uploaded/") {
		return false
	}

	if document.Source != nil {
		switch document.Source.Kind {
		case bdoc.SourceKindTopology:
			if document.Source.Name == target {
				return true
			}
		case bdoc.SourceKindExperiment:
			if document.Source.Topology == target {
				return true
			}
		case bdoc.SourceKindManual:
			return false
		}
	}

	const tokenPrefix = "builder-doc/"
	if !strings.HasPrefix(meta.SourceToken, tokenPrefix) || existing == nil {
		return false
	}

	value, ok := existing.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return false
	}

	ref, err := bapi.DecodeReference(value)

	return err == nil && ref.ID == strings.TrimPrefix(meta.SourceToken, tokenPrefix)
}

func existingBuilderDocumentMatches(existing *store.Config, digest string) bool {
	if existing == nil || existing.Metadata.Annotations == nil {
		return false
	}

	value, ok := existing.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return false
	}

	ref, err := bapi.DecodeReference(value)

	return err == nil && ref.Digest == digest
}

//nolint:funlen // ordered validation prevents any write before every scenario check passes
func (b *builderBetaAPI) preflightScenario(
	document *bdoc.Document,
	topologyName string,
	target builderPublishTarget,
) (*builderPublishConfigPlan, error) {
	ref := document.Scenario
	if ref == nil {
		return nil, weberror.NewWebError(nil, "publish intent names a scenario but the document does not").
			SetStatus(http.StatusUnprocessableEntity)
	}

	if ref.Kind == bdoc.ScenarioRefStored {
		if target.Action != builderPublishActionUse || target.Name != ref.Name {
			return nil, weberror.NewWebError(nil, "stored scenario must be published with action use and its original name").
				SetStatus(http.StatusUnprocessableEntity)
		}
	} else if target.Action == builderPublishActionUse {
		return nil, weberror.NewWebError(nil, "uploaded scenario requires an explicit create or update action").
			SetStatus(http.StatusUnprocessableEntity)
	}

	existing, exists, err := b.configIfExists(builderBetaKindScenario, target.Name)
	if err != nil {
		return nil, err
	}

	action := target.Action
	if action == builderPublishActionUse {
		action = builderPublishActionUpdate
	}

	if err := requirePublishAction(
		builderPublishTarget{Name: target.Name, Action: action, ExpectedDigest: target.ExpectedDigest},
		exists,
		false,
	); err != nil {
		return nil, err
	}

	var scenario *store.Config

	if ref.Kind == bdoc.ScenarioRefStored {
		digest, digestErr := bdoc.ContentDigest(existing.Spec)
		if digestErr != nil {
			return nil, weberror.NewWebError(digestErr, "unable to digest stored scenario %s", target.Name).
				SetStatus(http.StatusInternalServerError)
		}

		if existing.Version != ref.APIVersion || digest != ref.Digest {
			return nil, weberror.NewWebError(nil, "stored scenario %s changed after it was selected", target.Name).
				SetStatus(http.StatusConflict)
		}

		scenario = cloneBuilderConfig(existing)
	} else {
		if target.Action == builderPublishActionUpdate {
			digest, digestErr := bdoc.ContentDigest(existing.Spec)
			if digestErr != nil {
				return nil, weberror.NewWebError(digestErr, "unable to digest scenario %s", target.Name).
					SetStatus(http.StatusInternalServerError)
			}

			if target.ExpectedDigest == "" {
				return nil, weberror.NewWebError(
					nil,
					"updating uploaded scenario %s requires its expected digest",
					target.Name,
				).SetStatus(http.StatusBadRequest)
			}

			if target.ExpectedDigest != digest {
				return nil, weberror.NewWebError(
					nil,
					"scenario %s changed after publication was prepared",
					target.Name,
				).SetStatus(http.StatusConflict)
			}
		}

		scenario, err = store.NewConfig("Scenario/" + target.Name)
		if err != nil {
			return nil, invalidPublishTarget(builderBetaSourceScenario, target.Name)
		}

		scenario.Version = ref.APIVersion
		scenario.Spec = maps.Clone(ref.Content)

		if exists {
			scenario.Metadata = existing.Metadata
			scenario.Metadata.Annotations = maps.Clone(existing.Metadata.Annotations)
			scenario.Status = existing.Status
		}
	}

	if scenario.Metadata.Annotations == nil {
		scenario.Metadata.Annotations = store.Annotations{}
	}

	scenario.Metadata.Annotations["topology"] = addTopologyAnnotation(
		scenario.Metadata.Annotations["topology"],
		topologyName,
	)

	applied := false
	if exists {
		digest, digestErr := bdoc.ContentDigest(existing.Spec)
		applied = digestErr == nil &&
			digest == ref.Digest &&
			hasTopologyAnnotation(existing.Metadata.Annotations["topology"], topologyName)
	}

	if err := types.ValidateConfigSpec(*scenario); err != nil {
		return nil, weberror.NewWebError(err, "scenario %s is not valid", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	return &builderPublishConfigPlan{
		action: action, existing: existing, config: scenario, applied: applied,
	}, nil
}

func (b *builderBetaAPI) preflightExperiment(
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioPlan *builderPublishConfigPlan,
	target builderPublishTarget,
) (*builderPublishExperimentPlan, error) {
	existing, exists, err := b.configIfExists(kindExperiment, target.Name)
	if err != nil {
		return nil, err
	}

	applied := experimentAlreadyApplied(existing, projection, topologyName, scenarioName)
	if err := requirePublishAction(target, exists, applied); err != nil {
		return nil, err
	}

	plan := &builderPublishExperimentPlan{
		action: target.Action, name: target.Name, existing: existing, applied: applied,
	}

	if target.Action == builderPublishActionCreate || applied {
		return plan, nil
	}

	if !experimentUpdateMatchesSource(meta, document, target.Name) {
		return nil, weberror.NewWebError(
			nil,
			"experiment %s is not the source this draft was loaded from",
			target.Name,
		).SetStatus(http.StatusConflict)
	}

	current, err := types.DecodeExperimentFromConfig(*existing)
	if err != nil {
		return nil, weberror.NewWebError(err, "experiment %s cannot be decoded", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}
	if current.Running() {
		return nil, weberror.NewWebError(nil, "running experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusConflict)
	}

	var scenarioConfig *store.Config
	if scenarioPlan != nil {
		scenarioConfig = scenarioPlan.config
	}

	updated, err := updatedExperimentConfig(
		existing,
		projection,
		topologyName,
		scenarioName,
		document.Scenario,
		scenarioConfig,
	)
	if err != nil {
		return nil, weberror.NewWebError(err, "experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if err := types.ValidateConfigSpec(*updated); err != nil {
		return nil, weberror.NewWebError(err, "experiment %s is not valid", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	plan.existing = updated

	return plan, nil
}

func experimentUpdateMatchesSource(
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
	target string,
) bool {
	return !strings.HasPrefix(meta.SourceToken, "uploaded/") &&
		document.Source != nil &&
		document.Source.Kind == bdoc.SourceKindExperiment &&
		document.Source.Name == target
}

func updatedExperimentConfig(
	existing *store.Config,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioRef *bdoc.ScenarioRef,
	scenarioConfig *store.Config,
) (*store.Config, error) {
	exp, err := types.DecodeExperimentFromConfig(*existing)
	if err != nil {
		return nil, err
	}

	topologySpec, err := projection.SpecV1()
	if err != nil {
		return nil, err
	}

	exp.Spec.SetTopology(topologySpec)
	exp.Spec.VLANs().SetAliases(projection.VLANAliases)

	if scenarioRef == nil {
		exp.Spec.SetScenario(nil)
	} else {
		if scenarioConfig == nil {
			return nil, errors.New("scenario config was not prepared")
		}

		scenario, scenarioErr := types.MakeCustomScenarioFromConfig(*scenarioConfig, nil)
		if scenarioErr != nil {
			return nil, scenarioErr
		}

		if mergeErr := types.MergeScenariosForTopology(scenario, topologyName); mergeErr != nil {
			return nil, mergeErr
		}

		exp.Spec.SetScenario(scenario)
	}

	updated := cloneBuilderConfig(existing)
	updated.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)

	if updated.Metadata.Annotations == nil {
		updated.Metadata.Annotations = store.Annotations{}
	}

	updated.Metadata.Annotations["topology"] = topologyName
	if scenarioName == "" {
		delete(updated.Metadata.Annotations, "scenario")
	} else {
		updated.Metadata.Annotations["scenario"] = scenarioName
	}

	return updated, nil
}

func (b *builderBetaAPI) writePublishedConfig(
	plan builderPublishConfigPlan,
	cfg *store.Config,
) (*store.Config, error) {
	if plan.applied {
		return plan.existing, nil
	}

	if plan.action == builderPublishActionCreate {
		return b.publish.createConfig(cfg)
	}

	if err := b.publish.updateConfig(plan.existing.FullName(), cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (b *builderBetaAPI) writeExperiment(
	ctx context.Context,
	plan builderPublishExperimentPlan,
	topology *store.Config,
	aliases map[string]int,
	scenario string,
) error {
	if plan.applied {
		return nil
	}

	if err := b.publish.lockExperiment(plan.name, plan.action); err != nil {
		return err
	}
	defer b.publish.unlockExperiment(plan.name)

	if plan.action == builderPublishActionCreate {
		return b.publish.createExperiment(ctx, plan.name, topology.Metadata.Name, scenario, aliases)
	}

	return b.publish.updateConfig(plan.existing.FullName(), plan.existing)
}

func (b *builderBetaAPI) configIfExists(kind, name string) (*store.Config, bool, error) {
	fullName := store.ConfigFullName(kind, name)
	if fullName == "" || strings.TrimSpace(name) != name || name == "" {
		return nil, false, invalidPublishTarget(strings.ToLower(kind), name)
	}

	cfg, err := b.getConfig(fullName)
	if err == nil {
		return cfg, true, nil
	}

	if errors.Is(err, store.ErrNotExist) {
		return nil, false, nil
	}

	return nil, false, weberror.NewWebError(err, "unable to inspect config %s", fullName).
		SetStatus(http.StatusInternalServerError)
}

func requirePublishAction(target builderPublishTarget, exists, applied bool) error {
	switch {
	case applied:
		return nil
	case target.Action == builderPublishActionCreate && exists:
		return weberror.NewWebError(nil, "config %s already exists; choose update explicitly", target.Name).
			SetStatus(http.StatusConflict)
	case target.Action == builderPublishActionUpdate && !exists:
		return weberror.NewWebError(nil, "config %s does not exist; choose create explicitly", target.Name).
			SetStatus(http.StatusConflict)
	}

	return nil
}

func validateBuilderPublishRequest(request builderPublishRequest) error {
	if !request.Mode.Valid() {
		return weberror.NewWebError(nil, "unknown publish mode %q", request.Mode).
			SetStatus(http.StatusBadRequest)
	}

	if err := validatePublishTarget(builderBetaSourceTopology, request.Topology, false); err != nil {
		return err
	}

	switch request.Mode {
	case bapi.PublishModeTopology:
		if request.Experiment != nil || request.Scenario != nil {
			return weberror.NewWebError(nil, "topology-only publication cannot include scenario or experiment targets").
				SetStatus(http.StatusBadRequest)
		}
	case bapi.PublishModeTopologyExperiment:
		if request.Experiment == nil {
			return weberror.NewWebError(nil, "topology-experiment publication requires an experiment target").
				SetStatus(http.StatusBadRequest)
		}

		if err := validatePublishTarget(builderBetaSourceExperiment, *request.Experiment, false); err != nil {
			return err
		}

		if request.Scenario != nil {
			if err := validatePublishTarget(builderBetaSourceScenario, *request.Scenario, true); err != nil {
				return err
			}
		}
	}

	return nil
}

func validatePublishTarget(kind string, target builderPublishTarget, allowUse bool) error {
	if target.Name == "" || strings.TrimSpace(target.Name) != target.Name ||
		store.ConfigFullName(kind, target.Name) == "" {
		return invalidPublishTarget(kind, target.Name)
	}

	valid := target.Action == builderPublishActionCreate || target.Action == builderPublishActionUpdate
	if allowUse {
		valid = valid || target.Action == builderPublishActionUse
	}

	if !valid {
		return weberror.NewWebError(nil, "unknown %s publish action %q", kind, target.Action).
			SetStatus(http.StatusBadRequest)
	}

	if target.ExpectedDigest != "" &&
		(kind != builderBetaSourceScenario ||
			target.Action != builderPublishActionUpdate ||
			len(target.ExpectedDigest) != len("sha256:")+64 ||
			!strings.HasPrefix(target.ExpectedDigest, "sha256:") ||
			strings.Trim(strings.TrimPrefix(target.ExpectedDigest, "sha256:"), "0123456789abcdef") != "") {
		return weberror.NewWebError(nil, "%s target has an invalid expected digest", kind).
			SetStatus(http.StatusBadRequest)
	}

	return nil
}

func invalidPublishTarget(kind, name string) error {
	return weberror.NewWebError(nil, "%s target %q is not a valid config name", kind, name).
		SetStatus(http.StatusBadRequest)
}

func addTopologyAnnotation(value, topology string) string {
	seen := make(map[string]bool)
	names := make([]string, 0)

	for name := range strings.SplitSeq(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}

		seen[name] = true
		names = append(names, name)
	}

	if !seen[topology] {
		names = append(names, topology)
	}

	return strings.Join(names, ",")
}

func hasTopologyAnnotation(value, topology string) bool {
	for name := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(name) == topology {
			return true
		}
	}

	return false
}

func experimentAlreadyApplied(
	existing *store.Config,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
) bool {
	if existing == nil ||
		existing.Metadata.Annotations["topology"] != topologyName ||
		existing.Metadata.Annotations["scenario"] != scenarioName {
		return false
	}

	exp, err := types.DecodeExperimentFromConfig(*existing)
	if err != nil {
		return false
	}

	topologySpec, err := projection.SpecV1()
	if err != nil {
		return false
	}

	return reflect.DeepEqual(exp.Spec.Topology(), topologySpec) &&
		maps.Equal(exp.Spec.VLANs().Aliases(), projection.VLANAliases)
}

func cloneBuilderConfig(cfg *store.Config) *store.Config {
	clone := *cfg
	clone.Metadata.Annotations = maps.Clone(cfg.Metadata.Annotations)
	clone.Spec = maps.Clone(cfg.Spec)
	clone.Status = maps.Clone(cfg.Status)

	return &clone
}

func targetName(target *builderPublishTarget) string {
	if target == nil {
		return ""
	}

	return target.Name
}

func builderPublishRetry(
	meta *bapi.DraftMetadata,
	request builderPublishRequest,
	ifMatch string,
) bool {
	state := meta.Publication
	current := meta.Current()

	return state != nil &&
		current != nil &&
		ifMatch == fmt.Sprintf(`"%d"`, state.Revision) &&
		state.Mode == request.Mode &&
		state.TopologyTarget == request.Topology.Name &&
		state.TopologyAction == bapi.TopologyAction(request.Topology.Action) &&
		state.ExperimentTarget == targetName(request.Experiment) &&
		state.ScenarioTarget == targetName(request.Scenario) &&
		state.SnapshotID == current.ID &&
		state.Digest == current.Digest
}

func (b *builderBetaAPI) writePublishRetry(
	w http.ResponseWriter,
	meta *bapi.DraftMetadata,
	request builderPublishRequest,
) error {
	stages := []builderPublishStage{
		{Name: builderPublishStageDocument, Status: builderPublishSkipped, Message: "", Config: ""},
		{
			Name: builderPublishStageTopology, Status: builderPublishSkipped, Message: "",
			Config: builderBetaKindTopology + "/" + request.Topology.Name,
		},
	}

	if request.Scenario != nil {
		stages = append(stages, builderPublishStage{
			Name: builderPublishStageScenario, Status: builderPublishSkipped, Message: "",
			Config: builderBetaKindScenario + "/" + request.Scenario.Name,
		})
	}

	if request.Experiment != nil {
		stages = append(stages, builderPublishStage{
			Name: builderPublishStageExperiment, Status: builderPublishSkipped, Message: "",
			Config: kindExperiment + "/" + request.Experiment.Name,
		})
	}

	stages = append(stages, builderPublishStage{
		Name: builderPublishStageDraft, Status: builderPublishSkipped, Message: "", Config: "",
	})

	return builderBetaWriteJSON(w, http.StatusOK, meta.ETag(), builderPublishResponse{
		Status:     builderPublishSucceeded,
		Stages:     stages,
		Warnings:   []string{"identical publication was already complete"},
		Errors:     []string{},
		Topology:   &request.Topology,
		Scenario:   request.Scenario,
		Experiment: request.Experiment,
		Draft:      newBuilderDraftResponse(meta),
	})
}

func (b *builderBetaAPI) writePublishPartial(
	w http.ResponseWriter,
	meta *bapi.DraftMetadata,
	response *builderPublishResponse,
	stage string,
	cause error,
) error {
	message := stage + " publication failed"

	response.Status = builderPublishPartial
	response.Errors = append(response.Errors, message)
	response.Stages = append(response.Stages, builderPublishStage{
		Name: stage, Status: builderPublishFailed, Message: message, Config: "",
	})
	response.Draft = newBuilderDraftResponse(meta)

	plog.Error(
		plog.TypeSystem,
		"builder publication stage failed",
		"stage", stage,
		"draft", meta.ID,
		"err", cause,
	)

	status := http.StatusInternalServerError
	if errors.Is(cause, store.ErrExist) || errors.Is(cause, store.ErrNotExist) ||
		errors.Is(cause, bapi.ErrConflict) {
		status = http.StatusConflict
	}

	return builderBetaWriteJSON(w, status, meta.ETag(), response)
}
