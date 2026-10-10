package web

import (
	"encoding/json"
	"net/http"

	"gopkg.in/yaml.v3"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/web/weberror"
)

// builderTopologyExportRequest asks for the topology config a document
// publishes as. Name is the topology's name; empty, it is the name the
// Publish dialog proposes (see [bdoc.TopologyName]).
type builderTopologyExportRequest struct {
	Document json.RawMessage `json:"document"`
	Name     string          `json:"name"`
}

// builderTopologyExportResponse is the topology config a document publishes
// as, as YAML text. It is JSON rather than a YAML body so that it carries
// the warnings too, and so that a refusal is the JSON error of every other
// Builder route.
type builderTopologyExportResponse struct {
	Name     string       `json:"name"`
	YAML     string       `json:"yaml"`
	Warnings []bdoc.Issue `json:"warnings"`
	// PublishBlockers is why publishing the topology would be refused
	// although phenix's config validation accepts it, one issue per check in
	// the order Publish makes them; Publish's refusal names the first. Each
	// says what Publish's refusal says of its check, has the code of the
	// check's first problem, and is located when the check has one problem.
	PublishBlockers []bdoc.Issue `json:"publishBlockers"`
}

// builderExportedTopology is a topology config as an export writes it: the
// config publishing stores, without what storing it adds (the created and
// updated times, and the annotation naming the published document).
type builderExportedTopology struct {
	APIVersion string                  `yaml:"apiVersion"`
	Kind       string                  `yaml:"kind"`
	Metadata   builderExportedMetadata `yaml:"metadata"`
	// Spec is the spec as [store.ExactYAML] makes it.
	Spec any `yaml:"spec"`
}

type builderExportedMetadata struct {
	Name string `yaml:"name"`
}

// exportTopology - POST /builder/export/topology.
//
// The phenix Topology config a document publishes as, as Publish would write
// it (see [phenix/types/builder.Document.ExportTopologyConfig]), without
// writing anything. The document comes with the request, so it holds edits
// not saved yet, and is checked as saving a draft checks it. A document
// phenix's config validation refuses is refused with the status Publish
// answers, and with its message unless blank VLANs, shared addresses or
// hostnames phenix refuses when it creates an experiment, which Publish names
// first, are beside the validation's own reason (see
// [phenix/types/builder.Document.ExportTopologyConfig]); the checks only
// publishing makes are reported with the config. Nothing is read from the
// store: included topologies are named, as Publish writes them, not merged.
func (b *builderAPI) exportTopology(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderExportTopology")

	// Whoever may open a draft may export it, as its Builder JSON export
	// needs no request at all.
	if _, err := builderAuthorize(r, builderVerbGet, "exporting a builder topology"); err != nil {
		return err
	}

	var request builderTopologyExportRequest

	if err := builderDecode(w, r, &request); err != nil {
		return err
	}

	data, err := builderDocumentBytes(request.Document)
	if err != nil {
		return err
	}

	document, err := bapi.ParseDocument(data)
	if err != nil {
		return builderWebError(err, "unable to export the builder document")
	}

	name := request.Name
	if name == "" {
		name = bdoc.TopologyName(document.Metadata.Name)
	}

	// The name is checked as a publish checks its topology target.
	target := builderPublishTarget{Name: name, Action: builderPublishActionCreate}
	if err := validatePublishTarget(builderSourceTopology, target); err != nil {
		return err
	}

	export, err := document.ExportTopologyConfig(name)
	if err != nil {
		return publishProjectionRefusal(name, err)
	}

	// Each is named as Publish's refusal names it (see publishProjectionRefusal).
	blockers := make([]bdoc.Issue, 0, len(export.PublishBlockers))

	for _, blocker := range export.PublishBlockers {
		blockers = append(blockers, exportBlocker(blocker))
	}

	body, err := yaml.Marshal(builderExportedTopology{
		APIVersion: export.Config.Version,
		Kind:       export.Config.Kind,
		Metadata:   builderExportedMetadata{Name: export.Config.Metadata.Name},
		Spec:       store.ExactYAML(export.Config.Spec),
	})
	if err != nil {
		return weberror.NewWebError(err, "unable to encode topology %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	warnings := export.Warnings
	if warnings == nil {
		warnings = []bdoc.Issue{}
	}

	return builderWriteJSON(w, http.StatusOK, "", builderTopologyExportResponse{
		Name:            name,
		YAML:            string(body),
		Warnings:        warnings,
		PublishBlockers: blockers,
	})
}

// exportBlocker is the issue of one check only publishing makes that an
// exported topology fails: what Publish's refusal says of it (see
// [projectionProblems]), of the code of its first problem, and where that
// problem is when it is the only one.
func exportBlocker(blocker error) bdoc.Issue {
	problems := bdoc.ErrorIssues(blocker)

	reason, named := projectionProblems(blocker)
	if !named || len(problems) == 0 {
		return bdoc.NewIssue(bdoc.CodePublishTopologyInvalid, "", blocker.Error())
	}

	issue := problems[0]
	issue.Message = reason

	if len(problems) > 1 {
		issue.Path, issue.NodeID, issue.EdgeID, issue.NetworkID, issue.Field = "", "", "", "", ""
	}

	return issue
}
