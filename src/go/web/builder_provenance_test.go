package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// builderClaim returns an encoded document changed to claim a creator, a
// creation time, a last editor and a last edit time.
func builderClaim(t *testing.T, data []byte, claim bdoc.Provenance) []byte {
	t.Helper()

	document, err := bdoc.Decode(data)
	if err != nil {
		t.Fatalf("decoding a document: %v", err)
	}

	document.SetProvenance(claim)

	claimed, err := bdoc.Encode(document)
	if err != nil {
		t.Fatalf("encoding a document: %v", err)
	}

	return claimed
}

// postBuilderDraft creates a draft from the fields of a request as the
// user, and returns the answer and, when one was made, the draft.
func postBuilderDraft(
	t *testing.T,
	harness *builderHarness,
	user string,
	fields map[string]any,
) (*httptest.ResponseRecorder, builderDraftResponse) {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: user,
		body: builderJSON(t, fields),
	})

	var draft builderDraftResponse

	if recorder.Code == http.StatusCreated {
		harness.decode(recorder, &draft)
	}

	return recorder, draft
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

	recorder := harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/snapshots",
		body:   `{"document":` + string(document) + `}`,
		user:   user, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("saving draft %s as %s: status = %d: %s", draft.ID, user, recorder.Code, recorder.Body)
	}

	var saved builderDraftResponse

	harness.decode(recorder, &saved)

	return saved
}

// getBuilderDraft reads a draft with its current document, as the user.
func getBuilderDraft(t *testing.T, harness *builderHarness, user string, draft builderDraftResponse) builderDraftResponse {
	t.Helper()

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/" + draft.Owner + "/" + draft.ID, user: user,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("getting draft %s as %s: status = %d: %s", draft.ID, user, recorder.Code, recorder.Body)
	}

	if strings.Contains(recorder.Body.String(), `"stamp"`) {
		t.Fatalf("reading a draft returned a stamp, though it stored no document: %s", recorder.Body)
	}

	var read builderDraftResponse

	harness.decode(recorder, &read)

	return read
}

// TestBuilderDraftResponsesCarryTheStamp checks that creating a draft and
// saving one answer with the four values the server wrote into the document,
// which are exactly what the document then holds, and that no other
// response has a stamp.
func TestBuilderDraftResponsesCarryTheStamp(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder, draft := postBuilderDraft(t, harness, builderTestOwner, map[string]any{
		"document": json.RawMessage(builderDocument(t, "first")),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("creating the draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	// One time for the draft, its snapshot and its document.
	created := bdoc.FormatTime(draft.Created)

	want := bdoc.Provenance{
		CreatedBy: builderTestOwner, CreatedAt: created, UpdatedBy: builderTestOwner, UpdatedAt: created,
	}

	if draft.Stamp == nil || *draft.Stamp != want || !draft.Updated.Equal(draft.Created) {
		t.Fatalf("stamp of the new draft = %+v, want %+v", draft.Stamp, want)
	}

	// The response spells the stamp with the keys of the document's metadata.
	var raw struct {
		Stamp map[string]string `json:"stamp"`
	}

	harness.decode(recorder, &raw)

	if len(raw.Stamp) != 4 || raw.Stamp["createdBy"] != want.CreatedBy || raw.Stamp["createdAt"] != want.CreatedAt ||
		raw.Stamp["updatedBy"] != want.UpdatedBy || raw.Stamp["updatedAt"] != want.UpdatedAt {
		t.Fatalf("stamp = %v, want createdBy, createdAt, updatedBy and updatedAt", raw.Stamp)
	}

	read := getBuilderDraft(t, harness, builderTestOwner, draft)

	if got := builderStamp(t, read.Document); got != want {
		t.Fatalf("the document of the new draft holds %+v, want the stamp %+v", got, want)
	}

	saved := saveBuilderDraft(t, harness, builderTestOwner, draft, builderDocument(t, "second"))

	want.UpdatedAt = bdoc.FormatTime(saved.Updated)

	if saved.Stamp == nil || *saved.Stamp != want || !saved.Updated.After(draft.Created) {
		t.Fatalf("stamp of the save = %+v, want %+v", saved.Stamp, want)
	}

	read = getBuilderDraft(t, harness, builderTestOwner, saved)

	if got := builderStamp(t, read.Document); got != want {
		t.Fatalf("the saved document holds %+v, want the stamp %+v", got, want)
	}

	// The draft's own history says the same of the save (the document's time
	// is the snapshot's, to the second).
	if len(read.History) != 2 || read.History[1].CreatedBy != want.UpdatedBy ||
		bdoc.FormatTime(read.History[1].CreatedAt) != want.UpdatedAt {
		t.Fatalf("history = %+v, want its last snapshot stored by %s at %s", read.History, want.UpdatedBy, want.UpdatedAt)
	}

	// Nothing else stores a document, so nothing else answers with a stamp.
	path := "/builder/drafts/" + draft.Owner + "/" + draft.ID

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: "/builder/drafts", user: builderTestOwner},
		{method: http.MethodGet, path: path + "/snapshots", user: builderTestOwner},
		{method: http.MethodGet, path: path + "/snapshots/current", user: builderTestOwner},
		{method: http.MethodPatch, path: path + "/cursor", body: `{"index":0}`, user: builderTestOwner, ifMatch: saved.ETag},
	} {
		recorder := harness.do(request)
		if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"stamp"`) {
			t.Fatalf("%s %s: status = %d with a stamp %t; want %d and none",
				request.method, request.path, recorder.Code, strings.Contains(recorder.Body.String(), `"stamp"`), http.StatusOK)
		}
	}

	// After the undo the document is the first one again, with the stamp of
	// the save that made it.
	read = getBuilderDraft(t, harness, builderTestOwner, saved)

	if got := builderStamp(t, read.Document); got != *draft.Stamp {
		t.Fatalf("after an undo the document holds %+v, want %+v", got, *draft.Stamp)
	}
}

// TestBuilderStampNamesTheSaver saves a shared draft as its owner and as
// a user it is shared with. The last editor is whoever saved, the creator
// stays, and nothing a request claims about either is kept by a save.
func TestBuilderStampNamesTheSaver(t *testing.T) {
	fixture := newBuilderShareFixture(t)
	harness := fixture.harness

	fixture.share(builderTestPeer + ":edit")

	meta := fixture.meta()
	draft := newBuilderDraftResponse(meta)
	created := bdoc.FormatTime(meta.Created)

	forged := builderClaim(t, builderDocument(t, "edited"), bdoc.Provenance{
		CreatedBy: builderTestPeer, CreatedAt: "2001-01-01T00:00:00Z", UpdatedBy: builderTestOwner, UpdatedAt: "2031-01-01T00:00:00Z",
	})

	saved := saveBuilderDraft(t, harness, builderTestPeer, draft, forged)

	want := bdoc.Provenance{
		CreatedBy: builderTestOwner, CreatedAt: created, UpdatedBy: builderTestPeer,
		UpdatedAt: bdoc.FormatTime(saved.Updated),
	}

	if saved.Stamp == nil || *saved.Stamp != want {
		t.Fatalf("stamp of the recipient's save = %+v, want %+v", saved.Stamp, want)
	}

	read := getBuilderDraft(t, harness, builderTestOwner, saved)

	if got := builderStamp(t, read.Document); got != want {
		t.Fatalf("the document the recipient saved holds %+v, want %+v", got, want)
	}

	if read.Owner != builderTestOwner || read.LastModifiedBy != builderTestPeer {
		t.Fatalf("owner %q, last modified by %q; want the owner unchanged", read.Owner, read.LastModifiedBy)
	}

	// The owner saves the recipient's document back, as an editor does.
	saved = saveBuilderDraft(t, harness, builderTestOwner, saved, read.Document)

	want.UpdatedBy, want.UpdatedAt = builderTestOwner, bdoc.FormatTime(saved.Updated)

	if saved.Stamp == nil || *saved.Stamp != want {
		t.Fatalf("stamp of the owner's save = %+v, want %+v", saved.Stamp, want)
	}

	// A value no document may hold refuses the save, though it would have
	// been replaced.
	invalid := builderJSON(t, map[string]any{"document": json.RawMessage(
		strings.Replace(string(builderDocument(t, "bad")), `"metadata": {`, `"metadata": {"updatedAt": "today",`, 1),
	)})

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: fixture.path + "/snapshots", body: invalid,
		user: builderTestOwner, ifMatch: saved.ETag,
	})
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "updatedAt") {
		t.Fatalf("a save with a malformed updatedAt: status = %d: %s", recorder.Code, recorder.Body)
	}
}

// TestBuilderCreateDraftKeepsTheBodysCreator creates drafts from request
// bodies, as an upload does. The creator and the creation time a document
// names are kept, the last editor and the last edit time never are, and a
// value no document may hold refuses the request.
func TestBuilderCreateDraftKeepsTheBodysCreator(t *testing.T) {
	harness := newBuilderHarness(t)

	uploaded := builderClaim(t, builderDocument(t, "uploaded"), bdoc.Provenance{
		CreatedBy: "carol@elsewhere", CreatedAt: "2019-05-06T07:08:09Z", UpdatedBy: "mallory", UpdatedAt: "2031-01-01T00:00:00Z",
	})

	recorder, draft := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": json.RawMessage(uploaded),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("creating the draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	want := bdoc.Provenance{
		CreatedBy: "carol@elsewhere", CreatedAt: "2019-05-06T07:08:09Z", UpdatedBy: builderTestPeer,
		UpdatedAt: bdoc.FormatTime(draft.Created),
	}

	if draft.Stamp == nil || *draft.Stamp != want || draft.Owner != builderTestPeer {
		t.Fatalf("stamp = %+v for a draft of %s, want %+v", draft.Stamp, draft.Owner, want)
	}

	if got := builderStamp(t, getBuilderDraft(t, harness, builderTestPeer, draft).Document); got != want {
		t.Fatalf("the document holds %+v, want %+v", got, want)
	}

	// A later save keeps that creator, whatever it sends.
	saved := saveBuilderDraft(t, harness, builderTestPeer, draft, builderDocument(t, "uploaded"))

	want.UpdatedAt = bdoc.FormatTime(saved.Updated)

	if saved.Stamp == nil || *saved.Stamp != want {
		t.Fatalf("stamp of the save = %+v, want %+v", saved.Stamp, want)
	}

	drafts := harness.store.Count(bapi.NamespaceDrafts)

	for name, claim := range map[string]bdoc.Provenance{
		"a creator with a control character": {CreatedBy: "carol\x00", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
		"a creation time with an offset":     {CreatedBy: "", CreatedAt: "2019-05-06T07:08:09+02:00", UpdatedBy: "", UpdatedAt: ""},
		"a last editor that is too long": {
			CreatedBy: "", CreatedAt: "", UpdatedBy: strings.Repeat("m", bdoc.MaxUserBytes+1), UpdatedAt: "",
		},
		"a last edit time with a fraction": {CreatedBy: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: "2031-01-01T00:00:00.5Z"},
	} {
		// Encoded without validation, as a client can send anything.
		document, err := bdoc.Decode(builderDocument(t, "refused"))
		if err != nil {
			t.Fatalf("decoding a document: %v", err)
		}

		document.SetProvenance(claim)

		recorder, _ := postBuilderDraft(t, harness, builderTestPeer, map[string]any{"document": document})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want %d: %s", name, recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
		}
	}

	if got := harness.store.Count(bapi.NamespaceDrafts); got != drafts {
		t.Fatalf("the refused requests made %d drafts", got-drafts)
	}
}

// publishedBuilderDiagram publishes a new diagram with one device as topology
// name, as the owner, and returns its published document as the editor
// opens it: its ID and its bytes.
func publishedBuilderDiagram(t *testing.T, harness *builderHarness, name string) (string, []byte) {
	t.Helper()

	document := bdoc.NewDocument(name)
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	publishBuilderDraft(t, harness, draft, `{"mode":"topology","topology":{"name":"`+name+`","action":"create"}}`, http.StatusOK)

	reference := builderTopologyReference(t, harness, name)

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/documents/" + reference.ID, user: builderTestPeer,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("opening document %s: status = %d: %s", reference.ID, recorder.Code, recorder.Body)
	}

	var response builderDocumentResponse

	harness.decode(recorder, &response)

	if response.Digest != reference.Digest {
		t.Fatalf("document %s has digest %s, want the referenced %s", reference.ID, response.Digest, reference.Digest)
	}

	return reference.ID, response.Document
}

// TestBuilderEditAsDraftKeepsThePublishedDocument opens a published
// diagram as a draft, as another user. Sent back unchanged, the draft holds
// the published document itself: its digest, its creator and its last editor,
// so opening is not an edit, and publishing it again stores no new document.
// A save, or a body that is not that document, is the caller's edit.
func TestBuilderEditAsDraftKeepsThePublishedDocument(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	harness := newBuilderHarness(t)
	id, published := publishedBuilderDiagram(t, harness, "lab")
	reference := builderTopologyReference(t, harness, "lab")
	original := builderStamp(t, published)

	if original.CreatedBy != builderTestOwner || original.UpdatedBy != builderTestOwner {
		t.Fatalf("the published document holds %+v, want it made and saved by %s", original, builderTestOwner)
	}

	// Compact, as an editor sends back what it parsed.
	recorder, draft := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": json.RawMessage(builderCompactJSON(t, published)), "sourceToken": builderDocTokenPrefix + id,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("opening the published diagram as a draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	if draft.Digest != reference.Digest || draft.Stamp == nil || *draft.Stamp != original || draft.Owner != builderTestPeer {
		t.Fatalf("draft = digest %s, stamp %+v, owner %s; want the published %s and %+v, owned by %s",
			draft.Digest, draft.Stamp, draft.Owner, reference.Digest, original, builderTestPeer)
	}

	read := getBuilderDraft(t, harness, builderTestPeer, draft)

	if string(read.Document) != string(published) {
		t.Fatalf("the draft does not hold the published document's bytes:\n%s", read.Document)
	}

	// Publishing it unchanged finds the topology already holds it.
	documents := harness.store.Count(bapi.NamespacePublished)
	writes := harness.configWrites

	recorder = harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/publish",
		body: update, user: builderTestPeer, ifMatch: draft.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("publishing the unchanged draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	var response builderPublishResponse

	harness.decode(recorder, &response)

	if response.Stages[1].Status != bapi.PublishSkipped || harness.configWrites != writes ||
		harness.store.Count(bapi.NamespacePublished) != documents || builderTopologyReference(t, harness, "lab") != reference {
		t.Fatalf("stages = %#v with %d config writes and %d documents; want nothing written",
			response.Stages, harness.configWrites-writes, harness.store.Count(bapi.NamespacePublished))
	}

	if strings.Contains(recorder.Body.String(), `"stamp"`) {
		t.Fatalf("publishing returned a stamp, though it stored no snapshot: %s", recorder.Body)
	}

	// A save is the opener's edit of the creator's diagram.
	saved := saveBuilderDraft(t, harness, builderTestPeer, response.Draft, read.Document)

	want := bdoc.Provenance{
		CreatedBy: original.CreatedBy, CreatedAt: original.CreatedAt, UpdatedBy: builderTestPeer,
		UpdatedAt: bdoc.FormatTime(saved.Updated),
	}

	if saved.Stamp == nil || *saved.Stamp != want || saved.Digest == reference.Digest {
		t.Fatalf("stamp of the save = %+v (digest unchanged %t), want %+v", saved.Stamp, saved.Digest == reference.Digest, want)
	}

	// A body that is not the published document is the caller's edit from
	// the start, with the creator the body names.
	changed, err := bdoc.Decode(published)
	if err != nil {
		t.Fatalf("decoding the published document: %v", err)
	}

	changed.Metadata.Description = "changed before the draft was made"

	recorder, edited := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": changed, "sourceToken": builderDocTokenPrefix + id,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("opening a changed copy as a draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	want.UpdatedAt = bdoc.FormatTime(edited.Updated)

	if edited.Stamp == nil || *edited.Stamp != want || edited.Digest == reference.Digest {
		t.Fatalf("stamp of the changed copy = %+v (digest unchanged %t), want %+v",
			edited.Stamp, edited.Digest == reference.Digest, want)
	}

	// Without the token the same bytes are an upload: the caller's save.
	recorder, uploaded := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": json.RawMessage(published),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("uploading the published document: status = %d: %s", recorder.Code, recorder.Body)
	}

	want.UpdatedAt = bdoc.FormatTime(uploaded.Updated)

	if uploaded.Stamp == nil || *uploaded.Stamp != want || uploaded.Digest == reference.Digest {
		t.Fatalf("stamp of the upload = %+v (digest unchanged %t), want %+v",
			uploaded.Stamp, uploaded.Digest == reference.Digest, want)
	}
}

// TestBuilderFileDraftKeepsTheFileDocument opens the Builder file a
// topology names as a draft. Sent back unchanged, the draft holds the file's
// document under the file's digest, with whatever the file says of its
// creator, also when it says nothing. A changed body is the caller's edit.
func TestBuilderFileDraftKeepsTheFileDocument(t *testing.T) {
	harness := newBuilderHarness(t)

	// A file made by hand names nobody.
	plain := builderDocument(t, "plain")
	harness.addFileTopology("plain", bapi.DocumentReference{
		Path: harness.writeBuilderFile(builderYAML(t, plain), "plain.builder.yaml"),
	})

	// One exported from another server names its users.
	elsewhere := bdoc.Provenance{
		CreatedBy: "carol@elsewhere", CreatedAt: "2019-05-06T07:08:09Z", UpdatedBy: "dave@elsewhere", UpdatedAt: "2020-01-02T03:04:05Z",
	}
	exported := builderClaim(t, builderDocument(t, "exported"), elsewhere)
	harness.addFileTopology("exported", bapi.DocumentReference{
		Path: harness.writeBuilderFile(exported, "exported.builder.json"),
	})

	for name, want := range map[string]bdoc.Provenance{
		"plain":    {CreatedBy: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
		"exported": elsewhere,
	} {
		opened := openBuilderFile(t, harness, name)

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("%s: opening the file as a draft: status = %d: %s", name, recorder.Code, recorder.Body)
		}

		// The stamp is there also when it is empty: it says the document has
		// none of the four.
		if draft.Digest != opened.digest || draft.Stamp == nil || *draft.Stamp != want ||
			!strings.Contains(recorder.Body.String(), `"stamp"`) {
			t.Fatalf("%s: draft = digest %s, stamp %+v; want the file's %s and %+v",
				name, draft.Digest, draft.Stamp, opened.digest, want)
		}

		if got := builderStamp(t, getBuilderDraft(t, harness, builderTestOwner, draft).Document); got != want {
			t.Fatalf("%s: the draft's document holds %+v, want the file's %+v", name, got, want)
		}

		// A save writes the last edit, and no creator the file did not name.
		saved := saveBuilderDraft(t, harness, builderTestOwner, draft, []byte(builderJSON(t, opened.document)))

		edit := bdoc.Provenance{
			CreatedBy: want.CreatedBy, CreatedAt: want.CreatedAt, UpdatedBy: builderTestOwner,
			UpdatedAt: bdoc.FormatTime(saved.Updated),
		}

		if saved.Stamp == nil || *saved.Stamp != edit {
			t.Fatalf("%s: stamp of the save = %+v, want %+v", name, saved.Stamp, edit)
		}

		// A changed body is stamped when the draft is made.
		opened.document.Metadata.Description = "changed before the draft was made"

		recorder, changed := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("%s: opening a changed copy as a draft: status = %d: %s", name, recorder.Code, recorder.Body)
		}

		if want.CreatedBy == "" {
			edit.CreatedBy, edit.CreatedAt = builderTestOwner, bdoc.FormatTime(changed.Updated)
		}

		edit.UpdatedAt = bdoc.FormatTime(changed.Updated)

		if changed.Stamp == nil || *changed.Stamp != edit || changed.Digest == opened.digest {
			t.Fatalf("%s: stamp of the changed copy = %+v (digest unchanged %t), want %+v",
				name, changed.Stamp, changed.Digest == opened.digest, edit)
		}
	}
}

// TestBuilderForkIsTheForkersSave saves a draft as a new one. The fork's
// document is what the forking user's editor holds, so its last editor is
// that user, also when it is the very document the forked draft was opened
// from, and its creator is the one the body names.
func TestBuilderForkIsTheForkersSave(t *testing.T) {
	harness := newBuilderHarness(t)
	id, published := publishedBuilderDiagram(t, harness, "lab")
	reference := builderTopologyReference(t, harness, "lab")
	original := builderStamp(t, published)

	recorder, opened := postBuilderDraft(t, harness, builderTestOwner, map[string]any{
		"document": json.RawMessage(published), "sourceToken": builderDocTokenPrefix + id, "sourceFile": "",
	})
	if recorder.Code != http.StatusCreated || opened.Digest != reference.Digest {
		t.Fatalf("opening the published diagram: status = %d, digest %s: %s", recorder.Code, opened.Digest, recorder.Body)
	}

	recorder, fork := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": json.RawMessage(published), "forkOf": opened.Owner + "/" + opened.ID,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("forking the draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	want := bdoc.Provenance{
		CreatedBy: original.CreatedBy, CreatedAt: original.CreatedAt, UpdatedBy: builderTestPeer,
		UpdatedAt: bdoc.FormatTime(fork.Updated),
	}

	if fork.Stamp == nil || *fork.Stamp != want || fork.Digest == reference.Digest ||
		fork.SourceToken != opened.SourceToken || fork.Owner != builderTestPeer {
		t.Fatalf("fork = stamp %+v, token %q, owner %s (digest unchanged %t); want %+v",
			fork.Stamp, fork.SourceToken, fork.Owner, fork.Digest == reference.Digest, want)
	}

	// A fork of a document that names no creator is the forker's.
	recorder, bare := postBuilderDraft(t, harness, builderTestPeer, map[string]any{
		"document": json.RawMessage(builderDocument(t, "bare")), "forkOf": opened.Owner + "/" + opened.ID,
	})
	if recorder.Code != http.StatusCreated || bare.Stamp == nil || bare.Stamp.CreatedBy != builderTestPeer ||
		bare.Stamp.UpdatedBy != builderTestPeer {
		t.Fatalf("forking with a document that names nobody: status = %d, stamp %+v", recorder.Code, bare.Stamp)
	}
}

// TestBuilderPublishAfterUndoPublishesTheOlderStamp publishes a draft
// after an undo. The published document is the older snapshot as it was
// stored: it names the user who saved that content, and when, not the user
// who published it or undid the later save.
func TestBuilderPublishAfterUndoPublishesTheOlderStamp(t *testing.T) {
	fixture := newBuilderShareFixture(t)
	harness := fixture.harness

	fixture.share(builderTestPeer + ":edit")

	draft := newBuilderDraftResponse(fixture.meta())
	first := builderStamp(t, getBuilderDraft(t, harness, builderTestOwner, draft).Document)

	saved := saveBuilderDraft(t, harness, builderTestPeer, draft, builderDocument(t, "later"))

	recorder := harness.do(builderRequest{
		method: http.MethodPatch, path: fixture.path + "/cursor", body: `{"index":0}`,
		user: builderTestPeer, ifMatch: saved.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("undo: status = %d: %s", recorder.Code, recorder.Body)
	}

	var undone builderDraftResponse

	harness.decode(recorder, &undone)

	recorder = harness.do(builderRequest{
		method: http.MethodPost, path: fixture.path + "/publish",
		body: `{"mode":"topology","topology":{"name":"shared","action":"create"}}`,
		user: builderTestPeer, ifMatch: undone.ETag,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish: status = %d: %s", recorder.Code, recorder.Body)
	}

	reference := builderTopologyReference(t, harness, "shared")

	record, data, err := harness.service.GetPublishedDocumentData(context.Background(), reference.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	if got := builderStamp(t, data); got != first || got.UpdatedBy != builderTestOwner {
		t.Fatalf("the published document holds %+v, want the first snapshot's %+v", got, first)
	}

	// The record of the publication is another fact: who published, and when.
	published, err := time.Parse(bdoc.TimeLayout, first.UpdatedAt)
	if err != nil {
		t.Fatalf("parsing %q: %v", first.UpdatedAt, err)
	}

	if record.CreatedBy != builderTestPeer || !record.CreatedAt.After(published) || record.Digest != undone.Digest {
		t.Fatalf("published record = by %s at %s with digest %s; want %s, later than %s, with the snapshot's %s",
			record.CreatedBy, record.CreatedAt, record.Digest, builderTestPeer, first.UpdatedAt, undone.Digest)
	}
}

// TestBuilderDraftSourceFile creates drafts that name the uploaded file
// they were made from. The name is stored as given and returned wherever a
// draft is, a name that is not a file name refuses the request, and a fork
// records none.
func TestBuilderDraftSourceFile(t *testing.T) {
	const name = "Pump station (rev 2).builder.yaml"

	harness := newBuilderHarness(t)
	document := json.RawMessage(builderDocument(t, "uploaded"))

	recorder, draft := postBuilderDraft(t, harness, builderTestOwner, map[string]any{
		"document": document, "sourceFile": name,
	})
	if recorder.Code != http.StatusCreated || draft.SourceFile != name {
		t.Fatalf("creating the draft: status = %d, source file %q: %s", recorder.Code, draft.SourceFile, recorder.Body)
	}

	if read := getBuilderDraft(t, harness, builderTestOwner, draft); read.SourceFile != name {
		t.Fatalf("GET of the draft: source file = %q, want %q", read.SourceFile, name)
	}

	if saved := saveBuilderDraft(t, harness, builderTestOwner, draft, document); saved.SourceFile != name {
		t.Fatalf("the save: source file = %q, want %q", saved.SourceFile, name)
	}

	// A draft made without one has none, and the response leaves the key out.
	recorder, plain := postBuilderDraft(t, harness, builderTestOwner, map[string]any{"document": document})
	if recorder.Code != http.StatusCreated || plain.SourceFile != "" || strings.Contains(recorder.Body.String(), "sourceFile") {
		t.Fatalf("a draft without a source file: status = %d: %s", recorder.Code, recorder.Body)
	}

	var listing struct {
		Drafts []builderDraftResponse `json:"drafts"`
	}

	harness.decode(harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts", user: builderTestOwner,
	}), &listing)

	files := map[string]string{}
	for _, listed := range listing.Drafts {
		files[listed.ID] = listed.SourceFile
	}

	if len(files) != 2 || files[draft.ID] != name || files[plain.ID] != "" {
		t.Fatalf("listed source files = %v, want %q for %s only", files, name, draft.ID)
	}

	// A fork records none: neither the forked draft's, nor one it names.
	forkOf := draft.Owner + "/" + draft.ID

	for _, fields := range []map[string]any{
		{"document": document, "forkOf": forkOf},
		{"document": document, "forkOf": forkOf, "sourceFile": "fork.json"},
	} {
		recorder, fork := postBuilderDraft(t, harness, builderTestOwner, fields)
		if recorder.Code != http.StatusCreated || fork.SourceFile != "" || strings.Contains(recorder.Body.String(), "sourceFile") {
			t.Fatalf("fork %v: status = %d, source file %q", fields["sourceFile"], recorder.Code, fork.SourceFile)
		}
	}

	drafts := harness.store.Count(bapi.NamespaceDrafts)

	for label, invalid := range map[string]string{
		"a slash":             "exports/pump.json",
		"an absolute path":    "/phenix/pump.json",
		"a backslash":         `C:\exports\pump.json`,
		"the parent":          "..",
		"the directory":       ".",
		"a parent path":       "../pump.json",
		"a control character": "pump\n.json",
		"a NUL":               "pump\x00.json",
		"256 bytes":           strings.Repeat("p", bapi.MaxSourceFileLength+1),
	} {
		for _, fields := range []map[string]any{
			{"document": document, "sourceFile": invalid},
			{"document": document, "sourceFile": invalid, "forkOf": forkOf},
		} {
			recorder, _ := postBuilderDraft(t, harness, builderTestOwner, fields)

			var refusal builderErrorBody

			harness.decode(recorder, &refusal)

			if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(refusal.Message+refusal.Cause, "sourceFile") {
				t.Errorf("%s (fork %t): status = %d, said %+v; want %d naming sourceFile",
					label, fields["forkOf"] != nil, recorder.Code, refusal, http.StatusUnprocessableEntity)
			}
		}
	}

	if got := harness.store.Count(bapi.NamespaceDrafts); got != drafts {
		t.Fatalf("the refused requests made %d drafts", got-drafts)
	}

	// The longest name is accepted, and one that only looks like a path
	// element is a file name.
	for _, valid := range []string{strings.Repeat("p", bapi.MaxSourceFileLength), "..pump.json", "pump..", "Wasserwerk Süd.yml"} {
		recorder, made := postBuilderDraft(t, harness, builderTestOwner, map[string]any{
			"document": document, "sourceFile": valid,
		})
		if recorder.Code != http.StatusCreated || made.SourceFile != valid {
			t.Errorf("%q: status = %d, source file %q: %s", valid, recorder.Code, made.SourceFile, recorder.Body)
		}
	}
}
