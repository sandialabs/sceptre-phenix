package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderPublishPreviewStatus is the status of a dry run's answer.
const builderPublishPreviewStatus = "preview"

// builderPublishPreview answers a dry run of a publication. It tells what a
// publish of the draft to the request targets would change (see
// [bapi.DescribePublishChanges]), what the publication would warn of, and
// why it would be refused. Each warning and refusal is an issue with its
// code. Changes is nil when the publication would be refused.
type builderPublishPreview struct {
	Status   string               `json:"status"`
	Changes  *bapi.PublishChanges `json:"changes"`
	Warnings []bdoc.Issue         `json:"warnings"`
	Errors   []bdoc.Issue         `json:"errors"`
}

// previewPublish answers a dry run of a publication, and writes nothing.
// A refusal that a publication would answer with 400, 409 or 422 is listed in its errors with 200.
// It takes no If-Match and no publish lock, so its result may change before a publication is sent.
func (b *builderAPI) previewPublish(
	w http.ResponseWriter,
	r *http.Request,
	actor builderActor,
	request builderPublishRequest,
) error {
	meta, err := b.pathDraft(r, false)
	if err != nil {
		return err
	}

	changes, warnings, err := b.publishPreview(r.Context(), actor, meta, request)

	preview := builderPublishPreview{
		Status:   builderPublishPreviewStatus,
		Changes:  changes,
		Warnings: append([]bdoc.Issue{}, warnings...),
		Errors:   []bdoc.Issue{},
	}

	if err != nil {
		issues, refused := builderPreviewRefusal(err)
		if !refused {
			return err
		}

		preview.Changes = nil
		preview.Errors = issues
	}

	return builderWriteJSON(w, http.StatusOK, meta.ETag(), preview)
}

// publishPreview makes the checks of a publication of the current snapshot
// of the draft. It returns what the publication would change and warn of,
// or the error that would refuse it.
func (b *builderAPI) publishPreview(
	ctx context.Context,
	actor builderActor,
	meta *bapi.DraftMetadata,
	request builderPublishRequest,
) (*bapi.PublishChanges, []bdoc.Issue, error) {
	if err := validateBuilderPublishRequest(request); err != nil {
		return nil, nil, err
	}

	snapshot, err := b.drafts.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		return nil, nil, builderWebError(err, "unable to load the current draft snapshot")
	}

	document, err := snapshot.Decode()
	if err != nil {
		return nil, nil, builderWebError(err, "unable to decode the current draft snapshot")
	}

	topology, projection, warnings, err := document.PublishTopology(request.Topology.Name)
	if err != nil {
		return nil, warnings, publishProjectionRefusal(request.Topology.Name, err)
	}

	plan, err := b.preflightPublish(ctx, actor, meta, snapshot, document, topology, projection, request)
	if err != nil {
		return nil, warnings, err
	}

	warnings = append(warnings, publishPreviewWarnings(request.Topology.Name, topology, plan)...)

	state := bapi.PublishState{
		TopologyName:   request.Topology.Name,
		Spec:           projection.Spec,
		VLANAliases:    projection.VLANAliases,
		StoredTopology: plan.topology.existing,
		TopologyHeld:   plan.topology.applied,
		Scenarios:      nil,
		Experiment:     nil,
		ServerImages:   b.serverImages(ctx, actor, projection.Spec),
	}

	if plan.scenarios != nil {
		state.Scenarios = plan.scenarios.stored
	}

	if plan.experiment != nil {
		state.Experiment = &bapi.ExperimentState{
			Name: plan.experiment.name, Stored: plan.experiment.existing, Held: plan.experiment.applied,
			RemovedHostPins: nil,
		}

		if plan.experiment.pinsVisible {
			state.Experiment.RemovedHostPins = plan.experiment.removedPins
		}
	}

	changes, err := bapi.DescribePublishChanges(state)
	if err != nil {
		return nil, warnings, weberror.NewWebError(err, "unable to describe what publishing changes").
			SetStatus(http.StatusInternalServerError)
	}

	hideUnlistedImages(changes, actor)

	return changes, warnings, nil
}

// publishPreviewWarnings are the warnings that a publication adds when it writes the topology and the experiment.
// A dry run reports them without the write. topology is the config the publication would write.
func publishPreviewWarnings(name string, topology *store.Config, plan *builderPublishPlan) []bdoc.Issue {
	var warnings []bdoc.Issue

	if existing := plan.topology.existing; existing != nil {
		reference, err := bapi.DecodeReference(existing.Metadata.Annotations[bapi.DocumentAnnotation])
		if err == nil && reference.Path != "" {
			warnings = append(warnings, bdoc.NewIssue(bdoc.CodePublishFileUnchanged, "",
				builderFileNotWrittenWarning(name, reference.Path)))
		}
	}

	if !plan.topology.applied {
		if warning, replaced := bapi.ReplaceLegacyDiagram(topology); replaced {
			warnings = append(warnings, warning)
		}
	}

	if experiment := plan.experiment; experiment != nil && len(experiment.removedPins) > 0 {
		warnings = append(warnings, hostPinsWarning(experiment.removedPins, experiment.pinsVisible))
	}

	return warnings
}

// builderPublishIntent reads a publish request and its entity tag.
// A publication refuses a bad entity tag before it decodes the body. A dry run needs no entity tag.
func builderPublishIntent(w http.ResponseWriter, r *http.Request) (builderPublishRequest, string, error) {
	var request builderPublishRequest

	ifMatch, ifMatchErr := builderIfMatch(r)
	if ifMatchErr != nil && !builderDryRunRequested(w, r) {
		return request, "", ifMatchErr
	}

	if err := builderDecode(w, r, &request); err != nil {
		return request, "", err
	}

	if ifMatchErr != nil && !request.DryRun {
		return request, "", ifMatchErr
	}

	return request, ifMatch, nil
}

// builderDryRunRequested reports whether the body of a publish request asks
// for a dry run. It puts the body back for the decode of the request. It
// reads at most [builderMaxRequestBytes]. A body that it cannot read or
// decode asks for no dry run. Thus a publication answers its missing entity
// tag first, as it does without a look at the body.
func builderDryRunRequested(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, builderMaxRequestBytes))
	r.Body = io.NopCloser(bytes.NewReader(data))

	if err != nil {
		return false
	}

	var peek struct {
		DryRun bool `json:"dryRun"`
	}

	if err := json.Unmarshal(data, &peek); err != nil {
		return false
	}

	return peek.DryRun
}

// builderPreviewRefusal returns the issues that a dry run lists for an error that would refuse a publication.
// It lists a 4xx refusal other than 401, 403 and 404, which keep their status.
func builderPreviewRefusal(err error) ([]bdoc.Issue, bool) {
	web := &weberror.WebError{} //nolint:exhaustruct // filled by errors.As

	if !errors.As(builderCodedError(err), &web) {
		return nil, false
	}

	authorization := web.Status == http.StatusUnauthorized || web.Status == http.StatusForbidden ||
		web.Status == http.StatusNotFound
	if authorization || web.Status < http.StatusBadRequest || web.Status >= http.StatusInternalServerError {
		return nil, false
	}

	issues := bdoc.ErrorIssues(err)
	if len(issues) == 0 {
		issues = []bdoc.Issue{bdoc.NewIssue(bdoc.Code(web.Code), "", web.Error())}
	}

	for i := range issues {
		issues[i].Severity = bdoc.SeverityError
	}

	return issues, true
}

// serverImages returns the file names of every server disk image, or nil when they are unknown.
// It lists nothing when spec names no disk image, because the Publish dialog sends a dry run after each edit pause.
func (b *builderAPI) serverImages(ctx context.Context, actor builderActor, spec map[string]any) []string {
	if !actor.role.Allowed("disks", "list") || !bapi.NamesDiskImage(spec) {
		return nil
	}

	disks, err := b.disks.images(ctx)
	if err != nil {
		plog.Debug(plog.TypeSystem, "listing disk images for a builder publication preview", "err", err)

		return nil
	}

	names := make([]string, 0, len(disks))

	for _, image := range disks {
		if image.Name != "" {
			names = append(names, image.Name)
		}
	}

	if len(names) == 0 {
		return nil
	}

	return names
}

// hideUnlistedImages sets onServer to null (unknown) for each image whose
// file name the caller may not list. GET /disks leaves such images out for
// the caller. This applies whether the server has the image or not. Thus the
// answer never tells apart an image hidden from the caller and an image that
// the server does not have.
func hideUnlistedImages(changes *bapi.PublishChanges, actor builderActor) {
	for i := range changes.Images {
		if !actor.role.Allowed("disks", "list", path.Base(changes.Images[i].Name)) {
			changes.Images[i].OnServer = nil
		}
	}
}
