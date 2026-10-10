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

// builderPublishPreview answers a dry run of a publication: what publishing
// the draft to the request's targets would change (see
// [bapi.DescribePublishChanges]), what the publication would warn of, and
// why it would be refused, each an issue with its code. Changes is nil when
// it would be refused.
type builderPublishPreview struct {
	Status   string               `json:"status"`
	Changes  *bapi.PublishChanges `json:"changes"`
	Warnings []bdoc.Issue         `json:"warnings"`
	Errors   []bdoc.Issue         `json:"errors"`
}

// previewPublish answers a dry run of a publication (a publish request with
// dryRun): it authorizes the draft and the targets as a publication does,
// makes every check of preflightPublish on the draft's current snapshot, and
// writes nothing: no published document, config, scenario, experiment or
// draft record. A publication would be refused with 409 or 422 (or 400 for
// a target name), and the dry run lists that refusal in its errors with
// 200; authorization failures (403, and 404 for a draft the caller may not
// see) keep their status, as do the server's own failures. It needs no
// If-Match, and takes no publish lock: what it reports may change before a
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

// publishPreview makes the checks of a publication of the draft's current
// snapshot and returns what it would change and warn of, or the error it
// would be refused with.
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
		ServerImages:   b.serverImages(actor),
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

// publishPreviewWarnings are the warnings a publication adds once it writes
// the topology, which a dry run reports without writing it: a Builder file
// the topology names, which publishing leaves behind, and a legacy Builder
// diagram the update replaces. topology is the config the publication would
// write, which nothing stores.
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
// before its body is decoded. A dry run writes nothing, so it needs no
// entity tag: only a request without a valid one has its body looked at
// first, for dryRun, and goes on to be decoded when it asks for a dry run.
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
// for a dry run, and puts the body back for the request's own decoding. It
// reads at most [builderMaxRequestBytes]. A body it cannot read or decode
// asks for none, so a publication answers its missing entity tag first, as
// it does without looking at the body.
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

// builderPreviewRefusal returns the issues a dry run lists for the error a
// publication would be refused with, and whether it lists it: a refusal a
// publication answers with a 4xx status other than 401, 403 and 404, which
// say who may do what and keep their status. The issues are those the
// refusal is made of (see [bdoc.ErrorIssues]), or else one of its code
// saying what it says, each an error.
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

// serverImages returns the file names of every disk image the server has,
// as GET /disks reads them, or nil when they are unknown: the caller may
// list no disks, the listing failed, or it lists none, as it does when
// minimega is not running. It holds the images the caller may not list by
// name too, which [hideUnlistedImages] then reports nothing of.
func (b *builderAPI) serverImages(actor builderActor) []string {
	if b.listDisks == nil || !actor.role.Allowed("disks", "list") {
		return nil
	}

	disks, err := b.listDisks()
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

// hideUnlistedImages makes whether the server has a disk image unknown
// (onServer null) for each image whose file name the caller may not list,
// which GET /disks leaves out for the caller. That holds whether the server
// has the image or not, so the answer never tells an image hidden from the
// caller from one the server lacks.
func hideUnlistedImages(changes *bapi.PublishChanges, actor builderActor) {
	for i := range changes.Images {
		if !actor.role.Allowed("disks", "list", path.Base(changes.Images[i].Name)) {
			changes.Images[i].OnServer = nil
		}
	}
}
