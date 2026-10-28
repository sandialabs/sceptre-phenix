package web

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/web/rbac"
)

// linkedExperiment returns an Experiment config that records the Builder
// publication of the draft and the document, built from the topology.
func linkedExperiment(t *testing.T, name, topology, draftID, documentID string) store.Config {
	t.Helper()

	config := builderConfig(t, kindExperiment, name)
	config.Metadata.Annotations = store.Annotations{
		"topology": topology,
		builderExperimentAnnotation: `{"draftId":"` + draftID + `","documentId":"` + documentID +
			`","digest":"sha256:0"}`,
	}

	return config
}

// TestBuilderExperimentLinks reads the links only of Experiment configs
// whose builder-experiment annotation decodes and names something.
func TestBuilderExperimentLinks(t *testing.T) {
	t.Parallel()

	annotated := func(kind, name, value string) store.Config {
		config := builderConfig(t, kind, name)
		config.Metadata.Annotations = store.Annotations{"topology": "lab", builderExperimentAnnotation: value}

		return config
	}

	noTopology := builderConfig(t, kindExperiment, "no-topology")
	noTopology.Metadata.Annotations = store.Annotations{builderExperimentAnnotation: `{"draftId":"d2"}`}

	links := builderExperimentLinks(store.Configs{
		builderConfig(t, kindExperiment, "by-hand"),
		annotated(kindExperiment, "not-json", "draft d1"),
		annotated(kindExperiment, "a-list", `["d1"]`),
		annotated(kindExperiment, "a-string", `"d1"`),
		annotated(kindExperiment, "null", "null"),
		annotated(kindExperiment, "empty", "{}"),
		annotated(kindExperiment, "half-read", `{"draftId":"d1","documentId":5}`),
		annotated(builderKindTopology, "a-topology", `{"draftId":"d1","documentId":"doc1"}`),
		annotated(kindExperiment, "made", `{"draftId":"d1","documentId":"doc1","digest":"sha256:0"}`),
		noTopology,
	})

	want := []builderExperimentLink{
		{name: "made", topology: "lab", draftID: "d1", documentID: "doc1"},
		{name: "no-topology", topology: "", draftID: "d2", documentID: ""},
	}

	if diff := cmp.Diff(want, links, cmp.AllowUnexported(builderExperimentLink{})); diff != "" {
		t.Errorf("builderExperimentLinks(configs) mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderDocumentExperiment names the experiment of a published document:
// one of its topology that records the document or its draft.
func TestBuilderDocumentExperiment(t *testing.T) {
	t.Parallel()

	document := &bapi.PublishedDocument{ID: "doc1", Target: "lab", Kind: builderKindTopology, DraftID: "d1"}
	fromFile := &bapi.PublishedDocument{ID: "doc1", Target: "lab", Kind: builderKindTopology}

	for _, test := range []struct {
		name     string
		role     *rbac.Role
		document *bapi.PublishedDocument
		links    []builderExperimentLink
		want     string
	}{
		{
			name: "the experiment records the document", role: experimentRole(), document: document,
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "other", documentID: "doc1"}},
			want:  "exp",
		},
		{
			name: "the experiment records the draft, which published the topology again", role: experimentRole(),
			document: document,
			links:    []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc0"}},
			want:     "exp",
		},
		{
			name: "the same draft published another topology", role: experimentRole(), document: document,
			links: []builderExperimentLink{{name: "exp", topology: "other", draftID: "d1", documentID: "doc0"}},
		},
		{
			name: "the document of another topology has the same ID", role: experimentRole(), document: document,
			links: []builderExperimentLink{{name: "exp", topology: "other", draftID: "d9", documentID: "doc1"}},
		},
		{
			name: "another draft and another document", role: experimentRole(), document: document,
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d9", documentID: "doc9"}},
		},
		{
			name: "a published document without a draft", role: experimentRole(), document: fromFile,
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "", documentID: "doc9"}},
		},
		{
			name: "a document published to another kind", role: experimentRole(),
			document: &bapi.PublishedDocument{ID: "doc1", Target: "lab", Kind: kindExperiment, DraftID: "d1"},
			links:    []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
		},
		{
			name: "the experiment was deleted", role: experimentRole(), document: document,
		},
		{
			name: "the experiment was renamed", role: experimentRole(), document: document,
			links: []builderExperimentLink{{name: "renamed", topology: "lab", draftID: "d1", documentID: "doc1"}},
			want:  "renamed",
		},
		{
			name: "the role may not get the experiment", role: experimentRole("other"), document: document,
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
		},
		{
			name: "then the smallest name, whatever the order", role: experimentRole(), document: document,
			links: []builderExperimentLink{
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "B", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "aa", topology: "lab", draftID: "d1", documentID: "doc0"},
			},
			want: "B",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := builderDocumentExperiment(*test.role, test.links, test.document); got != test.want {
				t.Errorf("builderDocumentExperiment(role, links, %+v) = %q, want %q", *test.document, got, test.want)
			}
		})
	}
}

// TestBuilderDraftExperiment names the experiment of a draft's publication:
// one that records the draft or a published document of the draft's.
func TestBuilderDraftExperiment(t *testing.T) {
	t.Parallel()

	published := func(experiment, document string) *bapi.DraftMetadata {
		return &bapi.DraftMetadata{
			ID: "d1",
			Publication: &bapi.PublicationState{
				TopologyTarget: "lab", ExperimentTarget: experiment, DocumentID: document,
			},
		}
	}

	for _, test := range []struct {
		name  string
		role  *rbac.Role
		meta  *bapi.DraftMetadata
		links []builderExperimentLink
		want  string
	}{
		{
			name: "the experiment records the draft", role: experimentRole(), meta: published("exp", "doc2"),
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
			want:  "exp",
		},
		{
			name: "the experiment records the document the draft last published", role: experimentRole(),
			meta:  published("", "doc1"),
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d9", documentID: "doc1"}},
			want:  "exp",
		},
		{
			name: "the draft was opened from the document", role: experimentRole(),
			meta:  &bapi.DraftMetadata{ID: "d2", SourceToken: builderDocTokenPrefix + "doc1"},
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
			want:  "exp",
		},
		{
			name: "the draft forks the draft that published the document", role: experimentRole(),
			meta: &bapi.DraftMetadata{
				ID:     "d2",
				Forked: &bapi.ForkedPublication{DocumentID: "doc1", TopologyTarget: "lab", ExperimentTarget: "exp"},
			},
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
			want:  "exp",
		},
		{
			name: "another draft and another document", role: experimentRole(), meta: published("exp", "doc2"),
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d9", documentID: "doc9"}},
		},
		{
			name: "an experiment that records no document is not one of a draft without one", role: experimentRole(),
			meta:  &bapi.DraftMetadata{ID: "d2"},
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: ""}},
		},
		{
			name: "the experiment was deleted", role: experimentRole(), meta: published("exp", "doc1"),
		},
		{
			name: "the experiment was renamed", role: experimentRole(), meta: published("exp", "doc1"),
			links: []builderExperimentLink{{name: "renamed", topology: "lab", draftID: "d1", documentID: "doc1"}},
			want:  "renamed",
		},
		{
			name: "the role may not get the experiment", role: experimentRole("other"), meta: published("exp", "doc1"),
			links: []builderExperimentLink{{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"}},
		},
		{
			name: "the one the last publication named comes first", role: experimentRole(),
			meta: published("zz", "doc1"),
			links: []builderExperimentLink{
				{name: "aa", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "mm", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc0"},
			},
			want: "zz",
		},
		{
			name: "then one that records the last published document", role: experimentRole(),
			meta: published("gone", "doc1"),
			links: []builderExperimentLink{
				{name: "aa", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "mm", topology: "lab", draftID: "d1", documentID: "doc1"},
			},
			want: "mm",
		},
		{
			name: "then the smallest name, whatever the order", role: experimentRole(),
			meta: published("gone", "doc2"),
			links: []builderExperimentLink{
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "B", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "aa", topology: "other", draftID: "d1", documentID: "doc0"},
			},
			want: "B",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := builderDraftExperiment(*test.role, test.links, test.meta); got != test.want {
				t.Errorf("builderDraftExperiment(role, links, draft %s) = %q, want %q", test.meta.ID, got, test.want)
			}
		})
	}
}

// failExperimentListing makes the harness answer from routes whose listing
// of the Experiment configs fails, over the same drafts and configs.
func (h *builderHarness) failExperimentListing() {
	h.t.Helper()

	h.reroute(withBuilderConfigs(func(kind string) (store.Configs, error) {
		if strings.EqualFold(kind, kindExperiment) {
			return nil, errors.New("injected experiment listing failure")
		}

		return h.listConfigs(kind)
	}, h.getConfig))
}

// publishedLab is a draft that created topology lab, with node aa, and
// experiment exp, and the document it holds.
type publishedLab struct {
	harness  *builderHarness
	draft    builderDraftResponse
	document *bdoc.Document
	answer   builderPublishResponse
}

const (
	publishLabExperiment = `{"mode":"topology-experiment","topology":{"name":"lab","action":"create"},` +
		`"experiment":{"name":"exp","action":"create"}}`
	publishLabUpdate = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`
)

func newPublishedLab(t *testing.T) *publishedLab {
	t.Helper()

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")
	answer, _ := publishBuilderDraft(t, harness, draft, publishLabExperiment, http.StatusOK)

	return &publishedLab{harness: harness, draft: answer.Draft, document: document, answer: answer}
}

// documentRows returns the rows of GET /builder/documents by target, as the
// role sees them (the full role when nil), each as the JSON object it is.
func documentRows(t *testing.T, harness *builderHarness, role *rbac.Role) map[string]map[string]any {
	t.Helper()

	var response struct {
		Documents []map[string]any `json:"documents"`
	}

	harness.as(builderAlice, role).doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents"}, http.StatusOK, &response)

	rows := map[string]map[string]any{}

	for _, row := range response.Documents {
		target, _ := row["target"].(string)
		rows[target] = row
	}

	return rows
}

// rowExperimentError reports a listed row that does not name the experiment
// want, or that has the field at all when want is empty.
func rowExperimentError(row map[string]any, want string) error {
	if row == nil {
		return errors.New("the row is not listed")
	}

	got, present := row["experiment"]

	switch {
	case want == "" && present:
		return fmt.Errorf("experiment = %v, want the field left out", got)
	case want != "" && got != want:
		return fmt.Errorf("experiment = %v, want %q", got, want)
	}

	return nil
}

// TestBuilderListDocumentsNamesExperiment names the experiment of a stored
// document's publication on its row, for a caller who may get it.
func TestBuilderListDocumentsNamesExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	harness.addFileTopology("file", bapi.DocumentReference{Path: filepath.Join(harness.files, "file.json")})
	harness.configs = append(harness.configs, linkedExperiment(t, "file-exp", "file", "d9", "doc9"))
	plain := builderPublish(t, harness, "plain")
	harness.configs = append(harness.configs, linkedExperiment(t, "by-hand", "plain", "", "doc9"))

	rows := documentRows(t, harness, nil)

	for _, tt := range []struct {
		what   string
		target string
		want   string
	}{
		{what: "published with an experiment", target: "lab", want: "exp"},
		{what: "a file row", target: "file", want: ""},
		{what: "a document no draft published", target: "plain", want: ""},
	} {
		t.Run(tt.what, func(t *testing.T) {
			if err := rowExperimentError(rows[tt.target], tt.want); err != nil {
				t.Errorf("GET /builder/documents row %s: %v", tt.target, err)
			}
		})
	}

	got := map[string]any{
		"lab source": rows["lab"]["source"], "file source": rows["file"]["source"], "plain id": rows["plain"]["id"],
	}
	want := map[string]any{
		"lab source": builderDocumentSourceStore, "file source": builderDocumentSourceFile, "plain id": plain.ID,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /builder/documents rows mismatch (-want +got):\n%s", diff)
	}

	// The draft publishes the topology again, without the experiment: the
	// document is a new one, and the experiment still records the old one.
	edited := editBuilderDraft(t, harness, lab.draft, lab.document, "bb")
	again, _ := publishBuilderDraft(t, harness, edited, publishLabUpdate, http.StatusOK)

	if got := again.Draft.Publication.DocumentID; got == lab.draft.Publication.DocumentID {
		t.Errorf("publishing the edit: document ID = %s, want a new document", got)
	}

	if err := rowExperimentError(documentRows(t, harness, nil)["lab"], "exp"); err != nil {
		t.Errorf("GET /builder/documents row lab, published again without it: %v", err)
	}

	none, other := experimentRole("other"), experimentRole("other", "second")
	if err := rowExperimentError(documentRows(t, harness, none)["lab"], ""); err != nil {
		t.Errorf("GET /builder/documents row lab, as a role that may not get it: %v", err)
	}

	// A second experiment of the same publication stands in for one the
	// caller may not get, and comes after it otherwise.
	harness.configs = append(harness.configs,
		linkedExperiment(t, "second", "lab", lab.draft.ID, again.Draft.Publication.DocumentID))

	if err := rowExperimentError(documentRows(t, harness, other)["lab"], "second"); err != nil {
		t.Errorf("GET /builder/documents row lab, as a role that may get the second: %v", err)
	}

	if err := rowExperimentError(documentRows(t, harness, nil)["lab"], "second"); err != nil {
		t.Errorf("GET /builder/documents row lab, the one that records the document: %v", err)
	}

	harness.failExperimentListing()

	rows = documentRows(t, harness, nil)
	if err := rowExperimentError(rows["lab"], ""); err != nil {
		t.Errorf("GET /builder/documents row lab, the experiments cannot be listed: %v", err)
	}

	if len(rows) != 3 {
		t.Errorf("GET /builder/documents listed %d rows, want all 3", len(rows))
	}
}

// publishOutcome is the status of a publication and the experiment its draft names.
type publishOutcome struct {
	Status     bapi.PublishStatus
	Experiment string
}

func outcomeOf(response builderPublishResponse) publishOutcome {
	return publishOutcome{Status: response.Status, Experiment: response.Draft.Experiment}
}

// TestBuilderPublishAnswersNameExperiment names the experiment in the draft
// of a completed, a repeated and a partial publication.
func TestBuilderPublishAnswersNameExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	if lab.answer.Draft.Experiment != "exp" {
		t.Errorf("publish: draft experiment = %q, want exp", lab.answer.Draft.Experiment)
	}

	// The answer of a publication that already completed, asked for again.
	before := lab.draft
	before.ETag = bapi.RevisionETag(mustDraftMeta(t, harness, lab.draft.ID).Publication.Revision)

	retried, _ := publishBuilderDraft(t, harness, before, publishLabExperiment, http.StatusOK)
	if !strings.Contains(strings.Join(bdoc.IssueMessages(retried.Warnings), " "), "already complete") {
		t.Errorf("publish retry: warnings = %q, want the completed publication", retried.Warnings)
	}

	if retried.Draft.Experiment != "exp" {
		t.Errorf("publish retry: draft experiment = %q, want exp", retried.Draft.Experiment)
	}

	// A publication that fails at the topology leaves the draft as it was,
	// and its answer still names the experiment.
	edited := editBuilderDraft(t, harness, lab.draft, lab.document, "bb")
	harness.failConfigKind = builderKindTopology

	partial, _ := publishBuilderDraft(t, harness, edited, publishLabUpdate, http.StatusInternalServerError)
	if diff := cmp.Diff(publishOutcome{bapi.PublishPartial, "exp"}, outcomeOf(partial)); diff != "" {
		t.Errorf("publish failing at the topology mismatch (-want +got):\n%s", diff)
	}

	harness.failConfigKind = ""

	// A role that may not get the experiment is not told its name.
	role := builderRole(
		builderPolicy([]string{"configs", "topologies"}, []string{"*", "*/*"}, builderConfigVerbs),
		builderPolicy([]string{"experiments"}, []string{"other"}, []string{"get"}),
	)

	hidden, _ := publishBuilderDraftAs(t, harness, edited, role, publishLabUpdate, http.StatusOK)
	if diff := cmp.Diff(publishOutcome{bapi.PublishSucceeded, ""}, outcomeOf(hidden)); diff != "" {
		t.Errorf("publish without experiments get mismatch (-want +got):\n%s", diff)
	}

	// The experiments cannot be listed: the publication is not refused.
	harness.failExperimentListing()

	unlisted := editBuilderDraft(t, harness, hidden.Draft, lab.document, "cc")

	quiet, _ := publishBuilderDraft(t, harness, unlisted, publishLabUpdate, http.StatusOK)
	if diff := cmp.Diff(publishOutcome{bapi.PublishSucceeded, ""}, outcomeOf(quiet)); diff != "" {
		t.Errorf("publish with experiments not listed mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderDraftAnswersNameExperiment names the experiment in a draft's GET
// and create answers, for the publishing draft, one opened from it and a fork.
func TestBuilderDraftAnswersNameExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	// experimentError reads the draft as role and reports an experiment other than want.
	experimentError := func(role *rbac.Role, draft builderDraftResponse, want string) error {
		t.Helper()

		got, raw := readBuilderDraftAs(t, harness, builderAlice, role, draft.ID)
		if _, present := raw["experiment"]; got.Experiment != want || present != (want != "") {
			return fmt.Errorf("experiment = %q (field present: %t), want %q", got.Experiment, present, want)
		}

		return nil
	}

	if err := experimentError(nil, lab.draft, "exp"); err != nil {
		t.Errorf("GET the draft that published it: %v", err)
	}

	// "Edit as a draft" of the published diagram, not yet published itself.
	opened := openPublishedBuilderDocument(t, harness, "lab")
	fromDocument := createBuilderPublishDraft(t, harness, opened.document, builderDocTokenPrefix+opened.id)

	if fromDocument.Experiment != "exp" {
		t.Errorf("POST /builder/drafts from the published diagram: experiment = %q, want exp", fromDocument.Experiment)
	}

	if fromDocument.Publication != nil {
		t.Errorf("POST /builder/drafts from the published diagram: publication = %+v, want none", fromDocument.Publication)
	}

	if err := experimentError(nil, fromDocument, "exp"); err != nil {
		t.Errorf("GET a draft opened from the published diagram: %v", err)
	}

	recorder := forkBuilderDraft(t, harness, builderAlice, nil, lab.draft.ID, lab.document)
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"POST /builder/drafts forking %s: status = %d, want %d: %s",
			lab.draft.ID,
			recorder.Code,
			http.StatusCreated,
			recorder.Body,
		)
	}

	var forked builderDraftResponse

	harness.decode(recorder, &forked)

	if forked.Experiment != "exp" {
		t.Errorf("fork: experiment = %q, want exp", forked.Experiment)
	}

	if forked.Forked == nil {
		t.Errorf("fork: forked = nil, want what the draft published")
	}

	if err := experimentError(nil, forked, "exp"); err != nil {
		t.Errorf("GET a fork: %v", err)
	}

	none := experimentRole("other")
	if err := experimentError(none, lab.draft, ""); err != nil {
		t.Errorf("GET the draft as a role that may not get it: %v", err)
	}

	// The listing names no experiment.
	var listing struct {
		Drafts []map[string]any `json:"drafts"`
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/drafts", user: builderAlice}, http.StatusOK, &listing)

	if len(listing.Drafts) != 3 {
		t.Errorf("GET /builder/drafts listed %d drafts, want 3", len(listing.Drafts))
	}

	for _, row := range listing.Drafts {
		if _, present := row["experiment"]; present {
			t.Errorf("GET /builder/drafts: draft %v names experiment %v, want the listing never to", row["id"], row["experiment"])
		}
	}

	harness.failExperimentListing()

	if err := experimentError(nil, lab.draft, ""); err != nil {
		t.Errorf("GET the draft when the experiments cannot be listed: %v", err)
	}
}
