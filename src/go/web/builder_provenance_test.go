package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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
		t.Errorf("GET /builder/drafts/%s answered with a stamp, though it stored no document: %s", draft.ID, recorder.Body)
	}

	var read builderDraftResponse

	decodeBuilderAnswer(t, recorder, &read)

	return read
}

// TestBuilderDraftResponsesCarryTheStamp asserts creating and saving a draft
// answer with the stamp the document then holds, and no other answer has one.
func TestBuilderDraftResponsesCarryTheStamp(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "first")
	created := bdoc.FormatTime(draft.Created)
	want := bdoc.Provenance{CreatedBy: builderAlice, CreatedAt: created, UpdatedBy: builderAlice, UpdatedAt: created}

	if diff := cmp.Diff(&want, draft.Stamp); diff != "" {
		t.Errorf("creating draft first: stamp mismatch (-want +got):\n%s", diff)
	}

	saved := saveBuilderDraft(t, harness, builderBob, draft, builderDocument(t, "second"))
	want.UpdatedBy, want.UpdatedAt = builderBob, bdoc.FormatTime(saved.Updated)

	if diff := cmp.Diff(&want, saved.Stamp); diff != "" {
		t.Errorf("saving draft first as bob: stamp mismatch (-want +got):\n%s", diff)
	}

	// The response spells the stamp with the keys of the document's metadata.
	var raw struct {
		Stamp map[string]string `json:"stamp"`
	}

	if err := json.Unmarshal([]byte(builderJSON(t, saved)), &raw); err != nil {
		t.Fatalf("decoding the saved draft: %v", err)
	}

	wantKeys := map[string]string{
		"createdBy": want.CreatedBy, "createdAt": want.CreatedAt, "updatedBy": want.UpdatedBy, "updatedAt": want.UpdatedAt,
	}
	if diff := cmp.Diff(wantKeys, raw.Stamp); diff != "" {
		t.Errorf("saving draft first as bob: encoded stamp mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, builderStamp(t, getBuilderDraft(t, harness, builderAlice, saved).Document)); diff != "" {
		t.Errorf("the saved document's stamp mismatch (-want +got):\n%s", diff)
	}

	path := "/builder/drafts/" + draft.ID

	// A value no document may hold refuses the save, though it would have been replaced.
	invalid := strings.Replace(string(builderDocument(t, "bad")), `"metadata": {`, `"metadata": {"updatedAt": "today",`, 1)

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", body: `{"document":` + invalid + `}`,
		user: builderAlice, ifMatch: saved.ETag,
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("a save with updatedAt today: status = %d, want %d: %s",
			recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
	}

	if !strings.Contains(recorder.Body.String(), "updatedAt") {
		t.Errorf("a save with updatedAt today = %s, want it to name updatedAt", recorder.Body)
	}

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: "/builder/drafts", user: builderAlice},
		{method: http.MethodGet, path: path + "/snapshots", user: builderAlice},
		{method: http.MethodGet, path: path + "/snapshots/current", user: builderAlice},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderAlice, ifMatch: saved.ETag},
	} {
		recorder := harness.do(request)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, http.StatusOK, recorder.Body)
		}

		if strings.Contains(recorder.Body.String(), `"stamp"`) {
			t.Errorf("%s %s = %s, want no stamp", request.method, request.path, recorder.Body)
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

	if opened.Digest != reference.Digest {
		t.Errorf("opening lab as a draft: digest = %s, want the published %s", opened.Digest, reference.Digest)
	}

	stamp := builderStamp(t, published.Document)
	if diff := cmp.Diff(&stamp, opened.Stamp); diff != "" {
		t.Errorf("opening lab as a draft: stamp mismatch (-want +got):\n%s", diff)
	}

	documents, writes := harness.store.Count(bapi.NamespacePublished), harness.configWrites
	response, _ := publishBuilderDraft(t, harness, opened, `{"mode":"topology","topology":{"name":"lab","action":"update"}}`,
		http.StatusOK)

	if len(response.Stages) < 2 {
		t.Fatalf("republishing lab: stages = %+v, want a topology stage", response.Stages)
	}

	if got := response.Stages[1].Status; got != bapi.PublishSkipped {
		t.Errorf("republishing lab: topology stage status = %q, want %q", got, bapi.PublishSkipped)
	}

	if got := harness.configWrites - writes; got != 0 {
		t.Errorf("republishing lab wrote %d configs, want none", got)
	}

	if got := harness.store.Count(bapi.NamespacePublished) - documents; got != 0 {
		t.Errorf("republishing lab stored %d published documents, want none", got)
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

	got := builderStamp(t, data)
	if diff := cmp.Diff(first, got); diff != "" {
		t.Errorf("publishing after the undo: stamp mismatch (-want +got):\n%s", diff)
	}

	if got.UpdatedBy != builderAlice {
		t.Errorf("publishing after the undo: stamp updatedBy = %q, want %q", got.UpdatedBy, builderAlice)
	}

	// The record of the publication is another fact: who published, and when.
	published, err := time.Parse(bdoc.TimeLayout, first.UpdatedAt)
	if err != nil {
		t.Fatalf("parsing %q: %v", first.UpdatedAt, err)
	}

	if record.CreatedBy != builderBob {
		t.Errorf("publishing after the undo: record createdBy = %q, want %q", record.CreatedBy, builderBob)
	}

	if !record.CreatedAt.After(published) {
		t.Errorf("publishing after the undo: record createdAt = %s, want later than %s", record.CreatedAt, first.UpdatedAt)
	}

	if record.Digest != undone.Digest {
		t.Errorf("publishing after the undo: record digest = %s, want the snapshot's %s", record.Digest, undone.Digest)
	}
}

// TestBuilderDraftSourceFile asserts a draft keeps the file name it was
// uploaded from, and a name that is not a file name is refused.
func TestBuilderDraftSourceFile(t *testing.T) {
	const name = "Pump station (rev 2).builder.yaml"

	harness := newBuilderHarness(t)
	document := json.RawMessage(builderDocument(t, "uploaded"))
	draft := harness.postDraft(builderAlice, map[string]any{"document": document, "sourceFile": name})

	if draft.SourceFile != name {
		t.Errorf("creating a draft from %q: source file = %q, want %q", name, draft.SourceFile, name)
	}

	if read := getBuilderDraft(t, harness, builderAlice, draft); read.SourceFile != name {
		t.Errorf("reading the draft from %q: source file = %q, want %q", name, read.SourceFile, name)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: builderJSON(t, map[string]any{"document": document, "sourceFile": "../pump.json"}),
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("creating a draft from ../pump.json: status = %d, want %d: %s",
			recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
	}

	if refusal := builderRefusal(t, recorder); !strings.Contains(refusal.Message+refusal.Cause, "sourceFile") {
		t.Errorf("creating a draft from ../pump.json: refusal = %+v, want it to name sourceFile", refusal)
	}
}
