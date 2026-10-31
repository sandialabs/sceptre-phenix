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
	"unicode"
	"unicode/utf8"

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

// lockBuilderPublishing holds [builderPublishLock] while a caller outside Builder deletes or renames a Topology config.
// Only the time of a document tells it apart from one that a publication in flight just stored, so the lock is needed.
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
var errBuilderExperimentRunning = errors.New("phenix cannot update a running experiment")

// builderExperimentAnnotation records the draft and document that last published an experiment, and its digest after configure.
// A draft updates the experiment only while it still has that digest.
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
	// RemovedHostPins are the host pins that the update of the experiment
	// removed (see [updatedExperimentConfig]). It is left out when there is
	// none, or when the caller may not see the experiment's schedule.
	RemovedHostPins []bapi.HostPin       `json:"removedHostPins,omitempty"`
	Draft           builderDraftResponse `json:"draft"`
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

// publishDraft is the only Builder handler that creates or updates phenix configs. The bytes come from the draft snapshot.
//
//nolint:funlen // ordered publication stages and partial results are kept together
func (b *builderAPI) publishDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPublishDraft")

	actor, err := builderAuthorize(r, "publishing a Builder draft")
	if err != nil {
		return err
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

	meta, err := b.pathDraft(r, false)
	if err != nil {
		return err
	}

	if ifMatch != meta.ETag() {
		if builderPublishRetry(meta, request, ifMatch) {
			return b.writePublishRetry(r.Context(), w, actor, meta, request)
		}

		return builderCheckIfMatch(w, ifMatch, meta)
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
		Status:          bapi.PublishSucceeded,
		Stages:          []builderPublishStage{},
		Warnings:        warnings,
		Errors:          []bdoc.Issue{},
		Topology:        &request.Topology,
		Scenario:        request.Scenario,
		Experiment:      request.Experiment,
		RemovedHostPins: nil,
		Draft:           newBuilderDraftResponse(meta),
	}

	published, err := b.drafts.PutPublishedDocument(r.Context(), bapi.PutPublishedDocumentRequest{
		Target:     request.Topology.Name,
		Kind:       builderKindTopology,
		Actor:      actor.user,
		Document:   snapshot.Data,
		DraftID:    meta.ID,
		SnapshotID: snapshot.Manifest.ID,
	})
	if err != nil {
		return builderWebError(err, "unable to store the published Builder document")
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDocument, Status: "created", Message: "phenix stored the immutable Builder document", Config: "",
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
		return b.writePublishPartial(w, actor, meta, &response, builderPublishStageDraft, err)
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageDraft, Status: "ok", Message: "", Config: "",
	})
	response.Draft = b.draftResponse(actor, updated)

	if _, err := b.drafts.DeleteSupersededDocuments(r.Context(), request.Topology.Name, published.ID); err != nil {
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishCleanupFailed, "",
			"the publication succeeded, but phenix could not remove the superseded Builder documents"))
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
// warning for the host pins that an update removed, and for anything that
// did not complete after the experiment was stored. It returns the error of
// a stage that failed.
func (b *builderAPI) publishExperimentStage(
	ctx context.Context,
	plan builderPublishExperimentPlan,
	topology *store.Config,
	aliases map[string]int,
	scenario string,
	publication builderExperimentPublication,
	response *builderPublishResponse,
) error {
	recorded, removed, err := b.writeExperiment(ctx, plan, topology, aliases, scenario, publication)
	if err != nil {
		return err
	}

	if len(removed) > 0 {
		response.Warnings = append(response.Warnings, hostPinsWarning(removed, plan.pinsVisible))

		if plan.pinsVisible {
			response.RemovedHostPins = removed
		}
	}

	if !recorded {
		response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishExperimentUnrecorded, "",
			"phenix stored the experiment, but did not record which draft published it. Thus this draft cannot update it again"))
	}

	response.Stages = append(response.Stages, builderPublishStage{
		Name: builderPublishStageExperiment, Status: plan.status(), Message: "",
		Config: kindExperiment + "/" + plan.name,
	})

	if !plan.applied {
		if err := b.publish.broadcastExperiment(plan.name, plan.action); err != nil {
			response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishBroadcastFailed, "",
				"phenix stored the experiment, but could not broadcast its live update"))
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
	// experiment config as it is when it is written, and the host pins that
	// the update removes.
	rebuild func(*store.Config) (*store.Config, []bapi.HostPin, error)
	// removedPins are the host pins that the update removes from existing.
	removedPins []bapi.HostPin
	// pinsVisible reports whether the caller may see the experiment's
	// schedule, and thus which host pins the update removes.
	pinsVisible bool
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

	return fmt.Sprintf("added topology %s to %s, and %s already %s it",
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
		return nil, weberror.NewWebError(err, "Builder document cannot be projected").
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
			ctx,
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

		experimentPlan.pinsVisible = actor.role.Allowed("experiments/schedule", "get", request.Experiment.Name)
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

// publishProjectionRefusal refuses a document whose topology projection cannot be published, with [bdoc.CodePublishBlocked].
// Clients show the message, so it names the problems itself. A different schema refusal has [bdoc.CodePublishTopologyInvalid].
func publishProjectionRefusal(topologyName string, err error) error {
	problems, named := projectionProblems(err)
	if !named {
		return weberror.NewWebError(err, "Builder document cannot be published as topology %s", topologyName).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishTopologyInvalid))
	}

	return weberror.NewWebError(err, "phenix cannot publish topology %s: %s", topologyName, problems).
		SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishBlocked))
}

// projectionProblems names, as clients show them, the first few problems that err reports, then how many more.
// It reports false for an error that names none.
func projectionProblems(err error) (string, bool) {
	const listed = 3

	var (
		vlanErr     *bdoc.InterfaceVLANError
		addressErr  *bdoc.InterfaceAddressError
		hostnameErr *bdoc.NodeHostnameError
		setupErr    *bdoc.DeviceSetupError
		problems    []string
		// What the problems past the first few are, after their number.
		one, many string
	)

	switch {
	case errors.As(err, &vlanErr):
		problems, one, many = vlanErr.Problems, "interface has no VLAN", "interfaces have no VLAN"
	case errors.As(err, &addressErr):
		problems, one, many = addressErr.Problems, "duplicate address", "duplicate addresses"
	case errors.As(err, &hostnameErr):
		problems, one, many = hostnameErr.Problems, "hostname phenix refuses", "hostnames phenix refuses"
	case errors.As(err, &setupErr):
		problems, one, many = setupErr.Problems, "setting stops the experiment from starting",
			"settings stop the experiment from starting"
	}

	if len(problems) == 0 {
		return "", false
	}

	shown := slices.Clone(problems[:min(len(problems), listed)])

	switch more := len(problems) - listed; {
	case more == 1:
		shown = append(shown, "1 more "+one)
	case more > 1:
		shown = append(shown, fmt.Sprintf("%d more %s", more, many))
	}

	return problemSentences(shown), true
}

// problemSentences joins problems as sentences: each starts with a capital letter, unless it starts with phenix.
func problemSentences(problems []string) string {
	sentences := make([]string, len(problems))

	for i, problem := range problems {
		problem = strings.TrimSuffix(strings.TrimSpace(problem), ".")
		if first, size := utf8.DecodeRuneInString(problem); size > 0 && !strings.HasPrefix(problem, "phenix") {
			problem = string(unicode.ToUpper(first)) + problem[size:]
		}

		sentences[i] = problem
	}

	return strings.Join(sentences, ". ")
}

// mergedIncludesRefusal is why an experiment update may not merge the included topologies.
// The update merges them itself (see experimentTopology), so it reads them only as import does.
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
			"phenix cannot update experiment %s: it cannot merge included topology %s: %v",
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
		nil, "phenix cannot publish experiment %s: %s", experimentName, problemSentences(reasons),
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
			"phenix cannot publish topology %s: node %s is in this topology and in its included topology %s. "+
				"phenix rejects duplicate hostnames. Rename the node here or in %s",
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
			"phenix cannot publish topology %s: nodes %s and %s are in this topology and in its included topologies. "+
				"phenix rejects duplicate hostnames. Rename them here or in those topologies",
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
	if !builderConfigAllowed(
		actor.role,
		builderPublishVerb(request.Topology.Action),
		store.ConfigFullName(builderKindTopology, request.Topology.Name),
	) {
		return builderForbidden(actor, "publishing topology "+request.Topology.Name)
	}

	if request.Experiment != nil &&
		(!builderExperimentAllowed(actor.role, request.Experiment.Action, request.Experiment.Name) ||
			!builderConfigAllowed(
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

// checkSourceFreshness refuses a draft whose source config changed after the draft was imported.
// A source is fresh when only a publication of this draft changed it. A deleted source publishes as a new diagram.
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
		return weberror.NewWebError(nil, "phenix cannot publish a Builder source of kind %s", source.Kind).
			SetStatus(http.StatusUnprocessableEntity)
	}

	fullName := store.ConfigFullName(kind, source.Name)
	if !builderConfigAllowed(actor.role, builderVerbGet, fullName) ||
		!builderSourceGetAllowed(actor.role, kind, source.Name) {
		return builderForbidden(actor, "checking Builder source "+fullName)
	}

	current, err := b.getConfig(fullName)
	if err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil
		}

		return weberror.NewWebError(err, "unable to reload Builder source %s", fullName).
			SetStatus(http.StatusInternalServerError)
	}

	digest, err := bdoc.ImportDigest(*current)
	if err != nil {
		return weberror.NewWebError(err, "unable to calculate the digest of Builder source %s", fullName).
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
		return sourceChangedRefusal(source)
	}

	return nil
}

// sourceChangedRefusal refuses a draft whose source config changed after the diagram was made from it.
// The message suits a draft imported from the config and one uploaded as a Builder file that names it.
func sourceChangedRefusal(source *bdoc.Source) error {
	kind, again := builderSourceTopology, "import it again or open its published diagram"
	if source.Kind == bdoc.SourceKindExperiment {
		kind, again = builderSourceExperiment, "import it again"
	}

	return weberror.NewWebError(nil,
		"this diagram comes from %[1]s %[2]s. That %[1]s changed on the server after Builder made the diagram. "+
			"Publish refuses this diagram under any name. "+
			"To continue, make a new draft of %[1]s %[2]s: %[3]s. Then copy your changes to the new draft",
		kind, source.Name, again,
	).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishSourceChanged))
}

// sourceHoldsDraftPublication reports whether only a publish of this draft, or of its origin document, changed a source.
// An experiment is compared by its recorded digest, because the configure stage rewrites its spec.
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

// experimentHoldsDraftPublication reports whether an experiment records this draft, or its document, as its last publisher.
// It also reports whether the experiment still has the digest recorded then, so nothing else changed it.
func experimentHoldsDraftPublication(meta *bapi.DraftMetadata, exp *store.Config) (bool, bool, error) {
	record, ok := experimentPublicationRecord(exp)
	if !ok {
		return false, false, nil
	}

	owned := record.DraftID == meta.ID || draftOwnsDocument(meta, record.DocumentID)
	if !owned {
		return false, false, nil
	}

	digest, err := bdoc.SourceDigest(*exp)
	if err != nil {
		return true, false, weberror.NewWebError(err, "unable to calculate the digest of experiment %s", exp.Metadata.Name).
			SetStatus(http.StatusInternalServerError)
	}

	return true, digest == record.Digest, nil
}

// experimentPublicationRecord returns what an experiment records of the
// publication that last wrote it (see [builderExperimentAnnotation]). It
// reports false when the experiment records none, or a record it cannot
// read, which vouches for nothing.
func experimentPublicationRecord(exp *store.Config) (builderExperimentPublication, bool) {
	var record builderExperimentPublication

	value, ok := exp.Metadata.Annotations[builderExperimentAnnotation]
	if !ok || json.Unmarshal([]byte(value), &record) != nil {
		return record, false
	}

	return record, true
}

// experimentPublishedDocument returns the published document that last wrote
// the experiment, or nil when the experiment does not record one, or it
// cannot be read. A later publication of the same topology may have removed
// that document.
func (b *builderAPI) experimentPublishedDocument(ctx context.Context, exp *store.Config) *bdoc.Document {
	record, ok := experimentPublicationRecord(exp)
	if !ok || record.DocumentID == "" {
		return nil
	}

	_, data, err := b.drafts.GetPublishedDocumentData(ctx, record.DocumentID)
	if err != nil {
		plog.Debug(plog.TypeSystem, "reading the document that published an experiment",
			"experiment", exp.Metadata.Name, "document", record.DocumentID, "err", err)

		return nil
	}

	document, err := bdoc.Decode(data)
	if err != nil {
		plog.Debug(plog.TypeSystem, "decoding the document that published an experiment",
			"experiment", exp.Metadata.Name, "document", record.DocumentID, "err", err)

		return nil
	}

	return document
}

// publishedTopologyReference is the document reference that a publication stores on its topology.
// It keeps the path of a Builder file that the topology already named. Publish never writes that file.
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
			"Download the diagram and replace the file with it, so that the file and the topology stay the same.",
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

// openedTopologyFile returns the document of the Builder file that a new draft's source token names.
// It authorizes as GET /builder/topologies/{topology}/document does, and refuses with 409 a file that changed.
func (b *builderAPI) openedTopologyFile(
	ctx context.Context,
	actor builderActor,
	token string,
) (builderTopologyDocument, error) {
	var none builderTopologyDocument

	topology, digest, ok := openedBuilderFile(token)
	if !ok {
		return none, builderNotFound("Builder document of topology", strings.TrimPrefix(token, builderFileTokenPrefix))
	}

	_, resolved, err := b.readableTopologyDocument(ctx, actor, topology)

	switch {
	case err != nil:
		return none, err
	case resolved.record != nil:
		return none, weberror.NewWebError(
			nil,
			"The server no longer reads topology %s from its Builder file. Open its diagram again.",
			topology,
		).SetStatus(http.StatusConflict)
	case resolved.digest != digest:
		return none, weberror.NewWebError(
			nil,
			"The Builder file of topology %s changed after Builder opened it. Open its diagram again.",
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

// forkOrigin returns the source token and forked publication of a new draft that forks the draft forkOf.
// The fork may update what that draft published or was opened from, but not what that draft publishes later.
func (b *builderAPI) forkOrigin(r *http.Request, forkOf string) (string, *bapi.ForkedPublication, error) {
	meta, err := b.namedDraft(r.Context(), forkOf, false)
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

// holdsDraftDocument reports whether a topology holds a document of this draft, and whether its spec is still that projection.
// A document that can no longer be read or projected counts as changed, and is the draft's own only by the reference.
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
		return held, builderWebError(err, "unable to read the Builder document of topology %s", name)
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

// topologyUpdateRefusal refuses an update of a topology that this draft may not update.
// A draft updates a topology that holds its own unchanged document, or the topology it was loaded from.
// A topology that the legacy Builder drew is updated only by a draft imported from it (see [topologyUpdateMatchesSource]).
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
			nil, "topology %s is not what its Builder file publishes. Thus this draft cannot update it", name,
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
			nil, "topology %s or its Builder file changed after Builder opened this draft from the file", name,
		).SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyFileChanged))
	}

	if topologyUpdateMatchesSource(meta, document, existing) {
		return nil
	}

	return weberror.NewWebError(nil, "topology %s is not the source of this draft", name).
		SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishTopologyNotSource))
}

// topologyUpdateMatchesSource reports whether the document was imported from the topology, or from an experiment of it.
// A topology with a legacy diagram matches only a direct import, because that import converted the diagram.
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

// topologyPublicationApplied reports whether a topology already holds the publication with this
// digest, so a publish writes nothing.
// A reference that also names a Builder file holds the publication only after the document is stored.
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
		return false, builderWebError(err, "unable to read the Builder document of topology %s", name)
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

// preflightScenarios plans the scenario stage of a publication for each scenario that the document lists.
// It refuses a scenario that the caller may not read as one that does not exist, so its existence is not disclosed.
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
			if !builderConfigAllowed(actor.role, builderVerbUpdate, existing.FullName()) {
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
		!builderConfigAllowed(actor.role, builderVerbGet, full) ||
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
					"phenix added topology %s to %s before the scenario stage failed",
					topologyName, builderScenarioList(written),
				)))
			}

			return err
		}

		written = append(written, stored.Metadata.Name)

		if err := b.publish.broadcastConfig(stored, change.action); err != nil {
			response.Warnings = append(response.Warnings, bdoc.NewIssue(bdoc.CodePublishBroadcastFailed, "",
				fmt.Sprintf("phenix stored scenario %s, but could not broadcast its live update", stored.Metadata.Name)))
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
	ctx context.Context,
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
		action: target.Action, name: target.Name, applied: applied, existing: existing,
		rebuild: nil, removedPins: nil, pinsVisible: false,
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
		return nil, weberror.NewWebError(err, "phenix cannot decode experiment %s", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}
	if current.Running() {
		return nil, weberror.NewWebError(nil, "phenix cannot update running experiment %s", target.Name).
			SetStatus(http.StatusConflict).WithCode(string(bdoc.CodePublishExperimentRunning))
	}

	if topologyErr != nil {
		return nil, weberror.NewWebError(topologyErr, "phenix cannot update experiment %s", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	plan.rebuild = func(current *store.Config) (*store.Config, []bapi.HostPin, error) {
		updated, removed, err := updatedExperimentConfig(
			current,
			topologySpec,
			projection,
			topologyName,
			scenarioName,
			scenarioConfig,
		)
		if err != nil || len(removed) == 0 {
			return updated, removed, err
		}

		// Read the earlier document only when a pin is removed. Each edit sends a dry run.
		renames := bapi.NewDeviceRenames(b.experimentPublishedDocument(ctx, current), document)
		for i := range removed {
			removed[i].RenamedTo = renames.RenamedTo(removed[i].Hostname)
		}

		return updated, removed, err
	}

	updated, removed, err := plan.rebuild(existing)
	if err != nil {
		return nil, weberror.NewWebError(err, "phenix cannot update experiment %s", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	if err := types.ValidateConfigSpec(*updated); err != nil {
		return nil, weberror.NewWebError(err, "experiment %s is not valid", target.Name).
			SetStatus(http.StatusUnprocessableEntity).WithCode(string(bdoc.CodePublishExperimentInvalid))
	}

	plan.removedPins = removed

	return plan, nil
}

// experimentCreateRefusal refuses, before anything is written, what experiment.Create would refuse only after the writes.
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

// experimentUpdateRefusal refuses an update of an experiment that this draft may not update.
// A draft updates an experiment that it, or its origin document, published and nothing changed since, or its import source.
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

	return weberror.NewWebError(nil, "experiment %s is not the source of this draft", name).
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

// experimentTopology returns the topology that an experiment holds for the projection.
// The projection only names included topologies, so it merges them as phenix does at experiment create.
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

// updatedExperimentConfig is the experiment config that an update writes, merged as phenix merges one at create.
// It removes and returns the host pins of removed VMs, because a stale pin still counts toward round-robin host load.
func updatedExperimentConfig(
	existing *store.Config,
	topologySpec ifaces.TopologySpec,
	projection *bdoc.Topology,
	topologyName, scenarioName string,
	scenarioConfig *store.Config,
) (*store.Config, []bapi.HostPin, error) {
	exp, err := types.DecodeExperimentFromConfig(*existing)
	if err != nil {
		return nil, nil, err
	}

	exp.Spec.SetTopology(topologySpec)
	exp.Spec.VLANs().SetAliases(projection.VLANAliases)

	kept, removed := bapi.PruneHostPins(exp.Spec.Schedules(), nodeHostnames(topologySpec))
	if len(removed) > 0 {
		exp.Spec.SetSchedule(kept)
	}

	if scenarioConfig == nil {
		exp.Spec.SetScenario(nil)
	} else {
		scenario, scenarioErr := types.MakeCustomScenarioFromConfig(*scenarioConfig, nil)
		if scenarioErr != nil {
			return nil, nil, scenarioErr
		}

		if mergeErr := types.MergeScenariosForTopology(scenario, topologyName); mergeErr != nil {
			return nil, nil, mergeErr
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

	return updated, removed, nil
}

// nodeHostnames returns the hostname of each node of a topology.
func nodeHostnames(topology ifaces.TopologySpec) []string {
	nodes := topology.Nodes()
	hostnames := make([]string, 0, len(nodes))

	for _, node := range nodes {
		hostnames = append(hostnames, node.General().Hostname())
	}

	return hostnames
}

// hostPinsWarning is the warning of an experiment update that removed host
// pins. It names each pin, its host, and the new hostname of a renamed
// device only when the caller may see the experiment's schedule. Otherwise
// it gives only their number.
func hostPinsWarning(removed []bapi.HostPin, visible bool) bdoc.Issue {
	if !visible {
		if len(removed) == 1 {
			return bdoc.NewIssue(bdoc.CodePublishExperimentPins, "",
				"Publish removed the host pin of 1 VM that is no longer in the topology.")
		}

		return bdoc.NewIssue(bdoc.CodePublishExperimentPins, "", fmt.Sprintf(
			"Publish removed the host pins of %d VMs that are no longer in the topology.", len(removed)))
	}

	pins := make([]string, len(removed))

	for i, pin := range removed {
		if pin.RenamedTo == "" {
			pins[i] = fmt.Sprintf("%s (on %s)", pin.Hostname, pin.Host)
		} else {
			pins[i] = fmt.Sprintf("%s (on %s, renamed to %s)", pin.Hostname, pin.Host, pin.RenamedTo)
		}
	}

	prefix := "Publish removed the host pins of VMs that are no longer in the topology: "
	if len(removed) == 1 {
		prefix = "Publish removed the host pin of a VM that is no longer in the topology: "
	}

	return bdoc.NewIssue(bdoc.CodePublishExperimentPins, "", prefix+strings.Join(pins, ", ")+".")
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
				"phenix stored the "+stage+", but could not broadcast its live update"))
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

// writeExperiment creates or updates the experiment under the experiment lock, then records its publisher on it.
// A failure to store the record does not undo the publication, but the draft then cannot update the experiment again.
func (b *builderAPI) writeExperiment(
	ctx context.Context,
	plan builderPublishExperimentPlan,
	topology *store.Config,
	aliases map[string]int,
	scenario string,
	publication builderExperimentPublication,
) (bool, []bapi.HostPin, error) {
	if plan.applied {
		return true, nil, nil
	}

	if err := b.publish.lockExperiment(plan.name, plan.action); err != nil {
		return false, nil, err
	}
	defer b.publish.unlockExperiment(plan.name)

	if plan.action == builderPublishActionCreate {
		if err := b.publish.createExperiment(ctx, plan.name, topology.Metadata.Name, scenario, aliases); err != nil {
			return false, nil, err
		}

		return b.recordExperimentPublication(plan.name, publication), nil, nil
	}

	// Preflight read the experiment before the lock, and it may have been
	// started or changed since. Thus read it again and rebuild the update from
	// it, so a stale status or spec is never written back.
	current, err := b.getConfig(store.ConfigFullName(kindExperiment, plan.name))
	if err != nil {
		return false, nil, fmt.Errorf("reloading experiment %s: %w", plan.name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*current)
	if err != nil {
		return false, nil, fmt.Errorf("decoding experiment %s: %w", plan.name, err)
	}

	if exp.Running() {
		return false, nil, fmt.Errorf("experiment %s: %w", plan.name, errBuilderExperimentRunning)
	}

	updated, removed, err := plan.rebuild(current)
	if err != nil {
		return false, nil, fmt.Errorf("rebuilding experiment %s: %w", plan.name, err)
	}

	if err := b.publish.updateConfig(current.FullName(), updated); err != nil {
		return false, nil, err
	}

	// Run the configure stage of the apps on the new spec, as every other
	// update of an experiment spec does (the configs API, the workflow API and
	// the CLI). If it fails, put the experiment back as it was. Then the failed
	// stage wrote nothing, and a new publish retries all of it.
	if err := b.publish.reconfigureExperiment(plan.name); err != nil {
		b.restoreExperiment(current)

		return false, nil, fmt.Errorf("configuring experiment %s: %w", plan.name, err)
	}

	return b.recordExperimentPublication(plan.name, publication), removed, nil
}

// restoreExperiment puts an experiment back as it was before an update whose configure stage failed.
// The lock does not stop the CLI from starting it, so it reads the experiment again and never changes a running one.
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
		return weberror.NewWebError(nil, "config %s already exists. Choose update explicitly", target.Name).
			SetStatus(http.StatusConflict).WithCode(string(taken))
	case target.Action == builderPublishActionUpdate && !exists:
		return weberror.NewWebError(nil, "config %s does not exist. Choose create explicitly", target.Name).
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

// addTopologyAnnotation appends topology to the comma-separated "topology" annotation value when it is not there yet.
// The rest of value stays byte for byte, with no trim, reorder or de-duplication.
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

	stored, err := initializedExperiment(existing)
	if err != nil {
		return false
	}

	// Init adds aliases, so it gets a copy of those of the projection.
	projected := *projection
	projected.VLANAliases = maps.Clone(projection.VLANAliases)

	updated, _, err := updatedExperimentConfig(existing, topologySpec, &projected, topologyName, scenarioName, nil)
	if err != nil {
		return false
	}

	want, err := initializedExperiment(updated)
	if err != nil {
		return false
	}

	return reflect.DeepEqual(stored.Spec.Topology(), want.Spec.Topology()) &&
		maps.Equal(stored.Spec.VLANs().Aliases(), want.Spec.VLANs().Aliases())
}

// initializedExperiment decodes the experiment config and runs Init on it, as
// phenix does when it stores an experiment. Thus it gives the defaults that
// a projection does not set.
func initializedExperiment(config *store.Config) (*types.Experiment, error) {
	exp, err := types.DecodeExperimentFromConfig(*config)
	if err != nil {
		return nil, err
	}

	if err := exp.Spec.Init(); err != nil {
		return nil, err
	}

	return exp, nil
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
		Status:          bapi.PublishSucceeded,
		Stages:          stages,
		Warnings:        []bdoc.Issue{bdoc.NewIssue(bdoc.CodePublishRetryComplete, "", "identical publication was already complete")},
		Errors:          []bdoc.Issue{},
		Topology:        &request.Topology,
		Scenario:        request.Scenario,
		Experiment:      request.Experiment,
		RemovedHostPins: nil,
		Draft:           b.draftResponse(actor, meta),
	})
}

// builderPublishRetryAdvice is the message of [bdoc.CodePublishRetryAvailable].
const builderPublishRetryAdvice = "Publish again with the same choices to finish. The server skips the steps that are already done."

// writePublishPartial answers a publication that failed at stage after the
// stages before it were written. Unless the draft changed meanwhile, the
// answer says that the same request, sent again with the same If-Match,
// finishes it: the stages already done are skipped.
func (b *builderAPI) writePublishPartial(
	w http.ResponseWriter,
	actor builderActor,
	meta *bapi.DraftMetadata,
	response *builderPublishResponse,
	stage string,
	cause error,
) error {
	message, code := stage+" publication failed", bdoc.CodePublishStageFailed

	if errors.Is(cause, errBuilderExperimentRunning) {
		message += ": " + errBuilderExperimentRunning.Error()
		code = bdoc.CodePublishExperimentRunning
	}

	response.Status = bapi.PublishPartial
	response.Errors = append(response.Errors, bdoc.NewIssue(code, "", message))

	// A draft that changed meanwhile is published again from the editor,
	// with its new entity tag.
	if stage != builderPublishStageDraft || !errors.Is(cause, bapi.ErrConflict) {
		response.Warnings = append(response.Warnings,
			bdoc.NewIssue(bdoc.CodePublishRetryAvailable, "", builderPublishRetryAdvice))
	}
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

	if errors.Is(cause, store.ErrExist) || errors.Is(cause, store.ErrNotExist) ||
		errors.Is(cause, bapi.ErrConflict) || errors.Is(cause, errBuilderExperimentRunning) {
		status = http.StatusConflict
	}

	return builderWriteJSON(w, status, meta.ETag(), response)
}
