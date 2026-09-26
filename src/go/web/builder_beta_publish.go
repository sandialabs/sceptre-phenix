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
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/common"
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

// errBuilderExperimentRunning refuses to update an experiment found running
// once its lock is held, after the stages before it were written.
var errBuilderExperimentRunning = errors.New("a running experiment cannot be updated")

// builderExperimentAnnotation is the experiment config annotation recording
// the draft, and the published document, that last published the experiment,
// and the experiment's digest (see [bdoc.SourceDigest]) once its apps'
// configure stage ran. A draft updates an experiment it published only while
// the experiment still has that digest: nothing else has changed it since.
const builderExperimentAnnotation = "builder-experiment"

// builderExperimentPublication is the value of [builderExperimentAnnotation].
type builderExperimentPublication struct {
	DraftID    string `json:"draftId"`
	DocumentID string `json:"documentId"`
	Digest     string `json:"digest"`
}

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
	Name    string             `json:"name"`
	Status  bapi.PublishStatus `json:"status"`
	Message string             `json:"message,omitempty"`
	Config  string             `json:"config,omitempty"`
}

type builderPublishResponse struct {
	Status     bapi.PublishStatus    `json:"status"`
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
	// decodeTopology merges a topology config's includeTopologies, as phenix
	// does when it creates an experiment.
	decodeTopology func(store.Config) (ifaces.TopologySpec, error)
	// reconfigureExperiment runs the apps' configure stage on an updated
	// experiment; creating one runs it already.
	reconfigureExperiment func(string) error
	// annotateConfig stores a config whose annotations alone changed,
	// without running its config hooks again.
	annotateConfig func(*store.Config) error
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
		decodeTopology:      types.DecodeTopologyFromConfig,
		// As the configs API, the workflow API and the CLI do after an update.
		reconfigureExperiment: experiment.Reconfigure,
		annotateConfig:        store.Update,
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
		return publishProjectionRefusal(request.Topology.Name, err)
	}

	projection, err := document.ToTopology()
	if err != nil {
		return weberror.NewWebError(err, "builder document cannot be projected").
			SetStatus(http.StatusUnprocessableEntity)
	}

	plan, err := b.preflightPublish(r.Context(), actor, meta, snapshot, document, topology, projection, request)
	if err != nil {
		return err
	}

	response := builderPublishResponse{
		Status:     bapi.PublishSucceeded,
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

	switch {
	case published != nil && errors.Is(err, bapi.ErrCleanup):
		// The document is stored, repairing a damaged copy of it, but content
		// it replaces was not removed: publishing goes on, as every other
		// mutation does, and startup cleanup removes that content later.
		builderBetaWarnCleanup(w, err, "publish", actor.user)
		response.Warnings = append(response.Warnings,
			"the builder document was stored, but content it replaces could not be removed")
	case err != nil:
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
		publication := builderExperimentPublication{DraftID: meta.ID, DocumentID: published.ID, Digest: ""}

		if err := b.publishExperimentStage(
			r.Context(), *plan.experiment, topology, projection.VLANAliases, scenarioName, publication, &response,
		); err != nil {
			return b.writePublishPartial(w, meta, &response, builderPublishStageExperiment, err)
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

// publishExperimentStage writes a publication's experiment stage (see
// [builderBetaAPI.writeExperiment]) and adds it to the response, with a
// warning for anything that did not complete after the experiment was
// stored. It returns the error of a stage that failed.
func (b *builderBetaAPI) publishExperimentStage(
	ctx context.Context,
	plan builderPublishExperimentPlan,
	topology *store.Config,
	aliases map[string]int,
	scenario string,
	publication builderExperimentPublication,
	response *builderPublishResponse,
) error {
	recorded, err := b.writeExperiment(ctx, plan, topology, aliases, scenario, publication)
	if err != nil {
		return err
	}

	if !recorded {
		response.Warnings = append(response.Warnings,
			"experiment was stored, but not which draft published it, so this draft cannot update it again")
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageExperiment, Status: plan.status(), Message: "",
		Config: kindExperiment + "/" + plan.name,
	})

	if !plan.applied {
		if err := b.publish.broadcastExperiment(plan.name, plan.action); err != nil {
			response.Warnings = append(response.Warnings, "experiment was stored but its live update could not be broadcast")
			plog.Error(plog.TypeSystem, "broadcasting published experiment", "err", err)
		}
	}

	return nil
}

type builderPublishConfigPlan struct {
	action   string
	existing *store.Config
	config   *store.Config
	applied  bool
}

func (p builderPublishConfigPlan) status() bapi.PublishStatus {
	if p.applied {
		return bapi.PublishSkipped
	}

	return bapi.PublishStatus(p.action + "d")
}

type builderPublishExperimentPlan struct {
	action  string
	name    string
	applied bool
	// rebuild returns the updated config an update writes, from the
	// experiment config as it is when it is written.
	rebuild func(*store.Config) (*store.Config, error)
}

func (p builderPublishExperimentPlan) status() bapi.PublishStatus {
	if p.applied {
		return bapi.PublishSkipped
	}

	return bapi.PublishStatus(p.action + "d")
}

type builderPublishPlan struct {
	topology   builderPublishConfigPlan
	scenario   *builderPublishConfigPlan
	experiment *builderPublishExperimentPlan
}

func (b *builderBetaAPI) preflightPublish(
	ctx context.Context,
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

	// The topologies the published topology includes, read the way import
	// reads them: from the config store, and only when the caller may.
	includes, err := bdoc.CheckIncludes(request.Topology.Name, projection.Spec, b.includedTopologyLoader(actor))
	if err != nil {
		return nil, weberror.NewWebError(err, "builder document cannot be projected").
			SetStatus(http.StatusUnprocessableEntity)
	}

	topologyPlan, err := b.preflightTopology(ctx, meta, snapshot, document, topology, request.Topology)
	if err != nil {
		return nil, err
	}

	// Before the experiment checks, which would report the same clash as a
	// failure to merge the includes into the experiment.
	if err := includeClashRefusal(request.Topology.Name, includes); err != nil {
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
		if request.Experiment.Action == builderPublishActionUpdate {
			if err := mergedIncludesRefusal(actor, request.Experiment.Name, includes); err != nil {
				return nil, err
			}
		}

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
		if err := b.checkSourceFreshness(ctx, actor, meta, snapshot, document); err != nil {
			return nil, err
		}
	}

	return plan, nil
}

// publishProjectionRefusal refuses a document whose topology projection
// cannot be published. Interfaces without a VLAN are named in the message,
// which is what clients show, and not only in its cause; past the first few,
// only their number is.
func publishProjectionRefusal(topologyName string, err error) error {
	const listed = 3

	var vlanErr *bdoc.InterfaceVLANError
	if !errors.As(err, &vlanErr) || len(vlanErr.Problems) == 0 {
		return weberror.NewWebError(err, "builder document cannot be published as topology %s", topologyName).
			SetStatus(http.StatusUnprocessableEntity)
	}

	problems := strings.Join(vlanErr.Problems[:min(len(vlanErr.Problems), listed)], "; ")

	switch more := len(vlanErr.Problems) - listed; {
	case more == 1:
		problems += "; 1 more interface has no VLAN"
	case more > 1:
		problems += fmt.Sprintf("; %d more interfaces have no VLAN", more)
	}

	return weberror.NewWebError(err, "topology %s cannot be published: %s", topologyName, problems).
		SetStatus(http.StatusUnprocessableEntity)
}

// mergedIncludesRefusal is why an experiment update may not merge the
// topologies the published topology includes. The update merges them itself
// (see experimentTopology), so it reads them only the way import does: from
// the config store, and only when the caller may read them. An experiment
// create leaves the merge to phenix, as it is outside the Builder.
func mergedIncludesRefusal(actor builderBetaActor, experimentName string, includes bdoc.IncludeReport) error {
	for _, problem := range includes.Unreadable {
		if errors.Is(problem.Err, errBuilderIncludeForbidden) {
			return builderBetaForbidden(
				actor,
				fmt.Sprintf("merging included topology %s into experiment %s", problem.Name, experimentName),
			)
		}
	}

	if len(includes.Unreadable) > 0 {
		problem := includes.Unreadable[0]

		return weberror.NewWebError(
			nil,
			"experiment %s cannot be updated: included topology %s cannot be merged: %v",
			experimentName, problem.Name, problem.Err,
		).SetStatus(http.StatusUnprocessableEntity)
	}

	return nil
}

// includeClashRefusal refuses a topology whose included topologies define a
// hostname it defines too, which phenix refuses to merge into an experiment.
// Import reports such a clash, but an included topology can gain the node
// after the draft was imported. The refusal names the topology first, so the
// Publish dialog shows it on the topology name field.
func includeClashRefusal(topologyName string, includes bdoc.IncludeReport) error {
	const listed = 3

	switch clashes := includes.Clashes; {
	case len(clashes) == 0:
		return nil
	case len(clashes) == 1:
		return weberror.NewWebError(
			nil,
			"topology %s cannot be published: node %s is defined both here and in its included topology %s; "+
				"phenix rejects duplicate hostnames, so rename the node here or in %s",
			topologyName, clashes[0].Hostname, clashes[0].Include, clashes[0].Include,
		).SetStatus(http.StatusConflict)
	default:
		nodes := make([]string, 0, listed+1)

		for _, clash := range clashes[:min(len(clashes), listed)] {
			nodes = append(nodes, fmt.Sprintf("%s (in %s)", clash.Hostname, clash.Include))
		}

		if len(clashes) > listed {
			nodes = append(nodes, fmt.Sprintf("%d more", len(clashes)-listed))
		}

		return weberror.NewWebError(
			nil,
			"topology %s cannot be published: nodes %s and %s are defined both here and in its included topologies; "+
				"phenix rejects duplicate hostnames, so rename them here or in those topologies",
			topologyName, strings.Join(nodes[:len(nodes)-1], ", "), nodes[len(nodes)-1],
		).SetStatus(http.StatusConflict)
	}
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

// checkSourceFreshness refuses a draft whose source config changed after the
// draft was imported. Publishing to the source changes it too, so a source
// that holds this draft's own publication, or the one of the published
// document it was opened from, is fresh while nothing else has changed it
// since (see [builderBetaAPI.sourceHoldsDraftPublication]).
func (b *builderBetaAPI) checkSourceFreshness(
	ctx context.Context,
	actor builderBetaActor,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
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

	if digest == source.Digest {
		return nil
	}

	published, err := b.sourceHoldsDraftPublication(ctx, meta, snapshot, current)
	if err != nil {
		return err
	}

	if !published {
		return weberror.NewWebError(nil, "builder source %s changed after this draft was imported", fullName).
			SetStatus(http.StatusConflict)
	}

	return nil
}

// sourceHoldsDraftPublication reports whether a source config changed only by
// publishing this draft, or the published document it was opened from: a
// topology that still holds exactly that document's projection, or an
// experiment that still has the digest recorded when it was published (see
// [experimentHoldsDraftPublication]). The apps' configure stage rewrites an
// experiment's spec as it is published, so an experiment cannot be compared
// with the document itself.
func (b *builderBetaAPI) sourceHoldsDraftPublication(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	source *store.Config,
) (bool, error) {
	if source.Kind == kindExperiment {
		_, unchanged, err := experimentHoldsDraftPublication(meta, source)

		return unchanged, err
	}

	_, unchanged, err := b.holdsDraftDocument(ctx, meta, snapshot, source)

	return unchanged, err
}

// experimentHoldsDraftPublication reports whether an experiment records (see
// [builderExperimentAnnotation]) that this draft published it last, or the
// published document the draft was opened from or last published did, and if
// so, whether it still has the digest recorded then: nothing else has changed
// it since, the configs API, another draft or the experiment's own start
// included.
func experimentHoldsDraftPublication(meta *bapi.DraftMetadata, exp *store.Config) (bool, bool, error) {
	value, ok := exp.Metadata.Annotations[builderExperimentAnnotation]
	if !ok {
		return false, false, nil
	}

	var record builderExperimentPublication
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return false, false, nil //nolint:nilerr // an unreadable record vouches for nothing
	}

	owned := record.DraftID == meta.ID || draftOwnsDocument(meta, record.DocumentID)
	if !owned {
		return false, false, nil
	}

	digest, err := bdoc.SourceDigest(*exp)
	if err != nil {
		return true, false, weberror.NewWebError(err, "unable to digest experiment %s", exp.Metadata.Name).
			SetStatus(http.StatusInternalServerError)
	}

	return true, digest == record.Digest, nil
}

// draftDocumentReference returns the builder document reference a topology
// config carries, and whether that document is this draft's own: one the
// draft published (the published record names the draft, or the draft
// recorded it as its last publication), the published document the draft was
// opened from, or one holding exactly the content the draft publishes now.
func draftDocumentReference(
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	topology *store.Config,
) (bapi.DocumentReference, bool) {
	if topology == nil {
		return bapi.DocumentReference{}, false
	}

	value, ok := topology.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return bapi.DocumentReference{}, false
	}

	ref, err := bapi.DecodeReference(value)
	if err != nil {
		return bapi.DocumentReference{}, false
	}

	owned := ref.DraftID == meta.ID ||
		ref.Digest == snapshot.Manifest.Digest ||
		draftOwnsDocument(meta, ref.ID)

	return ref, owned
}

// draftOwnsDocument reports whether a published document is one of this
// draft's own: the one it was opened from, the one it last published, or the
// one the draft it forks had last published when it was forked.
func draftOwnsDocument(meta *bapi.DraftMetadata, id string) bool {
	return id != "" &&
		(id == openedDocumentID(meta) ||
			(meta.Publication != nil && id == meta.Publication.DocumentID) ||
			(meta.Forked != nil && id == meta.Forked.DocumentID))
}

// builderDocTokenPrefix starts the source token of a draft opened from a
// published document: "builder-doc/<document id>".
const builderDocTokenPrefix = "builder-doc/"

// openedDocumentID returns the ID of the published document a draft was
// opened from, from its "builder-doc/<document id>" source token, or "".
func openedDocumentID(meta *bapi.DraftMetadata) string {
	id, ok := strings.CutPrefix(meta.SourceToken, builderDocTokenPrefix)
	if !ok {
		return ""
	}

	return id
}

// forkOrigin returns the source token and the forked publication of a new
// draft that forks the draft named "<owner>/<draft id>", as saving an
// editor's history as a new draft does: that draft's own source token, and
// its last publication (or else what it had forked). The fork may then
// update what that draft published or was opened from, as long as nothing
// else has changed it since (see [draftDocumentReference] and
// [experimentHoldsDraftPublication]), but not what that draft publishes
// later. Its source token is not the published document's, so opening that
// published diagram does not open the fork as the user's draft of it. Only a
// caller who may read that draft, its owner or one holding "builder-drafts"
// "get" for it, gets its identity; for anyone else it is a draft that does
// not exist (see [builderBetaAPI.draftFor]).
func (b *builderBetaAPI) forkOrigin(
	r *http.Request,
	actor builderBetaActor,
	forkOf string,
) (string, *bapi.ForkedPublication, error) {
	owner, draftID, _ := strings.Cut(forkOf, "/")

	meta, err := b.namedDraft(r, actor, builderBetaVerbGet, "forking a builder draft", owner, draftID)
	if err != nil {
		return "", nil, err
	}

	forked := meta.Forked

	if meta.Publication != nil && meta.Publication.DocumentID != "" {
		forked = &bapi.ForkedPublication{
			DocumentID:       meta.Publication.DocumentID,
			TopologyTarget:   meta.Publication.TopologyTarget,
			ExperimentTarget: meta.Publication.ExperimentTarget,
		}
	}

	return meta.SourceToken, forked, nil
}

// holdsDraftDocument reports whether a topology config holds a document of
// this draft (see [draftDocumentReference]) and, if so, whether its spec is
// still exactly that document's projection: nothing has changed the topology
// since the document was published to it. A document that can no longer be
// read or projected counts as changed.
func (b *builderBetaAPI) holdsDraftDocument(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	topology *store.Config,
) (bool, bool, error) {
	ref, owned := draftDocumentReference(meta, snapshot, topology)
	if !owned {
		return false, false, nil
	}

	data, err := b.drafts.VerifyPublishedDocument(ctx, ref)
	if err != nil {
		if errors.Is(err, bapi.ErrNotFound) || errors.Is(err, bapi.ErrCorrupt) || errors.Is(err, bapi.ErrInvalid) {
			return true, false, nil
		}

		return true, false, builderBetaWebError(err, "unable to read the builder document of topology %s", topology.Metadata.Name)
	}

	document, err := bdoc.Decode(data)
	if err != nil {
		return true, false, nil //nolint:nilerr // an undecodable document cannot vouch for the topology
	}

	published, _, err := document.ToTopologyConfig(topology.Metadata.Name)
	if err != nil {
		return true, false, nil //nolint:nilerr // nor can one that no longer projects
	}

	want, err := bdoc.SourceDigest(*published)
	if err != nil {
		return true, false, nil //nolint:nilerr // nor can one that cannot be digested
	}

	got, err := bdoc.SourceDigest(*topology)
	if err != nil {
		return true, false, weberror.NewWebError(err, "unable to digest topology %s", topology.Metadata.Name).
			SetStatus(http.StatusInternalServerError)
	}

	return true, got == want, nil
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
	ctx context.Context,
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

	if target.Action == builderPublishActionUpdate && !applied {
		if err := b.topologyUpdateRefusal(ctx, meta, snapshot, document, existing); err != nil {
			return builderPublishConfigPlan{}, err
		}
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

// topologyUpdateRefusal refuses an update of an existing topology this draft
// may not update. A draft updates a topology that holds one of its own
// documents (see [draftDocumentReference]), which it published or was opened
// from, as long as nothing else has changed the topology since: that is how a
// draft publishes again after further edits, whatever it was loaded from.
// Otherwise it updates only the topology it was loaded from: the one it was
// imported from, or the one its source experiment was built from, whose
// freshness checkSourceFreshness checks.
func (b *builderBetaAPI) topologyUpdateRefusal(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
	existing *store.Config,
) error {
	name := existing.Metadata.Name

	owned, unchanged, err := b.holdsDraftDocument(ctx, meta, snapshot, existing)

	switch {
	case err != nil:
		return err
	case owned && unchanged:
		return nil
	case owned:
		return weberror.NewWebError(nil, "topology %s changed after this draft published it", name).
			SetStatus(http.StatusConflict)
	case topologyUpdateMatchesSource(meta, document, name):
		return nil
	}

	return weberror.NewWebError(nil, "topology %s is not the source this draft was loaded from", name).
		SetStatus(http.StatusConflict)
}

// topologyUpdateMatchesSource reports whether the document was imported from
// the topology, or from an experiment built from it. An upload names no
// stored config, so it never matches.
func topologyUpdateMatchesSource(meta *bapi.DraftMetadata, document *bdoc.Document, target string) bool {
	if strings.HasPrefix(meta.SourceToken, "uploaded/") || document.Source == nil {
		return false
	}

	switch document.Source.Kind {
	case bdoc.SourceKindTopology:
		return document.Source.Name == target
	case bdoc.SourceKindExperiment:
		return document.Source.Topology == target
	case bdoc.SourceKindManual:
	}

	return false
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

	// Needed to update the experiment, and to tell whether it already was.
	topologySpec, topologyErr := b.experimentTopology(projection, topologyName)
	applied := topologyErr == nil &&
		experimentAlreadyApplied(existing, topologySpec, projection, topologyName, scenarioName)
	if err := requirePublishAction(target, exists, applied); err != nil {
		return nil, err
	}

	plan := &builderPublishExperimentPlan{
		action: target.Action, name: target.Name, applied: applied, rebuild: nil,
	}

	if applied {
		return plan, nil
	}

	if target.Action == builderPublishActionCreate {
		if err := experimentCreateRefusal(target.Name, projection); err != nil {
			return nil, err
		}

		return plan, nil
	}

	if err := experimentUpdateRefusal(meta, document, existing); err != nil {
		return nil, err
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

	if topologyErr != nil {
		return nil, weberror.NewWebError(topologyErr, "experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	var scenarioConfig *store.Config
	if scenarioPlan != nil {
		scenarioConfig = scenarioPlan.config
	}

	plan.rebuild = func(current *store.Config) (*store.Config, error) {
		return updatedExperimentConfig(
			current,
			topologySpec,
			projection,
			topologyName,
			scenarioName,
			document.Scenario,
			scenarioConfig,
		)
	}

	updated, err := plan.rebuild(existing)
	if err != nil {
		return nil, weberror.NewWebError(err, "experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if err := types.ValidateConfigSpec(*updated); err != nil {
		return nil, weberror.NewWebError(err, "experiment %s is not valid", target.Name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	return plan, nil
}

// experimentCreateRefusal refuses, before anything is written, an experiment
// that experiment.Create would refuse only after the document, topology and
// scenario are: the reserved name "all", a name longer than a bridge name
// when phenix names each experiment's bridge after it (auto bridge mode), or
// a config the Experiment schema refuses. validatePublishTarget has already
// checked the name against the config naming rule.
func experimentCreateRefusal(name string, projection *bdoc.Topology) error {
	// experiment.Create's limit, the longest Linux interface name.
	const maxBridgeName = 15

	if strings.EqualFold(name, "all") {
		return weberror.NewWebError(nil, "experiment %s is reserved: phenix uses the name to mean every experiment", name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	if common.BridgeMode == common.BridgeModeAuto && len(name) > maxBridgeName {
		return weberror.NewWebError(
			nil,
			"experiment %s has a name longer than %d characters, and this server names each experiment's bridge after it",
			name, maxBridgeName,
		).SetStatus(http.StatusUnprocessableEntity)
	}

	created, err := store.NewConfig(kindExperiment + "/" + name)
	if err != nil {
		return invalidPublishTarget(builderBetaSourceExperiment, name)
	}

	created.Version = store.APIGroup + "/" + version.StoredVersion[kindExperiment]
	created.Spec = map[string]any{
		"experimentName": name,
		"topology":       projection.Spec,
		"vlans":          map[string]any{"aliases": projection.VLANAliases},
	}

	if err := types.ValidateConfigSpec(*created); err != nil {
		return weberror.NewWebError(err, "experiment %s is not valid", name).
			SetStatus(http.StatusUnprocessableEntity)
	}

	return nil
}

// experimentUpdateRefusal refuses an update of an existing experiment this
// draft may not update. A draft updates an experiment it published, or that
// the published document it was opened from published, as long as nothing
// else has changed the experiment since (see
// [experimentHoldsDraftPublication]). Otherwise it updates only the
// experiment it was imported from, whose freshness checkSourceFreshness
// checks.
func experimentUpdateRefusal(meta *bapi.DraftMetadata, document *bdoc.Document, existing *store.Config) error {
	name := existing.Metadata.Name

	owned, unchanged, err := experimentHoldsDraftPublication(meta, existing)

	switch {
	case err != nil:
		return err
	case owned && unchanged:
		return nil
	case experimentUpdateMatchesSource(meta, document, name):
		return nil
	case owned:
		return weberror.NewWebError(nil, "experiment %s changed after this draft published it", name).
			SetStatus(http.StatusConflict)
	}

	return weberror.NewWebError(nil, "experiment %s is not the source this draft was loaded from", name).
		SetStatus(http.StatusConflict)
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

// experimentTopology returns the topology an experiment holds for the
// projection. The projection leaves the devices of included topologies out
// and names those topologies instead (see [bdoc.Document.ToTopology]); phenix
// merges them into an experiment when it creates one, so an update merges
// them the same way, once mergedIncludesRefusal has checked it may.
func (b *builderBetaAPI) experimentTopology( //nolint:ireturn // phenix decodes topologies to the interface
	projection *bdoc.Topology,
	topologyName string,
) (ifaces.TopologySpec, error) {
	spec, err := projection.SpecV1()
	if err != nil {
		return nil, err
	}

	if len(spec.IncludeTopologiesF) == 0 {
		return spec, nil
	}

	config, err := store.NewConfig(builderBetaKindTopology + "/" + topologyName)
	if err != nil {
		return nil, fmt.Errorf("creating topology config: %w", err)
	}

	config.Version = bdoc.TopologyAPIVersion
	config.Spec = projection.Spec

	return b.publish.decodeTopology(*config)
}

func updatedExperimentConfig(
	existing *store.Config,
	topologySpec ifaces.TopologySpec,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioRef *bdoc.ScenarioRef,
	scenarioConfig *store.Config,
) (*store.Config, error) {
	exp, err := types.DecodeExperimentFromConfig(*existing)
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

// writeExperiment creates or updates the experiment, holding its lock, and
// then records on it which draft and document published it (see
// [builderExperimentAnnotation]). It reports whether that record was stored:
// a failure to store it does not undo the publication, but leaves the draft
// unable to update the experiment again.
func (b *builderBetaAPI) writeExperiment(
	ctx context.Context,
	plan builderPublishExperimentPlan,
	topology *store.Config,
	aliases map[string]int,
	scenario string,
	publication builderExperimentPublication,
) (bool, error) {
	if plan.applied {
		return true, nil
	}

	if err := b.publish.lockExperiment(plan.name, plan.action); err != nil {
		return false, err
	}
	defer b.publish.unlockExperiment(plan.name)

	if plan.action == builderPublishActionCreate {
		if err := b.publish.createExperiment(ctx, plan.name, topology.Metadata.Name, scenario, aliases); err != nil {
			return false, err
		}

		return b.recordExperimentPublication(plan.name, publication), nil
	}

	// Preflight read the experiment before the lock, and it may have been
	// started or changed since, so it is read again and the update rebuilt
	// from it: a stale status or spec is never written back.
	current, err := b.getConfig(store.ConfigFullName(kindExperiment, plan.name))
	if err != nil {
		return false, fmt.Errorf("reloading experiment %s: %w", plan.name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*current)
	if err != nil {
		return false, fmt.Errorf("decoding experiment %s: %w", plan.name, err)
	}

	if exp.Running() {
		return false, fmt.Errorf("experiment %s: %w", plan.name, errBuilderExperimentRunning)
	}

	updated, err := plan.rebuild(current)
	if err != nil {
		return false, fmt.Errorf("rebuilding experiment %s: %w", plan.name, err)
	}

	if err := b.publish.updateConfig(current.FullName(), updated); err != nil {
		return false, err
	}

	// As every other update of an experiment spec does (the configs API, the
	// workflow API and the CLI), the apps' configure stage runs on the new
	// spec. If it fails, the experiment is put back as it was, so the failed
	// stage wrote nothing and publishing again retries all of it.
	if err := b.publish.reconfigureExperiment(plan.name); err != nil {
		b.restoreExperiment(current)

		return false, fmt.Errorf("configuring experiment %s: %w", plan.name, err)
	}

	return b.recordExperimentPublication(plan.name, publication), nil
}

// restoreExperiment puts an experiment back as it was before an update whose
// configure stage failed. The experiment lock does not stop the CLI from
// starting it meanwhile, which is also why the configure stage can fail, so
// it is read again first: a running experiment is left as it is, and one that
// is not keeps its current status.
func (b *builderBetaAPI) restoreExperiment(previous *store.Config) {
	name := previous.Metadata.Name

	restoreErr := func() error {
		current, err := b.getConfig(previous.FullName())
		if err != nil {
			return fmt.Errorf("reloading experiment: %w", err)
		}

		exp, err := types.DecodeExperimentFromConfig(*current)
		if err != nil {
			return fmt.Errorf("decoding experiment: %w", err)
		}

		if exp.Running() {
			return errBuilderExperimentRunning
		}

		restored := cloneBuilderConfig(previous)
		restored.Status = current.Status

		return b.publish.updateConfig(previous.FullName(), restored)
	}()
	if restoreErr != nil {
		plog.Error(
			plog.TypeSystem,
			"restoring experiment after its configure stage failed",
			"experiment", name,
			"err", restoreErr,
		)
	}
}

// recordExperimentPublication stores on an experiment, just published and
// still locked, which draft and document published it and the digest it has
// now that its configure stage ran (see [builderExperimentAnnotation]). It
// reports whether the record was stored, and logs why when it was not.
func (b *builderBetaAPI) recordExperimentPublication(name string, publication builderExperimentPublication) bool {
	err := func() error {
		current, err := b.getConfig(store.ConfigFullName(kindExperiment, name))
		if err != nil {
			return fmt.Errorf("reloading experiment: %w", err)
		}

		publication.Digest, err = bdoc.SourceDigest(*current)
		if err != nil {
			return fmt.Errorf("digesting experiment: %w", err)
		}

		value, err := json.Marshal(publication)
		if err != nil {
			return fmt.Errorf("encoding the publication record: %w", err)
		}

		annotated := cloneBuilderConfig(current)
		if annotated.Metadata.Annotations == nil {
			annotated.Metadata.Annotations = store.Annotations{}
		}

		annotated.Metadata.Annotations[builderExperimentAnnotation] = string(value)

		return b.publish.annotateConfig(annotated)
	}()
	if err != nil {
		plog.Error(
			plog.TypeSystem,
			"recording which builder draft published an experiment",
			"experiment", name,
			"draft", publication.DraftID,
			"err", err,
		)

		return false
	}

	return true
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
	if target.Name == "" || !config.NameRegex.MatchString(target.Name) ||
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
	topologySpec ifaces.TopologySpec,
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
		{Name: builderPublishStageDocument, Status: bapi.PublishSkipped, Message: "", Config: ""},
		{
			Name: builderPublishStageTopology, Status: bapi.PublishSkipped, Message: "",
			Config: builderBetaKindTopology + "/" + request.Topology.Name,
		},
	}

	if request.Scenario != nil {
		stages = append(stages, builderPublishStage{
			Name: builderPublishStageScenario, Status: bapi.PublishSkipped, Message: "",
			Config: builderBetaKindScenario + "/" + request.Scenario.Name,
		})
	}

	if request.Experiment != nil {
		stages = append(stages, builderPublishStage{
			Name: builderPublishStageExperiment, Status: bapi.PublishSkipped, Message: "",
			Config: kindExperiment + "/" + request.Experiment.Name,
		})
	}

	stages = append(stages, builderPublishStage{
		Name: builderPublishStageDraft, Status: bapi.PublishSkipped, Message: "", Config: "",
	})

	return builderBetaWriteJSON(w, http.StatusOK, meta.ETag(), builderPublishResponse{
		Status:     bapi.PublishSucceeded,
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
	if errors.Is(cause, errBuilderExperimentRunning) {
		message += ": " + errBuilderExperimentRunning.Error()
	}

	response.Status = bapi.PublishPartial
	response.Errors = append(response.Errors, message)
	response.Stages = append(response.Stages, builderPublishStage{
		Name: stage, Status: bapi.PublishFailed, Message: message, Config: "",
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
		errors.Is(cause, bapi.ErrConflict) || errors.Is(cause, errBuilderExperimentRunning) {
		status = http.StatusConflict
	}

	return builderBetaWriteJSON(w, status, meta.ETag(), response)
}
