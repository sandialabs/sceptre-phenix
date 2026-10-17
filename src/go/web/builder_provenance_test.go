package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
)

// builderStamp is the creator, creation time, last editor and last edit time
// an encoded document holds.
func builderStamp(t *testing.T, data []byte) bdoc.Provenance {
	t.Helper()

	document, err := bdoc.Decode(data)
	if err != nil {
		t.Fatalf("decoding a document: %v", err)
	}

	return document.Provenance()
}

// saveBuilderDraft saves a document as the next snapshot of a draft, as
// the user, and returns the draft after it.
func saveBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	user string,
	draft builderDraftResponse,
	document []byte,
) builderDraftResponse {
	t.Helper()

	var saved builderDraftResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts/" + draft.ID + "/snapshots",
		body: `{"document":` + string(document) + `}`, user: user, ifMatch: draft.ETag,
	}, http.StatusCreated, &saved)

	return saved
}

// getBuilderDraft reads a draft with its current document, as the user.
func getBuilderDraft(t *testing.T, harness *builderHarness, user string, draft builderDraftResponse) builderDraftResponse {
	t.Helper()

	recorder := harness.doDecode(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + draft.ID, user: user,
	}, http.StatusOK, nil)

	if strings.Contains(recorder.Body.String(), `"stamp"`) {
		t.Fatalf("reading a draft returned a stamp, though it stored no document: %s", recorder.Body)
	}

	var read builderDraftResponse

	harness.decode(recorder, &read)

	return read
}

// TestBuilderDraftResponsesCarryTheStamp asserts creating and saving a draft
// answer with the stamp the document then holds, and no other answer has one.
func TestBuilderDraftResponsesCarryTheStamp(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "first")
	created := bdoc.FormatTime(draft.Created)
	want := bdoc.Provenance{CreatedBy: builderAlice, CreatedAt: created, UpdatedBy: builderAlice, UpdatedAt: created}

	if draft.Stamp == nil || *draft.Stamp != want {
		t.Fatalf("stamp of the new draft = %+v, want %+v", draft.Stamp, want)
	}

	saved := saveBuilderDraft(t, harness, builderBob, draft, builderDocument(t, "second"))
	want.UpdatedBy, want.UpdatedAt = builderBob, bdoc.FormatTime(saved.Updated)

	if saved.Stamp == nil || *saved.Stamp != want {
		t.Fatalf("stamp of the save = %+v, want %+v", saved.Stamp, want)
	}

	// The response spells the stamp with the keys of the document's metadata.
	var raw struct {
		Stamp map[string]string `json:"stamp"`
	}

	if err := json.Unmarshal([]byte(builderJSON(t, saved)), &raw); err != nil || len(raw.Stamp) != 4 ||
		raw.Stamp["updatedBy"] != builderBob {
		t.Fatalf("stamp = %v (%v), want createdBy, createdAt, updatedBy and updatedAt", raw.Stamp, err)
	}

	if got := builderStamp(t, getBuilderDraft(t, harness, builderAlice, saved).Document); got != want {
		t.Fatalf("the saved document holds %+v, want the stamp %+v", got, want)
	}

	path := "/builder/drafts/" + draft.ID

	// A value no document may hold refuses the save, though it would have been replaced.
	invalid := strings.Replace(string(builderDocument(t, "bad")), `"metadata": {`, `"metadata": {"updatedAt": "today",`, 1)

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", body: `{"document":` + invalid + `}`,
		user: builderAlice, ifMatch: saved.ETag,
	})
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "updatedAt") {
		t.Fatalf("a save with a malformed updatedAt: status = %d: %s", recorder.Code, recorder.Body)
	}

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: "/builder/drafts", user: builderAlice},
		{method: http.MethodGet, path: path + "/snapshots", user: builderAlice},
		{method: http.MethodGet, path: path + "/snapshots/current", user: builderAlice},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderAlice, ifMatch: saved.ETag},
	} {
		if recorder := harness.doDecode(request, http.StatusOK, nil); strings.Contains(recorder.Body.String(), `"stamp"`) {
			t.Fatalf("%s %s answered with a stamp", request.method, request.path)
		}
	}
}

// TestBuilderEditAsDraftKeepsThePublishedDocument asserts a published diagram
// opened unchanged as a draft holds its digest and stamp, and republishing it is skipped.
func TestBuilderEditAsDraftKeepsThePublishedDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	publishBuilderDraft(t, harness, draft, `{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)

	reference := builderTopologyReference(t, harness, "lab")

	var published builderDocumentResponse

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents/" + reference.ID, user: builderBob},
		http.StatusOK, &published)

	// Compact, as an editor sends back what it parsed.
	opened := harness.postDraft(builderBob, map[string]any{
		"document":    json.RawMessage(builderCompactJSON(t, published.Document)),
		"sourceToken": builderDocTokenPrefix + reference.ID,
	})

	if opened.Digest != reference.Digest || opened.Stamp == nil || *opened.Stamp != builderStamp(t, published.Document) {
		t.Fatalf("draft = digest %s, stamp %+v; want the published %s", opened.Digest, opened.Stamp, reference.Digest)
	}

	documents, writes := harness.store.Count(bapi.NamespacePublished), harness.configWrites
	response, _ := publishBuilderDraft(t, harness, opened, `{"mode":"topology","topology":{"name":"lab","action":"update"}}`,
		http.StatusOK)

	if response.Stages[1].Status != bapi.PublishSkipped || harness.configWrites != writes ||
		harness.store.Count(bapi.NamespacePublished) != documents {
		t.Fatalf("stages = %#v; want nothing written", response.Stages)
	}
}

// TestBuilderPublishAfterUndoPublishesTheOlderStamp asserts publishing after
// an undo publishes the older snapshot's stamp, and records the publisher apart.
func TestBuilderPublishAfterUndoPublishesTheOlderStamp(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "shared")
	path := "/builder/drafts/" + draft.ID
	first := builderStamp(t, getBuilderDraft(t, harness, builderAlice, draft).Document)

	saved := saveBuilderDraft(t, harness, builderBob, draft, builderDocument(t, "later"))

	var undone builderDraftResponse

	harness.doDecode(builderRequest{
		method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderBob, ifMatch: saved.ETag,
	}, http.StatusOK, &undone)
	harness.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/publish", user: builderBob, ifMatch: undone.ETag,
		body: `{"mode":"topology","topology":{"name":"shared","action":"create"}}`,
	}, http.StatusOK, nil)

	reference := builderTopologyReference(t, harness, "shared")

	record, data, err := harness.service.GetPublishedDocumentData(context.Background(), reference.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	if got := builderStamp(t, data); got != first || got.UpdatedBy != builderAlice {
		t.Fatalf("the published document holds %+v, want the first snapshot's %+v", got, first)
	}

	// The record of the publication is another fact: who published, and when.
	published, err := time.Parse(bdoc.TimeLayout, first.UpdatedAt)
	if err != nil {
		t.Fatalf("parsing %q: %v", first.UpdatedAt, err)
	}

	if record.CreatedBy != builderBob || !record.CreatedAt.After(published) || record.Digest != undone.Digest {
		t.Fatalf("published record = by %s at %s with digest %s; want %s, later than %s, with the snapshot's %s",
			record.CreatedBy, record.CreatedAt, record.Digest, builderBob, first.UpdatedAt, undone.Digest)
	}
}

// TestBuilderDraftSourceFile asserts a draft keeps the file name it was
// uploaded from, and a name that is not a file name is refused.
func TestBuilderDraftSourceFile(t *testing.T) {
	const name = "Pump station (rev 2).builder.yaml"

	harness := newBuilderHarness(t)
	document := json.RawMessage(builderDocument(t, "uploaded"))
	draft := harness.postDraft(builderAlice, map[string]any{"document": document, "sourceFile": name})

	if read := getBuilderDraft(t, harness, builderAlice, draft); draft.SourceFile != name || read.SourceFile != name {
		t.Fatalf("source file = %q, then %q; want %q", draft.SourceFile, read.SourceFile, name)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: builderJSON(t, map[string]any{"document": document, "sourceFile": "../pump.json"}),
	})
	if refusal := builderRefusal(t, recorder); recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(refusal.Message+refusal.Cause, "sourceFile") {
		t.Fatalf("a parent path: status = %d, said %+v; want 422 naming sourceFile", recorder.Code, refusal)
	}
}
