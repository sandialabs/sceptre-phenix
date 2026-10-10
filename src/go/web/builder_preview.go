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

// previewPublish answers a dry run of a publication (a publish request with
// dryRun). It authorizes the draft and the targets as a publication does. It
// makes every check of preflightPublish on the current snapshot of the
// draft. It writes nothing: no published document, config, scenario,
// experiment or draft record.
//
// Where a publication would get 409 or 422 (or 400 for a target name), the
// dry run lists that refusal in its errors, with 200. Authorization failures
// (403, and 404 for a draft that the caller may not see) keep their status.
// The failures of the server also keep their status. It needs no If-Match,
// and takes no publish lock. Thus what it reports may change before a
// publication is sent.
func (b *builderAPI) previewPublish(
	w http.ResponseWriter,
	r *http.Request,
	actor builderActor,
	request builderPublishRequest,
) error {
	meta, err := b.draftFor(r, actor, builderVerbUpdate, "previewing a builder publication")
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
		ServerImages:   b.serverImages(actor, projection.Spec),
	}

	if plan.scenarios != nil {
		state.Scenarios = plan.scenarios.stored
	}

	if plan.experiment != nil {
		state.Experiment = &bapi.ExperimentState{
			Name: plan.experiment.name, Stored: plan.experiment.existing, Held: plan.experiment.applied,
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

// publishPreviewWarnings are the warnings that a publication adds when it
// writes the topology. A dry run reports them without the write. They are
// about a Builder file that the topology names, which the publish leaves
// behind, and a legacy Builder diagram that the update replaces. topology is
// the config that the publication would write, which nothing stores.
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

	return warnings
}

// builderPublishIntent reads a publish request and the entity tag it is
// based on. A publication answers a missing or malformed entity tag (400)
// before it decodes its body. A dry run writes nothing, so it needs no
// entity tag. Only for a request without a valid tag does the function first
// look at the body, for dryRun. It decodes the body when the request asks
// for a dry run.
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

// builderPreviewRefusal returns the issues that a dry run lists for the
// error that would refuse a publication, and whether it lists them. It lists
// a refusal that a publication answers with a 4xx status other than 401, 403
// and 404. Those three say who may do what, and keep their status. The
// issues are those of the refusal (see [bdoc.ErrorIssues]), or else one
// issue with its code that says what the refusal says. Each issue is an
// error.
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

// serverImages returns the file names of every disk image that the server
// has, as GET /disks reads them. It returns nil when they are unknown: the
// caller may list no disks, the listing failed, or the listing is empty (as
// it is when minimega is not running). The result also holds the images that
// the caller may not list by name. [hideUnlistedImages] then reports nothing
// about them. It lists nothing when spec, the topology that the publication
// writes, names no disk image. The Publish dialog sends a dry run after each
// pause in editing, and the server list would then say nothing about any
// image.
func (b *builderAPI) serverImages(actor builderActor, spec map[string]any) []string {
	if !actor.role.Allowed("disks", "list") || !bapi.NamesDiskImage(spec) {
		return nil
	}

	disks, err := b.disks.images()
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
