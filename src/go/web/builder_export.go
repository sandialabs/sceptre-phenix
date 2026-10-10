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
// publishes as. Name is the name of the topology. When it is empty, the
// name is the one that the Publish dialog proposes (see [bdoc.TopologyName]).
type builderTopologyExportRequest struct {
	Document json.RawMessage `json:"document"`
	Name     string          `json:"name"`
}

// builderTopologyExportResponse is the topology config a document publishes
// as, as YAML text. It is JSON, not a YAML body, so that it also carries the
// warnings. Also, a refusal is then the JSON error of every other Builder
// route.
type builderTopologyExportResponse struct {
	Name     string       `json:"name"`
	YAML     string       `json:"yaml"`
	Warnings []bdoc.Issue `json:"warnings"`
	// PublishBlockers tells why Publish would refuse the topology, although
	// the phenix config validation accepts it. It has one issue per check, in
	// the order that Publish makes the checks. The Publish refusal names the
	// first. Each issue says what the Publish refusal says about its check, and
	// has the code of the first problem of the check. When the check has one
	// problem, the issue also has its location.
	PublishBlockers []bdoc.Issue `json:"publishBlockers"`
}

// builderExportedTopology is a topology config as an export writes it. It is
// the config that a publish stores, without what the store adds: the created
// and updated times, and the annotation that names the published document.
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
// The phenix Topology config that a document publishes as, as Publish would
// write it (see [phenix/types/builder.Document.ExportTopologyConfig]). The
// route writes nothing. The document comes with the request, so it holds
// edits not saved yet. The route checks it as a draft save checks it.
//
// A document that the phenix config validation refuses gets the status that
// Publish answers. It gets its message only when no blank VLANs, shared
// addresses or hostnames that phenix refuses at experiment create are beside
// the reason of the validation. Publish names those first (see
// [phenix/types/builder.Document.ExportTopologyConfig]). The response reports
// the checks that only a publish makes with the config. The route reads
// nothing from the store. It names included topologies, as Publish writes
// them, and does not merge them.
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

// exportBlocker is the issue of one check, which only a publish makes, that
// an exported topology fails. The issue has what the Publish refusal says of
// the check (see [projectionProblems]) and the code of its first problem.
// When there is only one problem, the issue also has its location.
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
