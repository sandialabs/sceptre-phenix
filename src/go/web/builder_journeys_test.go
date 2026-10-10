package web

import (
	"net/http"
	"testing"
)

// TestBuilderJourneyDraftSharedWithEditorAndViewer follows one draft from
// its owner to two users it is shared with: bob, who may edit it, saves a
// version; carol, who may only view it, reads that version but may neither
// save, delete nor see whom it is shared with; and once the owner takes
// carol's share back she no longer sees the draft, while bob still does.
func TestBuilderJourneyDraftSharedWithEditorAndViewer(t *testing.T) {
	harness := newBuilderHarness(t)

	for _, user := range []string{builderTestOwner, builderTestPeer, builderShareCarol} {
		harness.setUser(user, builderShareCreated)
	}

	var (
		owner  = harness.as(builderTestOwner, builderConfigsRole())
		editor = harness.as(builderTestPeer, builderConfigsRole())
		viewer = harness.as(builderShareCarol, builderConfigsRole())
		draft  = harness.createDraft(builderTestOwner, "plant")
		path   = "/builder/drafts/" + builderTestOwner + "/" + draft.ID
	)

	editor.expect("bob before the draft is shared", builderRequest{method: http.MethodGet, path: path}, http.StatusNotFound)

	shares := owner.expect("reading whom the draft is shared with", builderRequest{
		method: http.MethodGet, path: path + "/shares",
	}, http.StatusOK)

	owner.expect("sharing the draft with bob to edit and carol to view", builderRequest{
		method: http.MethodPut, path: path + "/shares", ifMatch: shares.Header().Get("ETag"),
		body: builderShareBody(t, builderTestPeer+":edit", builderShareCarol+":view"),
	}, http.StatusOK)

	// Bob saves a version over the one he opened.
	var opened, saved builderDraftResponse

	harness.decode(editor.expect("bob opening the draft", builderRequest{
		method: http.MethodGet, path: path,
	}, http.StatusOK), &opened)

	harness.decode(editor.expect("bob saving a version", builderRequest{
		method: http.MethodPost, path: path + "/snapshots", ifMatch: opened.ETag,
		body: `{"document":` + string(builderDocument(t, "edited")) + `}`,
	}, http.StatusCreated), &saved)

	if opened.ReadOnly || opened.Access != "edit" || saved.Snapshots != 2 {
		t.Fatalf("bob opened %+v and saved %+v, want an editable draft and a second version", opened, saved)
	}

	// Carol reads bob's version, and nothing she may not do changes it.
	var seen builderDraftResponse

	harness.decode(viewer.expect("carol opening the draft", builderRequest{
		method: http.MethodGet, path: path,
	}, http.StatusOK), &seen)

	if !seen.ReadOnly || seen.Access != "view" || seen.Snapshots != 2 || seen.ETag != saved.ETag {
		t.Fatalf("carol opened %+v, want bob's version, read only", seen)
	}

	for _, refused := range []builderRequest{
		{
			method: http.MethodPost, path: path + "/snapshots", ifMatch: seen.ETag,
			body: `{"document":` + string(builderDocument(t, "viewed")) + `}`,
		},
		{method: http.MethodDelete, path: path, ifMatch: seen.ETag},
		{method: http.MethodGet, path: path + "/shares"},
	} {
		viewer.expect("carol: "+refused.method+" "+refused.path, refused, http.StatusForbidden)
	}

	// The owner sees who saved the version.
	var history struct {
		Snapshots []builderSnapshotResponse `json:"snapshots"`
	}

	harness.decode(owner.expect("the owner reading the history", builderRequest{
		method: http.MethodGet, path: path + "/snapshots",
	}, http.StatusOK), &history)

	if len(history.Snapshots) != 2 || history.Snapshots[1].CreatedBy != builderTestPeer {
		t.Fatalf("history = %+v, want bob's version last", history.Snapshots)
	}

	// The owner takes carol's share back.
	shares = owner.expect("reading whom the draft is shared with again", builderRequest{
		method: http.MethodGet, path: path + "/shares",
	}, http.StatusOK)

	owner.expect("taking carol's share back", builderRequest{
		method: http.MethodPut, path: path + "/shares", ifMatch: shares.Header().Get("ETag"),
		body: builderShareBody(t, builderTestPeer+":edit"),
	}, http.StatusOK)

	viewer.expect("carol after her share is taken back", builderRequest{method: http.MethodGet, path: path}, http.StatusNotFound)
	editor.expect("bob after carol's share is taken back", builderRequest{method: http.MethodGet, path: path}, http.StatusOK)
}

// TestBuilderJourneyConfigsRoleKeepsToOwnDrafts follows a user whose role
// holds every config verb and no permission on other users' drafts: she
// creates a draft and publishes it as a topology, while another user's draft
// is answered as one that does not exist on every route she tries, is not
// listed to her, and is left as it was.
func TestBuilderJourneyConfigsRoleKeepsToOwnDrafts(t *testing.T) {
	harness := newBuilderHarness(t)
	alice := harness.as(builderTestOwner, builderConfigsRole())

	var draft builderDraftResponse

	harness.decode(alice.expect("creating a draft", builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(builderDocument(t, "own")) + `}`,
	}, http.StatusCreated), &draft)

	alice.expect("publishing her draft as a topology", builderRequest{
		method: http.MethodPost, path: "/builder/drafts/" + builderTestOwner + "/" + draft.ID + "/publish", ifMatch: draft.ETag,
		body: `{"mode":"topology","topology":{"name":"own","action":"create"}}`,
	}, http.StatusOK)

	if _, err := harness.getConfig("Topology/own"); err != nil {
		t.Fatalf("publishing stored no topology: %v", err)
	}

	theirs := harness.createDraft(builderTestPeer, "theirs")
	path := "/builder/drafts/" + builderTestPeer + "/" + theirs.ID

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: path},
		{method: http.MethodGet, path: path + "/snapshots"},
		{
			method: http.MethodPost, path: path + "/snapshots", ifMatch: theirs.ETag,
			body: `{"document":` + string(builderDocument(t, "taken")) + `}`,
		},
		{
			method: http.MethodPost, path: path + "/publish", ifMatch: theirs.ETag,
			body: `{"mode":"topology","topology":{"name":"theirs","action":"create"}}`,
		},
		{method: http.MethodDelete, path: path, ifMatch: theirs.ETag},
	} {
		alice.expect(request.method+" "+request.path, request, http.StatusNotFound)
	}

	var listing builderShareListing

	harness.decode(alice.expect("listing drafts", builderRequest{
		method: http.MethodGet, path: "/builder/drafts",
	}, http.StatusOK), &listing)

	if len(listing.Drafts) != 1 || listing.Drafts[0].ID != draft.ID || len(listing.Shared) != 0 {
		t.Fatalf("listing = %+v, want her draft alone", listing)
	}

	if meta := mustDraftMeta(t, harness, theirs.ID); meta.ETag() != theirs.ETag || meta.Publication != nil {
		t.Fatalf("bob's draft is at %s with publication %+v, want it as it was created", meta.ETag(), meta.Publication)
	}

	if _, err := harness.getConfig("Topology/theirs"); err == nil {
		t.Fatal("a refused publication stored topology theirs")
	}
}
