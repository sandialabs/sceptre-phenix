package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
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
	builderPublishStageDocument   = "document"
	builderPublishStageTopology   = "topology"
	builderPublishStageScenario   = "scenario"
	builderPublishStageExperiment = "experiment"
	builderPublishStageDraft      = "draft"
)

// Config storage does not expose compare-and-swap. This lock prevents two
// publications in this process from passing the same preflight concurrently.
// Deployments with more than one process still rely on source digests and
// explicit actions.
var builderPublishLock sync.Mutex //nolint:gochecknoglobals // process-wide publication transaction boundary

// lockBuilderPublishing holds [builderPublishLock] while a caller outside
// Builder deletes or renames a Topology config. It returns the function that
// releases the lock. A delete or a rename removes the published documents of
// the topology, except those that a publication in flight may have just
// stored (see bapi.Service.DeleteConfigDocuments). Only the time of a
// document tells them apart. While the lock is held, no publication is in
// flight in this process. name is the full name of the config. For a config
// of another kind, it does nothing.
func lockBuilderPublishing(name string) func() {
	if kind, _, _ := strings.Cut(name, "/"); kind != builderKindTopology {
		return func() {}
	}

	builderPublishLock.Lock()

	return builderPublishLock.Unlock
}

// errBuilderExperimentRunning refuses to update an experiment that is
// running when the publication gets its lock. At that time, the stages
// before it are already written.
var errBuilderExperimentRunning = errors.New("a running experiment cannot be updated")

// builderExperimentAnnotation is the experiment config annotation that
// records the draft and the published document that last published the
// experiment. It also records the digest of the experiment (see
// [bdoc.SourceDigest]) after the configure stage of its apps ran. A draft
// updates an experiment it published only while the experiment still has
// that digest, that is, while nothing else changed it.
const builderExperimentAnnotation = "builder-experiment"

// builderExperimentPublication is the value of [builderExperimentAnnotation].
type builderExperimentPublication struct {
	DraftID    string `json:"draftId"`
	DocumentID string `json:"documentId"`
	Digest     string `json:"digest"`
}

type builderPublishTarget struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// builderPublishScenario names the scenario a topology-and-experiment
// publication gives its experiment: one of the Scenario configs the draft's
// document lists. Publishing never creates or replaces a scenario. It adds
// the topology to the "topology" annotation of each one the document lists.
type builderPublishScenario struct {
	Name string `json:"name"`
}

type builderPublishRequest struct {
	Mode     bapi.PublishMode     `json:"mode"`
	Topology builderPublishTarget `json:"topology"`
	// Scenario is the scenario of the experiment. A nil Scenario publishes the
	// experiment without one.
	Scenario   *builderPublishScenario `json:"scenario,omitempty"`
	Experiment *builderPublishTarget   `json:"experiment,omitempty"`
	// DryRun makes every check a publication makes, writes nothing, and
	// answers with what the publication would change (see
	// [builderAPI.previewPublish]).
	DryRun bool `json:"dryRun,omitempty"`
}

// scenarioName is the name of the experiment's scenario, or "" for none.
func (r builderPublishRequest) scenarioName() string {
	if r.Scenario == nil {
		return ""
	}

	return r.Scenario.Name
}

type builderPublishStage struct {
	Name    string             `json:"name"`
	Status  bapi.PublishStatus `json:"status"`
	Message string             `json:"message,omitempty"`
	Config  string             `json:"config,omitempty"`
}

// builderPublishResponse is what a publication did. Warnings and Errors are
// issues, each with its code (see [bdoc.Issue]). Warnings tell the caller
// what to know about a publication that completed. Errors tell why a stage
// of a partial publication failed.
type builderPublishResponse struct {
	Status     bapi.PublishStatus      `json:"status"`
	Stages     []builderPublishStage   `json:"stages"`
	Warnings   []bdoc.Issue            `json:"warnings"`
	Errors     []bdoc.Issue            `json:"errors"`
	Topology   *builderPublishTarget   `json:"topology,omitempty"`
	Scenario   *builderPublishScenario `json:"scenario,omitempty"`
	Experiment *builderPublishTarget   `json:"experiment,omitempty"`
	Draft      builderDraftResponse    `json:"draft"`
}

type builderPublishOps struct {
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
	// reconfigureExperiment runs the configure stage of the apps on an updated
	// experiment. The create of an experiment runs it already.
	reconfigureExperiment func(string) error
	// annotateConfig stores a config whose annotations alone changed,
	// without running its config hooks again.
	annotateConfig func(*store.Config) error
	// deleteConfig deletes a published topology as DELETE /configs deletes
	// a config.
	deleteConfig func(string) error
}

func newBuilderPublishOps() builderPublishOps {
	return builderPublishOps{
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
		broadcastConfig:     builderBroadcastConfig,
		broadcastExperiment: builderBroadcastExperiment,
		decodeTopology:      types.DecodeTopologyFromConfig,
		// As the configs API, the workflow API and the CLI do after an update.
		reconfigureExperiment: experiment.Reconfigure,
		annotateConfig:        store.Update,
		deleteConfig:          deleteConfig,
	}
}

// builderBroadcastConfig broadcasts a published config as CreateConfig
// and UpdateConfig do.
func builderBroadcastConfig(cfg *store.Config, action string) error {
	return broadcastConfig(cfg, cfg.FullName(), action)
}

// builderBroadcastExperiment broadcasts a published experiment as the
// experiments API does.
func builderBroadcastExperiment(name, action string) error {
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
		bt.NewResource(builderSourceExperiment, name, action),
		body,
	)

	return nil
}

// publishDraft is the only Builder handler that creates or updates phenix
// configs. deleteDocument deletes a published topology. The request carries
// only the intent. The document bytes always come from the current,
// ETag-protected draft snapshot.
//
//nolint:funlen // ordered publication stages and partial results are kept together
func (b *builderAPI) publishDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPublishDraft")

	actor, ok := builderRequestActor(r)
	if !ok {
		return builderForbidden(actor, "publishing a builder draft")
	}

	request, ifMatch, err := builderPublishIntent(w, r)
	if err != nil {
		return err
	}

	// A dry run writes nothing, so it needs no entity tag: it reports on the
	// draft as it is saved now.
	if request.DryRun {
		return b.previewPublish(w, r, actor, request)
	}

	if err := validateBuilderPublishRequest(request); err != nil {
		return err
	}

	builderPublishLock.Lock()
	defer builderPublishLock.Unlock()

	meta, err := b.draftFor(r, actor, builderVerbUpdate, "publishing a builder draft")
	if err != nil {
		return err
	}

	if ifMatch != meta.ETag() {
		if builderPublishRetry(meta, request, ifMatch) {
			return b.writePublishRetry(r.Context(), w, actor, meta, request)
		}

		return builderCheckIfMatch(ifMatch, meta)
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderWebError(err, "unable to load the current draft snapshot")
	}

	document, err := snapshot.Decode()
	if err != nil {
		return builderWebError(err, "unable to decode the current draft snapshot")
	}

	topology, projection, warnings, err := document.PublishTopology(request.Topology.Name)
	if err != nil {
		return publishProjectionRefusal(request.Topology.Name, err)
	}

	plan, err := b.preflightPublish(r.Context(), actor, meta, snapshot, document, topology, projection, request)
	if err != nil {
		return err
	}

	if warnings == nil {
		warnings = []bdoc.Issue{}
	}

	response := builderPublishResponse{
		Status:     bapi.PublishSucceeded,
		Stages:     []builderPublishStage{},
		Warnings:   warnings,
		Errors:     []bdoc.Issue{},
		Topology:   &request.Topology,
		Scenario:   request.Scenario,
		Experiment: request.Experiment,
		Draft:      newBuilderDraftResponse(meta),
	}

	published, err := b.drafts.PutPublishedDocument(r.Context(), bapi.PutPublishedDocumentRequest{
		Target:     request.Topology.Name,
		Kind:       builderKindTopology,
		Actor:      actor.user,
		Document:   snapshot.Data,
		DraftID:    meta.ID,
		SnapshotID: snapshot.Manifest.ID,
	})

	switch {
	case published != nil && errors.Is(err, bapi.ErrCleanup):
		// The document is stored (this repairs a damaged copy of it), but the
		// content it replaces was not removed. Publishing continues, as every
		// other mutation does. Startup cleanup removes that content later.
		builderWarnCleanup(w, err, "publish", actor.user)
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishCleanupFailed, "",
			"the builder document was stored, but content it replaces could not be removed"))
	case err != nil:
		return builderWebError(err, "unable to store the published builder document")
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDocument, Status: "created", Message: "immutable builder document stored", Config: "",
	})

	documentReference := publishedTopologyReference(published, plan.topology.existing)
	if documentReference.Path != "" {
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishFileUnchanged, "",
			builderFileNotWrittenWarning(request.Topology.Name, documentReference.Path)))
	}

	reference, err := documentReference.EncodeReference()
	if err != nil {
		return b.writePublishPartial(w, actor, meta, &response, "document reference", err)
	}

	if topology.Metadata.Annotations == nil {
		topology.Metadata.Annotations = store.Annotations{}
	}

	topology.Metadata.Annotations[bapi.DocumentAnnotation] = reference

	// A topology that this stage writes is a Builder topology from then on.
	// The write removes the diagram that the legacy Builder kept on it.
	if !plan.topology.applied {
		if warning, replaced := bapi.ReplaceLegacyDiagram(topology); replaced {
			response.Warnings = append(response.Warnings, warning)
		}
	}

	topology, err = b.publishConfigStage(builderPublishStageTopology, plan.topology, topology, &response)
	if err != nil {
		return b.writePublishPartial(w, actor, meta, &response, builderPublishStageTopology, err)
	}

	if plan.scenarios != nil {
		if err := b.publishScenarioStage(request.Topology.Name, *plan.scenarios, &response); err != nil {
			return b.writePublishPartial(w, actor, meta, &response, builderPublishStageScenario, err)
		}
	}

	if plan.experiment != nil {
		publication := builderExperimentPublication{DraftID: meta.ID, DocumentID: published.ID, Digest: ""}

		if err := b.publishExperimentStage(
			r.Context(), *plan.experiment, topology, projection.VLANAliases, request.scenarioName(), publication, &response,
		); err != nil {
			return b.writePublishPartial(w, actor, meta, &response, builderPublishStageExperiment, err)
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
		ScenarioTarget:   request.scenarioName(),
		DocumentID:       published.ID,
	})
	if err != nil {
		return b.writePublishPartial(w, actor, meta, &response, "draft", err)
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDraft, Status: "ok", Message: "", Config: "",
	})
	response.Draft = b.draftResponse(actor, updated)

	if _, err := b.drafts.DeleteSupersededDocuments(r.Context(), request.Topology.Name, published.ID); err != nil {
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishCleanupFailed, "",
			"publication succeeded but superseded builder documents could not be removed"))
		plog.Error(plog.TypeSystem, "cleaning superseded builder documents", "err", err)
	}

	plog.Info(
		plog.TypeAction,
		"published builder draft",
		"user", actor.user,
		"draft", meta.ID,
		"topology", request.Topology.Name,
	)

	return builderWriteJSON(w, http.StatusOK, updated.ETag(), response)
}

// publishExperimentStage writes the experiment stage of a publication (see
// [builderAPI.writeExperiment]) and adds it to the response. It adds a
// warning for anything that did not complete after the experiment was
// stored. It returns the error of a stage that failed.
func (b *builderAPI) publishExperimentStage(
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
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishExperimentUnrecorded, "",
			"experiment was stored, but not which draft published it, so this draft cannot update it again"))
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageExperiment, Status: plan.status(), Message: "",
		Config: kindExperiment + "/" + plan.name,
	})

	if !plan.applied {
		if err := b.publish.broadcastExperiment(plan.name, plan.action); err != nil {
			response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishBroadcastFailed, "",
				"experiment was stored but its live update could not be broadcast"))
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
	return builderPublishStatus(p.action, p.applied)
}

type builderPublishExperimentPlan struct {
	action  string
	name    string
	applied bool
	// existing is the experiment as preflight read it, or nil when there is
	// none.
	existing *store.Config
	// rebuild returns the updated config an update writes, from the
	// experiment config as it is when it is written.
	rebuild func(*store.Config) (*store.Config, error)
}

func (p builderPublishExperimentPlan) status() bapi.PublishStatus {
	return builderPublishStatus(p.action, p.applied)
}

// builderPublishStatus is a stage's status: what its action did ("created",
// "updated"), or skipped when an earlier attempt already applied it.
func builderPublishStatus(action string, applied bool) bapi.PublishStatus {
	if applied {
		return bapi.PublishSkipped
	}

	return bapi.PublishStatus(action + "d")
}

// builderPublishScenarioPlan is what the scenario stage of a publication
// does with the Scenario configs that the draft document lists. It adds the
// topology to the "topology" annotation of each scenario that does not name
// it yet (changed, each an update of the stored config). It does not change
// the others (unchanged).
type builderPublishScenarioPlan struct {
	changed   []builderPublishConfigPlan
	unchanged []string
	// listed is every scenario the document lists, in its order, and stored
	// each of them as preflight read it.
	listed []string
	stored []*store.Config
	// picked is the experiment's scenario as it is once the stage ran, or
	// nil when the experiment has none or no experiment is published.
	picked *store.Config
}

// applied reports whether the stage has nothing to write: every listed
// scenario already names the topology.
func (p builderPublishScenarioPlan) applied() bool {
	return len(p.changed) == 0
}

// status is the scenario stage's status: updated when it writes a scenario,
// skipped otherwise.
func (p builderPublishScenarioPlan) status() bapi.PublishStatus {
	return builderPublishStatus(builderPublishActionUpdate, p.applied())
}

// message says what the scenario stage did to the listed scenarios: which
// it added the topology to, and which already named it.
func (p builderPublishScenarioPlan) message(topologyName string) string {
	changed := make([]string, len(p.changed))
	for i, change := range p.changed {
		changed[i] = change.config.Metadata.Name
	}

	switch {
	case len(changed) == 0:
		return fmt.Sprintf("%s already %s topology %s",
			builderScenarioList(p.unchanged), builderNameVerb(len(p.unchanged)), topologyName)
	case len(p.unchanged) == 0:
		return fmt.Sprintf("added topology %s to %s", topologyName, builderScenarioList(changed))
	}

	return fmt.Sprintf("added topology %s to %s; %s already %s it",
		topologyName, builderScenarioList(changed),
		builderScenarioList(p.unchanged), builderNameVerb(len(p.unchanged)))
}

// builderScenarioList names scenarios in a stage message: "scenario a" or
// "scenarios a, b".
func builderScenarioList(names []string) string {
	if len(names) == 1 {
		return "scenario " + names[0]
	}

	return "scenarios " + strings.Join(names, ", ")
}

// builderNameVerb is "names" for one scenario and "name" for several.
func builderNameVerb(count int) string {
	if count == 1 {
		return "names"
	}

	return "name"
}

// builderScenarioStageConfig is the config a scenario stage reports: the
// one scenario a document lists, or none when it lists several, which the
// stage's message names.
func builderScenarioStageConfig(listed []string) string {
	if len(listed) != 1 {
		return ""
	}

	return store.ConfigFullName(builderKindScenario, listed[0])
}

type builderPublishPlan struct {
	topology builderPublishConfigPlan
	// scenarios is nil when the draft's document lists no scenario.
	scenarios  *builderPublishScenarioPlan
	experiment *builderPublishExperimentPlan
}

func (b *builderAPI) preflightPublish(
	ctx context.Context,
	actor builderActor,
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

	plan := &builderPublishPlan{topology: topologyPlan, scenarios: nil, experiment: nil}

	scenarios, err := b.preflightScenarios(actor, document, request)
	if err != nil {
		return nil, err
	}

	if len(scenarios.listed) > 0 {
		plan.scenarios = scenarios
	}

	if request.Experiment != nil {
		if request.Experiment.Action == builderPublishActionUpdate {
			if err := mergedIncludesRefusal(actor, request.Experiment.Name, includes); err != nil {
				return nil, err
			}
		}

		if err := includedHostnameRefusal(request.Experiment.Name, includes); err != nil {
			return nil, err
		}

		experimentPlan, experimentErr := b.preflightExperiment(
			meta,
			document,
			projection,
			request.Topology.Name,
			request.scenarioName(),
			scenarios.picked,
			*request.Experiment,
		)
		if experimentErr != nil {
			return nil, experimentErr
		}

		plan.experiment = experimentPlan
	}

	resuming := plan.topology.applied &&
		(plan.scenarios == nil || plan.scenarios.applied()) &&
		(plan.experiment == nil || plan.experiment.applied)
	if !resuming {
		if err := b.checkSourceFreshness(ctx, actor, meta, snapshot, document); err != nil {
			return nil, err
		}
	}

	return plan, nil
}

// publishProjectionRefusal refuses a document whose topology projection
// cannot be published. The message names interfaces without a VLAN,
// addresses that interfaces share, and hostnames that phenix refuses. Clients
// show the message, so these names are not only in its cause. Past the first
// few, the message gives only their number. Its code is
// [bdoc.CodePublishBlocked], and its issues name each problem at its location
// (see [builderCodedError]). A projection that the phenix schema refuses for
// a different reason has [bdoc.CodePublishTopologyInvalid].
func publishProjectionRefusal(topologyName string, err error) error {
	problems, named := projectionProblems(err)
	if !named {
		return weberror.NewWebError(err, "builder document cannot be published as topology %s", topologyName).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishTopologyInvalid))
	}

	return weberror.NewWebError(err, "topology %s cannot be published: %s", topologyName, problems).
		SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishBlocked))
}

// projectionProblems names, as clients show them, the problems that err
// reports (see [bdoc.InterfaceVLANError], [bdoc.InterfaceAddressError] and
// [bdoc.NodeHostnameError]). These are interfaces without a VLAN, addresses
// that interfaces share, or hostnames that phenix refuses. It names the
// first few, then how many more. It reports false for an error that names
// none.
func projectionProblems(err error) (string, bool) {
	const listed = 3

	var (
		vlanErr     *bdoc.InterfaceVLANError
		addressErr  *bdoc.InterfaceAddressError
		hostnameErr *bdoc.NodeHostnameError
		problems    []string
		// What the problems past the first few are, after their number.
		one, many string
	)

	switch {
	case errors.As(err, &vlanErr):
		problems, one, many = vlanErr.Problems, "interface has no VLAN", "interfaces have no VLAN"
	case errors.As(err, &addressErr):
		problems, one, many = addressErr.Problems, "address is used more than once", "addresses are used more than once"
	case errors.As(err, &hostnameErr):
		problems, one, many = hostnameErr.Problems, "hostname phenix refuses", "hostnames phenix refuses"
	}

	if len(problems) == 0 {
		return "", false
	}

	message := strings.Join(problems[:min(len(problems), listed)], "; ")

	switch more := len(problems) - listed; {
	case more == 1:
		message += "; 1 more " + one
	case more > 1:
		message += fmt.Sprintf("; %d more %s", more, many)
	}

	return message, true
}

// mergedIncludesRefusal is why an experiment update may not merge the
// topologies that the published topology includes. The update merges them
// itself (see experimentTopology). Thus it reads them only as import does:
// from the config store, and only when the caller may read them. An
// experiment create leaves the merge to phenix, as it is outside the
// Builder.
func mergedIncludesRefusal(actor builderActor, experimentName string, includes bdoc.IncludeReport) error {
	for _, problem := range includes.Unreadable {
		if errors.Is(problem.Err, errBuilderIncludeForbidden) {
			return builderForbidden(
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
		).SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentUnmerged))
	}

	return nil
}

// includedHostnameRefusal refuses an experiment whose included topologies
// define a hostname phenix refuses in an experiment, which phenix would find
// only once the topology is written. The editor leaves included devices to
// their own topology, and so does a topology publish.
func includedHostnameRefusal(experimentName string, includes bdoc.IncludeReport) error {
	const listed = 3

	if len(includes.Refused) == 0 {
		return nil
	}

	reasons := make([]string, 0, listed+1)

	for _, refused := range includes.Refused[:min(len(includes.Refused), listed)] {
		reasons = append(reasons, fmt.Sprintf("in included topology %s, %s", refused.Include, refused.Reason))
	}

	switch more := len(includes.Refused) - listed; {
	case more == 1:
		reasons = append(reasons, "1 more hostname phenix refuses")
	case more > 1:
		reasons = append(reasons, fmt.Sprintf("%d more hostnames phenix refuses", more))
	}

	return weberror.NewWebError(
		nil, "experiment %s cannot be published: %s", experimentName, strings.Join(reasons, "; "),
	).SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishIncludeHostname))
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
		).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishIncludeClash))
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
		).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishIncludeClash))
	}
}

// builderPublishVerb is the configs verb a publish action needs: create
// for a create, and update for an update.
func builderPublishVerb(action string) builderVerb {
	if action == builderPublishActionCreate {
		return builderVerbCreate
	}

	return builderVerbUpdate
}

func (b *builderAPI) authorizePublishTargets(actor builderActor, request builderPublishRequest) error {
	if !builderBaseAllowed(
		actor.role,
		builderPublishVerb(request.Topology.Action),
		store.ConfigFullName(builderKindTopology, request.Topology.Name),
	) {
		return builderForbidden(actor, "publishing topology "+request.Topology.Name)
	}

	if request.Experiment != nil &&
		(!builderExperimentAllowed(actor.role, request.Experiment.Action, request.Experiment.Name) ||
			!builderBaseAllowed(
				actor.role,
				builderPublishVerb(request.Experiment.Action),
				store.ConfigFullName(kindExperiment, request.Experiment.Name),
			)) {
		return builderForbidden(actor, "publishing experiment config "+request.Experiment.Name)
	}

	return nil
}

func builderExperimentAllowed(role rbac.Role, action, name string) bool {
	switch action {
	case builderPublishActionCreate:
		return role.Allowed("experiments", "create", name)
	case builderPublishActionUpdate:
		return role.Allowed("experiments", "update", name)
	}

	return false
}

// checkSourceFreshness refuses a draft whose source config changed after the
// draft was imported. The change can be to its spec, or to the legacy
// Builder diagram that a topology carries, which an update removes (see
// [bdoc.ImportDigest]). A publish to the source also changes it. Thus a
// source is fresh when it holds the publication of this draft, or of the
// published document that the draft was opened from, and nothing else
// changed it since (see [builderAPI.sourceHoldsDraftPublication]). A source
// deleted since then holds nothing that a publication could overwrite. Thus
// the draft publishes as a new diagram does: a published topology deleted
// from the drafts page is created again.
func (b *builderAPI) checkSourceFreshness(
	ctx context.Context,
	actor builderActor,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
) error {
	source := document.Source
	if source == nil || source.Kind == bdoc.SourceKindManual {
		return nil
	}

	if draftSourceUploaded(meta) {
		return nil
	}

	var kind string
	switch source.Kind {
	case bdoc.SourceKindTopology:
		kind = builderKindTopology
	case bdoc.SourceKindExperiment:
		kind = kindExperiment
	case bdoc.SourceKindManual:
		return nil
	default:
		return weberror.NewWebError(nil, "builder source kind %s cannot be published", source.Kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	fullName := store.ConfigFullName(kind, source.Name)
	if !builderBaseAllowed(actor.role, builderVerbGet, fullName) ||
		!builderSourceGetAllowed(actor.role, kind, source.Name) {
		return builderForbidden(actor, "checking builder source "+fullName)
	}

	current, err := b.getConfig(fullName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil
		}

		return weberror.NewWebError(err, "unable to reload builder source %s", fullName).
			SetStatus(http.StatusInternalServerError)
	}

	digest, err := bdoc.ImportDigest(*current)
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
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishSourceChanged))
	}

	return nil
}

// sourceHoldsDraftPublication reports whether only a publish of this draft,
// or of the published document it was opened from, changed a source config.
// That is a topology that still holds exactly the projection of that
// document, or an experiment that still has the digest recorded at its
// publish (see [experimentHoldsDraftPublication]). The configure stage of the
// apps rewrites the spec of an experiment during the publish. Thus an
// experiment cannot be compared with the document itself.
func (b *builderAPI) sourceHoldsDraftPublication(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	source *store.Config,
) (bool, error) {
	if source.Kind == kindExperiment {
		_, unchanged, err := experimentHoldsDraftPublication(meta, source)

		return unchanged, err
	}

	held, err := b.holdsDraftDocument(ctx, meta, snapshot, source)

	return held.unchanged, err
}

// experimentHoldsDraftPublication reports whether an experiment records (see
// [builderExperimentAnnotation]) that its last publisher was this draft, or
// the published document that the draft was opened from or last published.
// If so, it also reports whether the experiment still has the digest
// recorded then. That means nothing else changed it since, including the
// configs API, another draft or the start of the experiment.
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

// publishedTopologyReference is the document reference that a publication
// stores on its topology. It holds the digest and the ID of the published
// document, and the path of a Builder file that the updated topology already
// named. Publish never writes that file. After the publish, the topology is
// read from the stored document. The file is used only when no such document
// is stored and the file content has that digest.
func publishedTopologyReference(published *bapi.PublishedDocument, existing *store.Config) bapi.DocumentReference {
	reference := published.Reference()

	if existing == nil {
		return reference
	}

	if stored, err := bapi.DecodeReference(existing.Metadata.Annotations[bapi.DocumentAnnotation]); err == nil {
		reference.Path = stored.Path
	}

	return reference
}

// builderFileNotWrittenWarning is what a publication says about the Builder
// file that its topology still names. From now on, the topology is read from
// the stored document, and the file stays as it was.
func builderFileNotWrittenWarning(topology, path string) string {
	return fmt.Sprintf(
		"Topology %s names the Builder file %s, which Publish does not change. "+
			"Download the diagram and replace the file to keep it in step.",
		topology, path,
	)
}

// draftNamesDocument reports whether the document reference of a topology
// names, by its ID alone, a stored document of this draft. That is a document
// with exactly the content the draft publishes now, or one that the draft
// recorded (see [draftOwnsDocument]). It reads nothing, so it holds for a
// document that can no longer be read.
func draftNamesDocument(
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	topology string,
	reference bapi.DocumentReference,
) bool {
	id := reference.StoredID(topology)

	return id != "" &&
		(id == bapi.PublishedDocumentID(topology, snapshot.Manifest.Digest) || draftOwnsDocument(meta, id))
}

// draftOwnsDocument reports whether a published document is one of the
// draft's own: the one it was opened from, the one it last published, or the
// one that the forked draft had last published at the time of the fork.
func draftOwnsDocument(meta *bapi.DraftMetadata, id string) bool {
	return id != "" &&
		(id == openedDocumentID(meta) ||
			(meta.Publication != nil && id == meta.Publication.DocumentID) ||
			(meta.Forked != nil && id == meta.Forked.DocumentID))
}

// builderDocTokenPrefix starts the source token of a draft opened from a
// published document: "builder-doc/<document id>".
const builderDocTokenPrefix = "builder-doc/"

// builderFileTokenPrefix starts the source token of a draft opened from the
// Builder file that a topology names: "builder-file/<topology name>/<digest>".
// The digest is that of the file document when the draft was opened.
const builderFileTokenPrefix = "builder-file/"

// openedBuilderFile returns the topology and the digest the source token of
// a draft opened from a Builder file names. It reports false for any other
// token, and for one that names no topology or no digest.
func openedBuilderFile(token string) (string, string, bool) {
	rest, ok := strings.CutPrefix(token, builderFileTokenPrefix)
	if !ok {
		return "", "", false
	}

	// A topology name holds no slash, and nor does a digest.
	split := strings.LastIndex(rest, "/")
	if split <= 0 {
		return "", "", false
	}

	topology, digest := rest[:split], rest[split+1:]
	if strings.Contains(topology, "/") || !bdoc.IsDigest(digest) {
		return "", "", false
	}

	return topology, digest, true
}

// draftOpenedFile reports whether a draft was opened from the Builder file
// the topology names, when that file's document had this digest.
func draftOpenedFile(meta *bapi.DraftMetadata, topology, digest string) bool {
	opened, at, ok := openedBuilderFile(meta.SourceToken)

	return ok && opened == topology && at == digest
}

// openedTopologyFile returns the document of the Builder file that the
// source token of a new draft names. It reads the file as GET
// /builder/topologies/{topology}/document reads it, with the same
// authorization. A draft opened from a file may update the topology that
// names it (see [builderAPI.holdsDraftDocument]). Thus only a caller who may
// read the document of the topology may name it, and only while the file
// still holds what the caller opened. It refuses with 409 a topology that is
// no longer read from its file, and a file whose document now has another
// digest.
func (b *builderAPI) openedTopologyFile(
	ctx context.Context,
	actor builderActor,
	token string,
) (builderTopologyDocument, error) {
	var none builderTopologyDocument

	topology, digest, ok := openedBuilderFile(token)
	if !ok {
		return none, builderNotFound("builder document of topology", strings.TrimPrefix(token, builderFileTokenPrefix))
	}

	_, resolved, err := b.readableTopologyDocument(ctx, actor, topology)

	switch {
	case err != nil:
		return none, err
	case resolved.record != nil:
		return none, weberror.NewWebError(
			nil,
			"Topology %s is no longer read from its Builder file. Open its diagram again.",
			topology,
		).SetStatus(http.StatusConflict)
	case resolved.digest != digest:
		return none, weberror.NewWebError(
			nil,
			"The Builder file of topology %s changed since it was opened. Open its diagram again.",
			topology,
		).SetStatus(http.StatusConflict)
	}

	return resolved, nil
}

// builderUploadedTokenPrefix starts two source tokens:
// "uploaded/<kind>/<name>" for a draft generated from an uploaded config,
// and "uploaded/legacy-xml" for a draft converted from an uploaded legacy
// Builder diagram that came without a topology.
const builderUploadedTokenPrefix = "uploaded/"

// draftSourceUploaded reports whether a draft was generated from an upload,
// which can never authorize an update of a stored config.
func draftSourceUploaded(meta *bapi.DraftMetadata) bool {
	return strings.HasPrefix(meta.SourceToken, builderUploadedTokenPrefix)
}

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
// draft that forks the draft named "<owner>/<draft id>". A save of the
// editor history as a new draft makes such a fork. The values are the source
// token of that draft, and its last publication (or else what that draft
// had forked).
//
// The fork may then update what that draft published or was opened from,
// while nothing else changed it since (see [builderAPI.holdsDraftDocument]
// and [experimentHoldsDraftPublication]). It may not update what that draft
// publishes later. Its source token is not the token of the published
// document. Thus, when the user opens that published diagram, the fork does
// not open as the draft of the user for it.
//
// Only a caller who may read that draft gets its identity: its owner, a
// user it is shared with, or a user that holds "builder-drafts" "get" for
// it. For any other caller, it is a draft that does not exist (see
// [builderAPI.draftFor]).
func (b *builderAPI) forkOrigin(
	r *http.Request,
	actor builderActor,
	forkOf string,
) (string, *bapi.ForkedPublication, error) {
	owner, draftID, _ := strings.Cut(forkOf, "/")

	meta, err := b.namedDraft(r, actor, builderVerbGet, "forking a builder draft", owner, draftID)
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

// builderHeldDocument is what [builderAPI.holdsDraftDocument] found of the
// document a topology references.
type builderHeldDocument struct {
	// owned is set when the document is one of the draft's own.
	owned bool
	// unchanged is set when the document is the draft's own and the
	// topology's spec is still exactly its projection.
	unchanged bool
	// file is set when the document was read from the Builder file the
	// topology names.
	file bool
}

// holdsDraftDocument reports whether a topology config holds a document of
// this draft. If so, it also reports whether the topology spec is still
// exactly the projection of that document. That means nothing changed the
// topology since the document was published to it.
//
// The document that the topology references (see
// [builderAPI.topologyDocument]) is the draft's own in these cases:
//
//   - The reference names one of the stored documents of the draft (see
//     [draftNamesDocument]).
//   - The document holds exactly the content that the draft publishes now.
//   - The record of the document says that this draft published it.
//   - The document was read from the Builder file that the topology names,
//     the draft was opened from that file, and the file still holds what it
//     held then (see [draftOpenedFile]).
//
// A document that can no longer be read or projected counts as changed.
// This includes a Builder file that cannot be used. Such a document is the
// draft's own only by the reference, because its record vouches for nothing.
func (b *builderAPI) holdsDraftDocument(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	topology *store.Config,
) (builderHeldDocument, error) {
	none := builderHeldDocument{owned: false, unchanged: false, file: false}

	if topology == nil {
		return none, nil
	}

	value, ok := topology.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return none, nil
	}

	reference, err := bapi.DecodeReference(value)
	if err != nil {
		return none, nil //nolint:nilerr // an invalid reference names no document
	}

	name := topology.Metadata.Name
	held := builderHeldDocument{owned: draftNamesDocument(meta, snapshot, name, reference), unchanged: false, file: false}

	resolved, found, err := b.topologyDocument(ctx, name, reference)

	var fileErr *bapi.DocumentFileError

	switch {
	case errors.As(err, &fileErr),
		errors.Is(err, bapi.ErrNotFound), errors.Is(err, bapi.ErrCorrupt), errors.Is(err, bapi.ErrInvalid):
		return held, nil
	case err != nil:
		return held, builderWebError(err, "unable to read the builder document of topology %s", name)
	case !found:
		return held, nil
	}

	held.file = resolved.record == nil
	held.owned = held.owned || resolved.digest == snapshot.Manifest.Digest ||
		(resolved.record != nil && resolved.record.DraftID == meta.ID) ||
		(held.file && draftOpenedFile(meta, name, resolved.digest))

	if !held.owned {
		return none, nil
	}

	held.unchanged, err = topologyHoldsProjection(resolved.data, topology)

	return held, err
}

func builderSourceGetAllowed(role rbac.Role, kind, name string) bool {
	switch kind {
	case builderKindTopology:
		return role.Allowed("topologies", "get", name)
	case kindExperiment:
		return role.Allowed("experiments", "get", name)
	default:
		return false
	}
}

func (b *builderAPI) preflightTopology(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
	topology *store.Config,
	target builderPublishTarget,
) (builderPublishConfigPlan, error) {
	existing, exists, err := b.configIfExists(builderKindTopology, target.Name)
	if err != nil {
		return builderPublishConfigPlan{}, err
	}

	applied, err := b.topologyPublicationApplied(ctx, existing, snapshot.Manifest.Digest)
	if err != nil {
		return builderPublishConfigPlan{}, err
	}

	if err := requirePublishAction(builderKindTopology, target, exists, applied); err != nil {
		return builderPublishConfigPlan{}, err
	}

	if target.Action == builderPublishActionUpdate && !applied {
		if err := b.topologyUpdateRefusal(ctx, meta, snapshot, document, existing); err != nil {
			return builderPublishConfigPlan{}, err
		}
	}

	if exists {
		keepStoredMetadata(topology, existing)
	}

	return builderPublishConfigPlan{
		action: target.Action, existing: existing, config: topology, applied: applied,
	}, nil
}

// topologyUpdateRefusal refuses an update of an existing topology that this
// draft may not update. A draft updates a topology that holds one of its own
// documents (see [builderAPI.holdsDraftDocument]), which it published or was
// opened from, while nothing else changed the topology since. That is how a
// draft publishes again after more edits, whatever it was loaded from.
//
// For a topology read from the Builder file it names, this means a draft
// opened from the file as it is now, while the topology is still what the
// file publishes. A draft opened from that file updates the topology in no
// other way.
//
// Otherwise a draft updates only the topology it was loaded from: the one it
// was imported from, or the one its source experiment was built from.
// checkSourceFreshness checks the freshness of that topology. A topology that
// the legacy Builder drew is updated the same way, but only by the draft
// imported from that topology itself (see [topologyUpdateMatchesSource]).
func (b *builderAPI) topologyUpdateRefusal(
	ctx context.Context,
	meta *bapi.DraftMetadata,
	snapshot *bapi.Snapshot,
	document *bdoc.Document,
	existing *store.Config,
) error {
	name := existing.Metadata.Name

	held, err := b.holdsDraftDocument(ctx, meta, snapshot, existing)

	switch {
	case err != nil:
		return err
	case held.owned && held.unchanged:
		return nil
	case held.owned && held.file:
		return weberror.NewWebError(
			nil, "topology %s is not what its Builder file publishes, so this draft cannot update it", name,
		).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyFileMismatch))
	case held.owned:
		return weberror.NewWebError(nil, "topology %s changed after this draft published it", name).
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyChanged))
	}

	// Check this before the import rule. A document read from the file may
	// itself have been imported from this topology, which says nothing about
	// the file.
	if opened, _, ok := openedBuilderFile(meta.SourceToken); ok && opened == name {
		return weberror.NewWebError(
			nil, "topology %s or its Builder file changed after this draft was opened from the file", name,
		).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyFileChanged))
	}

	if topologyUpdateMatchesSource(meta, document, existing) {
		return nil
	}

	return weberror.NewWebError(nil, "topology %s is not the source this draft was loaded from", name).
		SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyNotSource))
}

// topologyUpdateMatchesSource reports whether the document was imported from
// the topology, or from an experiment built from it. An upload names no
// stored config, so it never matches.
//
// A topology that still carries a diagram of the legacy Builder matches only
// a document imported from the topology itself. That import converted the
// diagram, which the update removes. An experiment built from the topology
// never held the diagram.
func topologyUpdateMatchesSource(meta *bapi.DraftMetadata, document *bdoc.Document, existing *store.Config) bool {
	if draftSourceUploaded(meta) || document.Source == nil {
		return false
	}

	target := existing.Metadata.Name

	switch document.Source.Kind {
	case bdoc.SourceKindTopology:
		return document.Source.Name == target
	case bdoc.SourceKindExperiment:
		return document.Source.Topology == target && !bdoc.HasLegacyDiagram(*existing)
	case bdoc.SourceKindManual:
	}

	return false
}

// topologyPublicationApplied reports whether an existing topology already
// holds the publication of the content with this digest, so that a new
// publish of it writes nothing. This is true when its document reference
// names the stored document that the publication stores for it (see
// [existingBuilderDocumentReference]).
//
// A reference that also names a Builder file is on a topology that was never
// published. Its digest only pins the file. Such a topology holds the
// publication only after that document is stored. Until then, the topology
// is read from the file and updated as any other topology (see
// [builderAPI.topologyUpdateRefusal]).
func (b *builderAPI) topologyPublicationApplied(ctx context.Context, existing *store.Config, digest string) (bool, error) {
	reference, matches := existingBuilderDocumentReference(existing, digest)
	if !matches || reference.Path == "" {
		return matches, nil
	}

	name := existing.Metadata.Name

	// A damaged record counts as stored. A new store of the document repairs
	// it.
	_, err := b.drafts.GetPublishedDocument(ctx, reference.StoredID(name))

	switch {
	case errors.Is(err, bapi.ErrNotFound):
		return false, nil
	case err != nil && !errors.Is(err, bapi.ErrCorrupt):
		return false, builderWebError(err, "unable to read the builder document of topology %s", name)
	}

	return true, nil
}

// existingBuilderDocumentReference returns the document reference of an
// existing topology. It also reports whether the reference names the stored
// document that a publication of the content with this digest stores for
// it, whether the reference holds the digest or the ID. It reads no record.
func existingBuilderDocumentReference(existing *store.Config, digest string) (bapi.DocumentReference, bool) {
	none := bapi.DocumentReference{Digest: "", ID: "", Path: ""}

	if existing == nil || existing.Metadata.Annotations == nil {
		return none, false
	}

	value, ok := existing.Metadata.Annotations[bapi.DocumentAnnotation]
	if !ok {
		return none, false
	}

	ref, err := bapi.DecodeReference(value)
	if err != nil {
		return none, false
	}

	return ref, ref.Publishes(existing.Metadata.Name, digest)
}

// preflightScenarios plans the scenario stage of a publication. The stage
// runs in either mode whenever the draft document lists scenarios. Each
// listed Scenario config must exist. When the "topology" annotation of a
// scenario does not name the topology yet, the stage adds it. This needs the
// configs update permission for that scenario. A scenario that the caller may
// not read is refused as one that does not exist, so its existence is not
// disclosed. The scenario of the experiment must be one of those listed. For
// a document that lists no scenario, the plan lists none, and the
// publication has no scenario stage.
func (b *builderAPI) preflightScenarios(
	actor builderActor,
	document *bdoc.Document,
	request builderPublishRequest,
) (*builderPublishScenarioPlan, error) {
	picked := request.scenarioName()
	if picked != "" && !slices.Contains(document.Scenarios, picked) {
		return nil, builderScenarioRefusal(
			weberror.NewWebError(nil, "scenario %s is not one of the scenarios this draft lists", picked),
			bdoc.CodePublishScenarioNotListed, picked,
		)
	}

	topologyName := request.Topology.Name
	plan := &builderPublishScenarioPlan{
		changed: nil, unchanged: nil, listed: slices.Clone(document.Scenarios), stored: nil, picked: nil,
	}

	for _, name := range document.Scenarios {
		existing, err := b.listedScenario(actor, name)
		if err != nil {
			return nil, err
		}

		plan.stored = append(plan.stored, existing)
		scenario := existing

		if hasTopologyAnnotation(existing.Metadata.Annotations["topology"], topologyName) {
			plan.unchanged = append(plan.unchanged, name)
		} else {
			if !builderBaseAllowed(actor.role, builderVerbUpdate, existing.FullName()) {
				return nil, builderForbidden(actor, fmt.Sprintf("adding topology %s to scenario %s", topologyName, name))
			}

			scenario = cloneBuilderConfig(existing)
			if scenario.Metadata.Annotations == nil {
				scenario.Metadata.Annotations = store.Annotations{}
			}

			scenario.Metadata.Annotations["topology"] = addTopologyAnnotation(
				scenario.Metadata.Annotations["topology"],
				topologyName,
			)

			if err := types.ValidateConfigSpec(*scenario); err != nil {
				return nil, builderScenarioRefusal(
					weberror.NewWebError(err, "scenario %s is not valid", name), bdoc.CodePublishScenarioInvalid, name,
				)
			}

			plan.changed = append(plan.changed, builderPublishConfigPlan{
				action: builderPublishActionUpdate, existing: existing, config: scenario, applied: false,
			})
		}

		if name == picked {
			plan.picked = scenario
		}
	}

	return plan, nil
}

// listedScenario reads a Scenario config the draft's document lists. One
// that does not exist, and one the caller may not read (the configs get and
// the scenarios list permissions), are refused alike, with 422.
func (b *builderAPI) listedScenario(actor builderActor, name string) (*store.Config, error) {
	missing := builderScenarioRefusal(
		weberror.NewWebError(nil, "scenario %s does not exist", name), bdoc.CodePublishScenarioMissing, name,
	)

	full := store.ConfigFullName(builderKindScenario, name)
	if full == "" ||
		!builderBaseAllowed(actor.role, builderVerbGet, full) ||
		!builderKindAllowed(actor.role, builderScenarios, name) {
		return nil, missing
	}

	existing, exists, err := b.configIfExists(builderKindScenario, name)

	switch {
	case err != nil:
		return nil, err
	case !exists:
		return nil, missing
	}

	return existing, nil
}

// builderScenarioMetadata is the metadata key of a refusal about one of the
// scenarios that the draft document lists. Its value names that scenario, so
// a client can tell whether it is the scenario of the experiment.
const builderScenarioMetadata = "scenario"

// builderScenarioRefusal answers a publication refused for name, one of the
// scenarios that the draft document lists. The answer has 422, the refusal
// code, and the scenario name in its metadata.
func builderScenarioRefusal(refusal *weberror.WebError, code bdoc.Code, name string) *weberror.WebError {
	return refusal.SetStatus(http.StatusUnprocessableEntity).WithCode(string(code)).
		WithMetadata(builderScenarioMetadata, name, true)
}

// publishScenarioStage writes the scenario stage of a publication. It adds
// the topology to each listed scenario that does not name it yet, then
// stores and broadcasts that scenario. It adds the stage to the response,
// with a message that says what it did. If a write fails, it returns the
// error, after a warning that names the scenarios it already updated.
func (b *builderAPI) publishScenarioStage(
	topologyName string,
	plan builderPublishScenarioPlan,
	response *builderPublishResponse,
) error {
	written := make([]string, 0, len(plan.changed))

	for _, change := range plan.changed {
		stored, err := b.writePublishedConfig(change, change.config)
		if err != nil {
			if len(written) > 0 {
				response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishScenarioPartial, "", fmt.Sprintf(
					"topology %s was added to %s before the scenario stage failed",
					topologyName, builderScenarioList(written),
				)))
			}

			return err
		}

		written = append(written, stored.Metadata.Name)

		if err := b.publish.broadcastConfig(stored, change.action); err != nil {
			response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishBroadcastFailed, "",
				fmt.Sprintf("scenario %s was stored but its live update could not be broadcast", stored.Metadata.Name)))
			plog.Error(plog.TypeSystem, "broadcasting published scenario", "scenario", stored.Metadata.Name, "err", err)
		}
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageScenario, Status: plan.status(), Message: plan.message(topologyName),
		Config: builderScenarioStageConfig(plan.listed),
	})

	return nil
}

func (b *builderAPI) preflightExperiment(
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioConfig *store.Config,
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
	if err := requirePublishAction(kindExperiment, target, exists, applied); err != nil {
		return nil, err
	}

	plan := &builderPublishExperimentPlan{
		action: target.Action, name: target.Name, applied: applied, existing: existing, rebuild: nil,
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
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}
	if current.Running() {
		return nil, weberror.NewWebError(nil, "running experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishExperimentRunning))
	}

	if topologyErr != nil {
		return nil, weberror.NewWebError(topologyErr, "experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	plan.rebuild = func(current *store.Config) (*store.Config, error) {
		return updatedExperimentConfig(
			current,
			topologySpec,
			projection,
			topologyName,
			scenarioName,
			scenarioConfig,
		)
	}

	updated, err := plan.rebuild(existing)
	if err != nil {
		return nil, weberror.NewWebError(err, "experiment %s cannot be updated", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	if err := types.ValidateConfigSpec(*updated); err != nil {
		return nil, weberror.NewWebError(err, "experiment %s is not valid", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	return plan, nil
}

// experimentCreateRefusal refuses an experiment before anything is written.
// It refuses what experiment.Create would refuse only after the document,
// topology and scenario are written:
//
//   - the reserved name "all"
//   - a name longer than a bridge name, when phenix names the bridge of each
//     experiment after it (auto bridge mode)
//   - a config that the Experiment schema refuses
//
// validatePublishTarget already checked the name against the config naming
// rule.
func experimentCreateRefusal(name string, projection *bdoc.Topology) error {
	// experiment.Create's limit, the longest Linux interface name.
	const maxBridgeName = 15

	if strings.EqualFold(name, "all") {
		return weberror.NewWebError(nil, "experiment %s is reserved: phenix uses the name to mean every experiment", name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentReserved))
	}

	if common.BridgeMode == common.BridgeModeAuto && len(name) > maxBridgeName {
		return weberror.NewWebError(
			nil,
			"experiment %s has a name longer than %d characters, and this server names each experiment's bridge after it",
			name, maxBridgeName,
		).SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentNameLong))
	}

	created, err := store.NewConfig(kindExperiment + "/" + name)
	if err != nil {
		return invalidPublishTarget(builderSourceExperiment, name)
	}

	created.Version = store.APIGroup + "/" + version.StoredVersion[kindExperiment]
	created.Spec = map[string]any{
		"experimentName": name,
		"topology":       projection.Spec,
		"vlans":          map[string]any{"aliases": projection.VLANAliases},
	}

	if err := types.ValidateConfigSpec(*created); err != nil {
		return weberror.NewWebError(err, "experiment %s is not valid", name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	return nil
}

// experimentUpdateRefusal refuses an update of an existing experiment that
// this draft may not update. A draft updates an experiment that it
// published, or that the published document it was opened from published.
// This applies only while nothing else changed the experiment since (see
// [experimentHoldsDraftPublication]). Otherwise it updates only the
// experiment it was imported from. checkSourceFreshness checks the
// freshness of that experiment.
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
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishExperimentChanged))
	}

	return weberror.NewWebError(nil, "experiment %s is not the source this draft was loaded from", name).
		SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishExperimentNotSource))
}

func experimentUpdateMatchesSource(
	meta *bapi.DraftMetadata,
	document *bdoc.Document,
	target string,
) bool {
	return !draftSourceUploaded(meta) &&
		document.Source != nil &&
		document.Source.Kind == bdoc.SourceKindExperiment &&
		document.Source.Name == target
}

// experimentTopology returns the topology that an experiment holds for the
// projection. The projection does not include the devices of included
// topologies. It names those topologies instead (see
// [bdoc.Document.ToTopology]). phenix merges them into an experiment when it
// creates one. Thus an update merges them the same way, after
// mergedIncludesRefusal checks that it may.
func (b *builderAPI) experimentTopology( //nolint:ireturn // returns decodeTopology's interface
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

	config, err := store.NewConfig(builderKindTopology + "/" + topologyName)
	if err != nil {
		return nil, fmt.Errorf("creating topology config: %w", err)
	}

	config.Version = bdoc.TopologyAPIVersion
	config.Spec = projection.Spec

	return b.publish.decodeTopology(*config)
}

// updatedExperimentConfig is the experiment config that an update writes. It
// is the existing config with the topology and VLAN aliases of the
// projection, and the scenario of scenarioConfig as it is after the scenario
// stage ran. It is merged for the topology as phenix merges one when it
// creates an experiment. A nil scenarioConfig leaves the experiment without
// a scenario.
func updatedExperimentConfig(
	existing *store.Config,
	topologySpec ifaces.TopologySpec,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioConfig *store.Config,
) (*store.Config, error) {
	exp, err := types.DecodeExperimentFromConfig(*existing)
	if err != nil {
		return nil, err
	}

	exp.Spec.SetTopology(topologySpec)
	exp.Spec.VLANs().SetAliases(projection.VLANAliases)

	if scenarioConfig == nil {
		exp.Spec.SetScenario(nil)
	} else {
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

// publishConfigStage writes the topology config cfg as plan says, and adds
// its stage to the response. It broadcasts the change, unless an earlier
// attempt already applied it. It returns the config as written.
func (b *builderAPI) publishConfigStage(
	stage string,
	plan builderPublishConfigPlan,
	cfg *store.Config,
	response *builderPublishResponse,
) (*store.Config, error) {
	written, err := b.writePublishedConfig(plan, cfg)
	if err != nil {
		return nil, err
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: stage, Status: plan.status(), Message: "", Config: written.FullName(),
	})

	if !plan.applied {
		if err := b.publish.broadcastConfig(written, plan.action); err != nil {
			response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishBroadcastFailed, "",
				stage+" was stored but its live update could not be broadcast"))
			plog.Error(plog.TypeSystem, "broadcasting published "+stage, "err", err)
		}
	}

	return written, nil
}

// keepStoredMetadata gives cfg, a config about to replace existing, the
// stored config's metadata (with its own copy of the annotations) and status.
func keepStoredMetadata(cfg, existing *store.Config) {
	cfg.Metadata = existing.Metadata
	cfg.Metadata.Annotations = maps.Clone(existing.Metadata.Annotations)
	cfg.Status = existing.Status
}

func (b *builderAPI) writePublishedConfig(
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

// writeExperiment creates or updates the experiment while it holds the
// experiment lock. Then it records on the experiment which draft and
// document published it (see [builderExperimentAnnotation]). It reports
// whether it stored that record. A failure to store the record does not undo
// the publication, but the draft then cannot update the experiment again.
func (b *builderAPI) writeExperiment(
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
	// started or changed since. Thus read it again and rebuild the update from
	// it, so a stale status or spec is never written back.
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

	// Run the configure stage of the apps on the new spec, as every other
	// update of an experiment spec does (the configs API, the workflow API and
	// the CLI). If it fails, put the experiment back as it was. Then the failed
	// stage wrote nothing, and a new publish retries all of it.
	if err := b.publish.reconfigureExperiment(plan.name); err != nil {
		b.restoreExperiment(current)

		return false, fmt.Errorf("configuring experiment %s: %w", plan.name, err)
	}

	return b.recordExperimentPublication(plan.name, publication), nil
}

// restoreExperiment puts an experiment back as it was before an update whose
// configure stage failed. The experiment lock does not stop the CLI from
// starting the experiment meanwhile, which is also why the configure stage
// can fail. Thus it reads the experiment again first. It does not change a
// running experiment. An experiment that is not running keeps its current
// status.
func (b *builderAPI) restoreExperiment(previous *store.Config) {
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
// still locked, which draft and document published it. It also stores the
// digest that the experiment has after its configure stage ran (see
// [builderExperimentAnnotation]). It reports whether it stored the record,
// and logs the reason when it did not.
func (b *builderAPI) recordExperimentPublication(name string, publication builderExperimentPublication) bool {
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

func (b *builderAPI) configIfExists(kind, name string) (*store.Config, bool, error) {
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

// requirePublishAction refuses a create of a config of kind (Topology or
// Experiment) that exists, and an update of one that does not exist. It
// does not refuse when an earlier attempt already applied the publication.
// The code of the refusal names the kind and what the caller did not expect.
func requirePublishAction(kind string, target builderPublishTarget, exists, applied bool) error {
	taken, absent := bdoc.CodePublishTopologyExists, bdoc.CodePublishTopologyMissing
	if kind == kindExperiment {
		taken, absent = bdoc.CodePublishExperimentExists, bdoc.CodePublishExperimentMissing
	}

	switch {
	case applied:
		return nil
	case target.Action == builderPublishActionCreate && exists:
		return weberror.NewWebError(nil, "config %s already exists; choose update explicitly", target.Name).
			SetStatus(http.StatusConflict).WithCode(string(taken))
	case target.Action == builderPublishActionUpdate && !exists:
		return weberror.NewWebError(nil, "config %s does not exist; choose create explicitly", target.Name).
			SetStatus(http.StatusConflict).WithCode(string(absent))
	}

	return nil
}

func validateBuilderPublishRequest(request builderPublishRequest) error {
	if !request.Mode.Valid() {
		return weberror.NewWebError(nil, "unknown publish mode %q", request.Mode).
			SetStatus(http.StatusBadRequest)
	}

	if err := validatePublishTarget(builderSourceTopology, request.Topology); err != nil {
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

		if err := validatePublishTarget(builderSourceExperiment, *request.Experiment); err != nil {
			return err
		}

		if request.Scenario != nil && !validPublishName(builderSourceScenario, request.Scenario.Name) {
			return invalidPublishTarget(builderSourceScenario, request.Scenario.Name)
		}
	}

	return nil
}

// validPublishName reports whether name is a config name a publication may
// name a config of kind by.
func validPublishName(kind, name string) bool {
	return name != "" && config.NameRegex.MatchString(name) && store.ConfigFullName(kind, name) != ""
}

func validatePublishTarget(kind string, target builderPublishTarget) error {
	if !validPublishName(kind, target.Name) {
		return invalidPublishTarget(kind, target.Name)
	}

	if target.Action != builderPublishActionCreate && target.Action != builderPublishActionUpdate {
		return weberror.NewWebError(nil, "unknown %s publish action %q", kind, target.Action).
			SetStatus(http.StatusBadRequest)
	}

	return nil
}

func invalidPublishTarget(kind, name string) error {
	return weberror.NewWebError(nil, "%s target %q is not a valid config name", kind, name).
		SetStatus(http.StatusBadRequest).WithCode(string(bdoc.CodePublishTargetInvalid))
}

// addTopologyAnnotation adds topology to value, the comma-separated
// "topology" annotation of a scenario, when value does not name it yet (see
// [hasTopologyAnnotation]). Apart from the added name, the value stays the
// same byte for byte. It appends the name after a comma, or uses the name
// as the whole annotation when value is empty. It does not trim, reorder or
// de-duplicate the names that value already has.
func addTopologyAnnotation(value, topology string) string {
	switch {
	case hasTopologyAnnotation(value, topology):
		return value
	case value == "":
		return topology
	}

	return value + "," + topology
}

// hasTopologyAnnotation reports whether value, a scenario's comma-separated
// "topology" annotation, names topology: one of its names, trimmed, is
// exactly topology. A name that only contains it does not count.
func hasTopologyAnnotation(value, topology string) bool {
	return bapi.HasTopologyAnnotation(value, topology)
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
		ifMatch == bapi.RevisionETag(state.Revision) &&
		state.Mode == request.Mode &&
		state.TopologyTarget == request.Topology.Name &&
		state.TopologyAction == bapi.TopologyAction(request.Topology.Action) &&
		state.ExperimentTarget == targetName(request.Experiment) &&
		state.ScenarioTarget == request.scenarioName() &&
		state.SnapshotID == current.ID &&
		state.Digest == current.Digest
}

// writePublishRetry answers a publication the draft already completed, with
// every stage skipped. The draft's current snapshot is the one it published
// (see [builderPublishRetry]), so its document says whether the publication
// had a scenario stage.
func (b *builderAPI) writePublishRetry(
	ctx context.Context,
	w http.ResponseWriter,
	actor builderActor,
	meta *bapi.DraftMetadata,
	request builderPublishRequest,
) error {
	snapshot, err := b.drafts.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		return builderWebError(err, "unable to load the current draft snapshot")
	}

	document, err := snapshot.Decode()
	if err != nil {
		return builderWebError(err, "unable to decode the current draft snapshot")
	}

	stages := []builderPublishStage{
		{Name: builderPublishStageDocument, Status: bapi.PublishSkipped, Message: "", Config: ""},
		{
			Name: builderPublishStageTopology, Status: bapi.PublishSkipped, Message: "",
			Config: builderKindTopology + "/" + request.Topology.Name,
		},
	}

	if len(document.Scenarios) > 0 {
		stages = append(stages, builderPublishStage{
			Name: builderPublishStageScenario, Status: bapi.PublishSkipped, Message: "",
			Config: builderScenarioStageConfig(document.Scenarios),
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

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), builderPublishResponse{
		Status:     bapi.PublishSucceeded,
		Stages:     stages,
		Warnings:   []bdoc.Issue{bdoc.NewIssue(bdoc.CodePublishRetryComplete, "", "identical publication was already complete")},
		Errors:     []bdoc.Issue{},
		Topology:   &request.Topology,
		Scenario:   request.Scenario,
		Experiment: request.Experiment,
		Draft:      b.draftResponse(actor, meta),
	})
}

func (b *builderAPI) writePublishPartial(
	w http.ResponseWriter,
	actor builderActor,
	meta *bapi.DraftMetadata,
	response *builderPublishResponse,
	stage string,
	cause error,
) error {
	message, code := stage+" publication failed", bdoc.CodePublishStageFailed

	switch {
	case errors.Is(cause, errBuilderExperimentRunning):
		message += ": " + errBuilderExperimentRunning.Error()
		code = bdoc.CodePublishExperimentRunning
	case errors.Is(cause, store.ErrNoSpace):
		message += ": " + store.ErrNoSpace.Error()
		code = bdoc.CodeServerStorageFull
	}

	response.Status = bapi.PublishPartial
	response.Errors = append(response.Errors, bdoc.NewIssue(code, "", message))
	response.Stages = append(response.Stages, builderPublishStage{
		Name: stage, Status: bapi.PublishFailed, Message: message, Config: "",
	})
	response.Draft = b.draftResponse(actor, meta)

	plog.Error(
		plog.TypeSystem,
		"builder publication stage failed",
		"stage", stage,
		"draft", meta.ID,
		"err", cause,
	)

	status := http.StatusInternalServerError

	switch {
	case errors.Is(cause, store.ErrNoSpace):
		status = http.StatusInsufficientStorage
	case errors.Is(cause, store.ErrExist) || errors.Is(cause, store.ErrNotExist) ||
		errors.Is(cause, bapi.ErrConflict) || errors.Is(cause, errBuilderExperimentRunning):
		status = http.StatusConflict
	}

	return builderWriteJSON(w, status, meta.ETag(), response)
}
