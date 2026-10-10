package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

	if !slices.Equal(links, want) {
		t.Fatalf("links = %+v, want %+v", links, want)
	}
}

// TestBuilderDocumentExperiment names the experiment the publication behind
// a published document made: one built from the document's topology that
// records the document, or the draft that published it.
func TestBuilderDocumentExperiment(t *testing.T) {
	t.Parallel()

	document := &bapi.PublishedDocument{ID: "doc1", Target: "lab", Kind: builderKindTopology, DraftID: "d1"}
	fromFile := &bapi.PublishedDocument{ID: "doc1", Target: "lab", Kind: builderKindTopology}

	for _, test := range []struct {
		name     string
		role     rbac.Role
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
			name: "a document the CLI published has no draft", role: experimentRole(), document: fromFile,
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
			name: "the role may get only the next one", role: experimentRole("zz"), document: document,
			links: []builderExperimentLink{
				{name: "aa", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc0"},
			},
			want: "zz",
		},
		{
			name: "one that records the document comes first", role: experimentRole(), document: document,
			links: []builderExperimentLink{
				{name: "aa", topology: "lab", draftID: "d1", documentID: "doc0"},
				{name: "mm", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc1"},
			},
			want: "mm",
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

			if got := builderDocumentExperiment(test.role, test.links, test.document); got != test.want {
				t.Fatalf("experiment = %q, want %q", got, test.want)
			}
		})
	}
}

// TestBuilderDraftExperiment names the experiment a draft's publication
// made: one that records the draft, or a published document of the draft's
// (the one it was opened from, the one it last published, the one the draft
// it forks had published).
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
		role  rbac.Role
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
			name: "an experiment that records no document is not a draft's without one", role: experimentRole(),
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
			name: "the role may get only the next one", role: experimentRole("zz"), meta: published("exp", "doc1"),
			links: []builderExperimentLink{
				{name: "exp", topology: "lab", draftID: "d1", documentID: "doc1"},
				{name: "zz", topology: "lab", draftID: "d1", documentID: "doc0"},
			},
			want: "zz",
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

			if got := builderDraftExperiment(test.role, test.links, test.meta); got != test.want {
				t.Fatalf("experiment = %q, want %q", got, test.want)
			}
		})
	}
}

// failExperimentListing makes the harness answer from routes whose listing
// of the Experiment configs fails, over the same drafts and configs.
func (h *builderHarness) failExperimentListing() {
	h.t.Helper()

	router, api := newBuilderRouter()

	err := registerBuilderRoutes(api,
		withBuilderService(h.service),
		withBuilderConfigs(func(kind string) (store.Configs, error) {
			if strings.EqualFold(kind, kindExperiment) {
				h.configLists = append(h.configLists, kindExperiment)

				return nil, errors.New("injected experiment listing failure")
			}

			return h.listConfigs(kind)
		}, h.getConfig),
		withBuilderPublishOps(h.publishOps()),
		withBuilderDocumentFiles(h.files, filepath.Join(h.files, "mounts")),
	)
	if err != nil {
		h.t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	h.router, h.api = router, api
}

// renameExperiment renames a stored experiment, annotations and all, as
// renaming it through the configs API does.
func (h *builderHarness) renameExperiment(name, renamed string) {
	h.t.Helper()

	for i := range h.configs {
		if h.configs[i].Kind == kindExperiment && h.configs[i].Metadata.Name == name {
			h.configs[i].Metadata.Name = renamed

			return
		}
	}

	h.t.Fatalf("experiment %s is not stored", name)
}

// deleteExperiment deletes a stored experiment.
func (h *builderHarness) deleteExperiment(name string) {
	h.configs = slices.DeleteFunc(h.configs, func(config store.Config) bool {
		return config.Kind == kindExperiment && config.Metadata.Name == name
	})
}

// listsExperiments counts the listings of the Experiment configs the API
// made since configLists was last cleared.
func (h *builderHarness) listsExperiments() int {
	count := 0

	for _, kind := range h.configLists {
		if kind == kindExperiment {
			count++
		}
	}

	return count
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

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/documents", user: builderTestOwner, role: role,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing documents: status = %d: %s", recorder.Code, recorder.Body)
	}

	var response struct {
		Documents []map[string]any `json:"documents"`
	}

	harness.decode(recorder, &response)

	rows := map[string]map[string]any{}

	for _, row := range response.Documents {
		target, _ := row["target"].(string)
		rows[target] = row
	}

	return rows
}

// checkRowExperiment checks the experiment a listed row names: want, or no
// field at all when want is empty.
func checkRowExperiment(t *testing.T, what string, row map[string]any, want string) {
	t.Helper()

	if row == nil {
		t.Fatalf("%s: the row is not listed", what)
	}

	got, present := row["experiment"]

	switch {
	case want == "" && present:
		t.Errorf("%s: experiment = %v, want the field left out", what, got)
	case want != "" && got != want:
		t.Errorf("%s: experiment = %v, want %q", what, got, want)
	}
}

// TestBuilderListDocumentsNamesExperiment lists the experiment a
// publication made on the row of its stored document, for a caller who may
// get the experiment, and follows the experiment when it is renamed or
// deleted. A row read from a Builder file never names one, and neither does
// any row when the experiments cannot be listed.
func TestBuilderListDocumentsNamesExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	harness.addFileTopology("file", bapi.DocumentReference{Path: filepath.Join(harness.files, "file.json")})
	harness.configs = append(harness.configs, linkedExperiment(t, "file-exp", "file", "d9", "doc9"))
	plain := builderPublish(t, harness, "plain")
	harness.configs = append(harness.configs, linkedExperiment(t, "by-hand", "plain", "", "doc9"))

	harness.configLists = nil
	rows := documentRows(t, harness, nil)

	checkRowExperiment(t, "published with an experiment", rows["lab"], "exp")
	checkRowExperiment(t, "a file row", rows["file"], "")
	checkRowExperiment(t, "a document no draft published", rows["plain"], "")

	if rows["lab"]["source"] != builderDocumentSourceStore || rows["file"]["source"] != builderDocumentSourceFile ||
		rows["plain"]["id"] != plain.ID {
		t.Fatalf("rows = %v, want lab and plain stored and file read from its file", rows)
	}

	if got := harness.listsExperiments(); got != 1 {
		t.Errorf("the listing listed the experiments %d times, want once", got)
	}

	// The draft publishes the topology again, without the experiment: the
	// document is a new one, and the experiment still records the old one.
	edited := editBuilderDraft(t, harness, lab.draft, lab.document, "bb")
	again, _ := publishBuilderDraft(t, harness, edited, publishLabUpdate, http.StatusOK)

	if again.Draft.Publication.DocumentID == lab.draft.Publication.DocumentID {
		t.Fatal("publishing the edit stored no new document")
	}

	checkRowExperiment(t, "published again without it", documentRows(t, harness, nil)["lab"], "exp")

	none, other := experimentRole("other"), experimentRole("other", "second")
	checkRowExperiment(t, "a role that may not get it", documentRows(t, harness, &none)["lab"], "")

	// A second experiment of the same publication stands in for one the
	// caller may not get, and comes after it otherwise.
	harness.configs = append(harness.configs,
		linkedExperiment(t, "second", "lab", lab.draft.ID, again.Draft.Publication.DocumentID))

	checkRowExperiment(t, "a role that may get the second", documentRows(t, harness, &other)["lab"], "second")
	checkRowExperiment(t, "the one that records the document", documentRows(t, harness, nil)["lab"], "second")

	harness.deleteExperiment("second")
	harness.renameExperiment("exp", "renamed")
	checkRowExperiment(t, "renamed", documentRows(t, harness, nil)["lab"], "renamed")

	harness.failExperimentListing()
	harness.configLists = nil

	rows = documentRows(t, harness, nil)
	checkRowExperiment(t, "the experiments cannot be listed", rows["lab"], "")

	if len(rows) != 3 || harness.listsExperiments() != 1 {
		t.Errorf("listed %d rows after %d tries at the experiments, want all 3 rows after one try",
			len(rows), harness.listsExperiments())
	}
}

// TestBuilderListDocumentsWithoutExperiment leaves the field out once the
// experiment is deleted, and lists no experiments when no stored document is
// listed.
func TestBuilderListDocumentsWithoutExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	harness.deleteExperiment("exp")
	checkRowExperiment(t, "deleted", documentRows(t, harness, nil)["lab"], "")

	files := newBuilderHarness(t)
	files.addFileTopology("file", bapi.DocumentReference{Path: filepath.Join(files.files, "file.json")})
	files.configs = append(files.configs, linkedExperiment(t, "exp", "file", "d1", "doc1"))
	files.configLists = nil

	checkRowExperiment(t, "a file row", documentRows(t, files, nil)["file"], "")

	if got := files.listsExperiments(); got != 0 {
		t.Errorf("a listing of file rows listed the experiments %d times, want never", got)
	}
}

// TestBuilderPublishAnswersNameExperiment checks the draft of every publish
// answer names the experiment the draft published: a completed publication,
// one repeated with the same If-Match, and one that failed part way.
func TestBuilderPublishAnswersNameExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	if lab.answer.Draft.Experiment != "exp" {
		t.Fatalf("published: draft experiment = %q, want exp", lab.answer.Draft.Experiment)
	}

	// The answer of a publication that already completed, asked for again.
	before := lab.draft
	before.ETag = bapi.RevisionETag(mustDraftMeta(t, harness, lab.draft.ID).Publication.Revision)

	retried, _ := publishBuilderDraft(t, harness, before, publishLabExperiment, http.StatusOK)
	if !strings.Contains(strings.Join(bdoc.IssueMessages(retried.Warnings), " "), "already complete") ||
		retried.Draft.Experiment != "exp" {
		t.Fatalf("retry: warnings %q and draft experiment %q, want the completed publication with exp",
			retried.Warnings, retried.Draft.Experiment)
	}

	// A publication that fails at the topology leaves the draft as it was,
	// and its answer still names the experiment.
	edited := editBuilderDraft(t, harness, lab.draft, lab.document, "bb")
	harness.failConfigKind = builderKindTopology

	partial, _ := publishBuilderDraft(t, harness, edited, publishLabUpdate, http.StatusInternalServerError)
	if partial.Status != bapi.PublishPartial || partial.Draft.Experiment != "exp" {
		t.Fatalf("partial: status %q and draft experiment %q, want a partial publication with exp",
			partial.Status, partial.Draft.Experiment)
	}

	harness.failConfigKind = ""

	// A role that may not get the experiment is not told its name.
	role := builderRole(
		builderPolicy([]string{"configs", "topologies"}, []string{"*", "*/*"}, builderShareConfigVerbs),
		builderPolicy([]string{"experiments"}, []string{"other"}, []string{"get"}),
	)

	hidden, _ := publishBuilderDraftAs(t, harness, edited, &role, publishLabUpdate, http.StatusOK)
	if hidden.Status != bapi.PublishSucceeded || hidden.Draft.Experiment != "" {
		t.Fatalf("without experiments get: status %q and draft experiment %q, want a publication that names none",
			hidden.Status, hidden.Draft.Experiment)
	}

	// The experiments cannot be listed: the publication is not refused.
	harness.failExperimentListing()

	unlisted := editBuilderDraft(t, harness, hidden.Draft, lab.document, "cc")

	quiet, _ := publishBuilderDraft(t, harness, unlisted, publishLabUpdate, http.StatusOK)
	if quiet.Status != bapi.PublishSucceeded || quiet.Draft.Experiment != "" {
		t.Fatalf("experiments not listed: status %q and draft experiment %q, want a publication that names none",
			quiet.Status, quiet.Draft.Experiment)
	}
}

// TestBuilderDraftAnswersNameExperiment checks GET of a draft and the answer
// that creates one name the experiment: for the draft that published it, a
// draft opened from its published diagram, and a fork. The name follows a
// rename and goes with the experiment; the listing never has it.
func TestBuilderDraftAnswersNameExperiment(t *testing.T) {
	lab := newPublishedLab(t)
	harness := lab.harness

	experimentOf := func(what string, role *rbac.Role, draft builderDraftResponse, want string) {
		t.Helper()

		got, raw := readBuilderDraftAs(t, harness, builderTestOwner, role, draft.Owner, draft.ID)
		if _, present := raw["experiment"]; got.Experiment != want || present != (want != "") {
			t.Errorf("%s: experiment = %q (field present: %t), want %q", what, got.Experiment, present, want)
		}
	}

	experimentOf("the draft that published it", nil, lab.draft, "exp")

	// "Edit as a draft" of the published diagram, not yet published itself.
	opened := openPublishedBuilderDocument(t, harness, "lab")
	fromDocument := createBuilderPublishDraft(t, harness, opened.document, builderDocTokenPrefix+opened.id)

	if fromDocument.Experiment != "exp" || fromDocument.Publication != nil {
		t.Fatalf("created from the published diagram: experiment = %q and publication %v, want exp and none",
			fromDocument.Experiment, fromDocument.Publication)
	}

	experimentOf("a draft opened from the published diagram", nil, fromDocument, "exp")

	recorder := forkBuilderDraft(t, harness, builderTestOwner, nil, lab.draft.Owner+"/"+lab.draft.ID, lab.document)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("fork: status = %d: %s", recorder.Code, recorder.Body)
	}

	var forked builderDraftResponse

	harness.decode(recorder, &forked)

	if forked.Experiment != "exp" || forked.Forked == nil {
		t.Fatalf("fork: experiment = %q and forked %v, want exp and what the draft published", forked.Experiment, forked.Forked)
	}

	experimentOf("a fork", nil, forked, "exp")

	none := experimentRole("other")
	experimentOf("a role that may not get it", &none, lab.draft, "")

	// The listing lists no experiments and names none.
	harness.configLists = nil

	recorder = harness.do(builderRequest{method: http.MethodGet, path: "/builder/drafts", user: builderTestOwner})
	if recorder.Code != http.StatusOK {
		t.Fatalf("listing drafts: status = %d: %s", recorder.Code, recorder.Body)
	}

	var listing struct {
		Drafts []map[string]any `json:"drafts"`
	}

	harness.decode(recorder, &listing)

	if len(listing.Drafts) != 3 || harness.listsExperiments() != 0 {
		t.Fatalf("listed %d drafts and the experiments %d times, want 3 drafts and no experiment listing",
			len(listing.Drafts), harness.listsExperiments())
	}

	for _, row := range listing.Drafts {
		if _, present := row["experiment"]; present {
			t.Errorf("listed draft %v names experiment %v, want the listing never to", row["id"], row["experiment"])
		}
	}

	// The stored name of the last publication is not the link.
	harness.renameExperiment("exp", "renamed")

	if target := mustDraftMeta(t, harness, lab.draft.ID).Publication.ExperimentTarget; target != "exp" {
		t.Fatalf("the draft's last publication names %q, want exp still", target)
	}

	experimentOf("renamed", nil, lab.draft, "renamed")
	experimentOf("renamed, from the published diagram", nil, fromDocument, "renamed")
	experimentOf("renamed, a fork", nil, forked, "renamed")

	harness.failExperimentListing()
	experimentOf("the experiments cannot be listed", nil, lab.draft, "")

	harness.deleteExperiment("renamed")
	experimentOf("deleted", nil, lab.draft, "")
}

// TestBuilderDraftWithoutPublicationListsNoExperiments checks a draft that
// never published, forks nothing and was opened from no published diagram
// costs no listing of the experiments, when it is created, read or published
// for the first time without one.
func TestBuilderDraftWithoutPublicationListsNoExperiments(t *testing.T) {
	harness := newBuilderHarness(t, linkedExperiment(t, "exp", "lab", "d9", "doc9"))

	document := bdoc.NewDocument("lab")
	draft := createBuilderPublishDraft(t, harness, document, "Topology/elsewhere")

	if got, raw := readBuilderDraftAs(t, harness, builderTestOwner, nil, draft.Owner, draft.ID); got.Experiment != "" ||
		raw["experiment"] != nil || draft.Experiment != "" {
		t.Fatalf("a draft that published nothing names experiment %q (created: %q)", got.Experiment, draft.Experiment)
	}

	if got := harness.listsExperiments(); got != 0 {
		t.Fatalf("creating and reading the draft listed the experiments %d times, want never", got)
	}

	// A publication that fails before the draft records one names none, and
	// lists none either.
	harness.failConfigKind = builderKindTopology
	harness.configLists = nil

	partial, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusInternalServerError)

	if partial.Draft.Experiment != "" || harness.listsExperiments() != 0 {
		t.Fatalf("partial: draft experiment %q after %d listings of the experiments, want none and never",
			partial.Draft.Experiment, harness.listsExperiments())
	}
}

// TestBuilderDraftCanDelete checks canDelete says what DELETE of the draft
// would answer, wherever a draft's access is reported: the rows of the
// listing, GET of one draft, and the draft a PUT of its shares returns. A
// share never lets a user delete a draft.
func TestBuilderDraftCanDelete(t *testing.T) {
	fixture := newBuilderShareFixture(t)
	fixture.share(builderTestPeer + ":edit")

	var (
		all       = builderShareConfigVerbs
		noDelete  = []string{"list", "get", "create", "update"}
		harness   = fixture.harness
		owner     = builderTestOwner
		peer      = builderTestPeer
		draftPath = "/builder/drafts/" + owner + "/" + fixture.id
	)

	for _, test := range []struct {
		name string
		user string
		role *rbac.Role
		want bool
	}{
		{name: "owner", user: owner, role: builderShareRole(all), want: true},
		{name: "owner without config delete", user: owner, role: builderShareRole(noDelete)},
		{name: "owner whose role names only builder-drafts delete", user: owner, role: builderShareRole(noDelete, "delete")},
		{name: "edit share", user: peer, role: builderShareRole(all)},
		{name: "edit share with role delete", user: peer, role: builderShareRole(all, "delete"), want: true},
		{name: "role list, get and delete", user: builderShareCarol, role: builderShareRole(all, "list", "get", "delete"), want: true},
		{name: "role list and get", user: builderShareCarol, role: builderShareRole(all, "list", "get")},
		{
			name: "role delete without config delete", user: builderShareCarol,
			role: builderShareRole(noDelete, "list", "get", "delete"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, raw := readBuilderDraftAs(t, harness, test.user, test.role, owner, fixture.id)
			if _, present := raw["canDelete"]; got.CanDelete != test.want || present != test.want {
				t.Errorf("GET: canDelete = %t (field present: %t), want %t", got.CanDelete, present, test.want)
			}

			listing := fixture.list(test.user, test.role)
			rows := listing.Shared

			if test.user == owner {
				rows = listing.Drafts
			}

			if len(rows) != 1 || rows[0].ID != fixture.id || rows[0].CanDelete != test.want {
				t.Errorf("listing: rows = %+v, want the draft with canDelete %t", rows, test.want)
			}

			// What the field promises is what the route does. The request names
			// a revision the draft is not at, so nothing is deleted.
			recorder := harness.do(builderRequest{
				method: http.MethodDelete, path: draftPath, user: test.user, role: test.role, ifMatch: `"0"`,
			})

			want := http.StatusForbidden
			if test.want {
				want = http.StatusPreconditionFailed
			}

			if recorder.Code != want {
				t.Errorf("DELETE: status = %d, want %d: %s", recorder.Code, want, recorder.Body)
			}
		})
	}

	// The draft a PUT of its shares returns says the same.
	for _, test := range []struct {
		name string
		role *rbac.Role
		want bool
	}{
		{name: "owner", role: builderShareRole(all), want: true},
		{name: "owner without config delete", role: builderShareRole(noDelete)},
	} {
		recorder := harness.do(builderRequest{
			method: http.MethodPut, path: draftPath + "/shares", user: owner, role: test.role,
			ifMatch: fixture.meta().SharesETag(), body: builderShareBody(t, peer+":edit"),
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: sharing: status = %d: %s", test.name, recorder.Code, recorder.Body)
		}

		var response builderSharesResponse

		harness.decode(recorder, &response)

		if response.Draft == nil || response.Draft.CanDelete != test.want {
			t.Errorf("%s: the draft of a PUT of its shares = %+v, want canDelete %t", test.name, response.Draft, test.want)
		}
	}

	// Creating a draft reports no access, and so no canDelete.
	if created := harness.createDraft(owner, "created"); created.CanDelete || created.Access != "" {
		t.Errorf("created draft reports access %q and canDelete %t, want neither", created.Access, created.CanDelete)
	}
}
