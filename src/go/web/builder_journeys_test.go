package web

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestBuilderJourneyBuilderUsersShareEverything follows alice and bob, who
// see and change each other's drafts and icons.
func TestBuilderJourneyBuilderUsersShareEverything(t *testing.T) {
	harness := newBuilderHarness(t)

	var (
		alice = harness.as(builderAlice, builderUserRole())
		bob   = harness.as(builderBob, builderUserRole())
		draft builderDraftResponse
	)

	alice.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(builderDocument(t, "plant")) + `}`,
	}, http.StatusCreated, &draft)

	path := "/builder/drafts/" + draft.ID

	alice.mustDo("alice publishing her draft as a topology", builderRequest{
		method: http.MethodPost, path: path + "/publish", ifMatch: draft.ETag,
		body: `{"mode":"topology","topology":{"name":"plant","action":"create"}}`,
	}, http.StatusOK)

	damaged := harness.createDraft(builderAlice, "damaged")
	damagedETag := harness.damageDraft(damaged.ID)

	var listing struct {
		Drafts  []builderDraftResponse        `json:"drafts"`
		Damaged []builderDamagedDraftResponse `json:"damaged"`
	}

	bob.doDecode(builderRequest{
		method: http.MethodGet, path: "/builder/drafts",
	}, http.StatusOK, &listing)

	if !slices.ContainsFunc(listing.Drafts, func(listed builderDraftResponse) bool {
		return listed.ID == draft.ID && listed.CreatedBy == builderAlice
	}) {
		t.Errorf("GET /builder/drafts as bob lists %+v, want alice's draft %s among them", listing.Drafts, draft.ID)
	}

	wantDamaged := []builderDamagedDraftResponse{{ID: damaged.ID, CreatedBy: builderAlice, ETag: damagedETag}}
	if diff := cmp.Diff(wantDamaged, listing.Damaged,
		cmpopts.IgnoreFields(builderDamagedDraftResponse{}, "Title", "Updated")); diff != "" {
		t.Errorf("GET /builder/drafts as bob: damaged drafts mismatch (-want +got):\n%s", diff)
	}

	var opened, saved builderDraftResponse

	bob.doDecode(builderRequest{method: http.MethodGet, path: path}, http.StatusOK, &opened)

	bob.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", ifMatch: opened.ETag,
		body: `{"document":` + string(builderDocument(t, "edited")) + `}`,
	}, http.StatusCreated, &saved)

	if recorder := bob.do(builderRequest{
		method: http.MethodDelete, path: "/builder/drafts/" + damaged.ID, ifMatch: damagedETag,
	}); recorder.Code != http.StatusNoContent {
		t.Errorf("DELETE /builder/drafts/%s (alice's damaged draft) as bob: status = %d, want %d: %s",
			damaged.ID, recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if _, _, err := harness.service.AddIcon(context.Background(), builderAlice, "plc", builderIconPNG(t, 1, 1, 31)); err != nil {
		t.Fatalf("adding alice's icon: %v", err)
	}

	var renamed builderIconResponse

	bob.doDecode(builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`,
	}, http.StatusOK, &renamed)

	if recorder := bob.do(
		builderRequest{method: http.MethodDelete, path: builderIconsRoute + "/plc-2", ifMatch: renamed.ETag},
	); recorder.Code != http.StatusNoContent {
		t.Errorf("DELETE %s/plc-2 (alice's icon) as bob: status = %d, want %d: %s",
			builderIconsRoute, recorder.Code, http.StatusNoContent, recorder.Body)
	}

	var seen builderDraftResponse

	alice.doDecode(builderRequest{method: http.MethodGet, path: path}, http.StatusOK, &seen)

	type version struct {
		ETag, CreatedBy, LastModifiedBy string
		Snapshots                       int
	}

	// Bob's version of her draft.
	want := version{ETag: saved.ETag, CreatedBy: builderAlice, LastModifiedBy: builderBob, Snapshots: 2}
	got := version{ETag: seen.ETag, CreatedBy: seen.CreatedBy, LastModifiedBy: seen.LastModifiedBy, Snapshots: seen.Snapshots}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET %s as alice mismatch (-want +got):\n%s", path, diff)
	}

	bob.mustDo("bob deleting alice's draft", builderRequest{
		method: http.MethodDelete, path: path, ifMatch: saved.ETag,
	}, http.StatusNoContent)

	if recorder := alice.do(builderRequest{method: http.MethodGet, path: path}); recorder.Code != http.StatusNotFound {
		t.Errorf("GET %s as alice after bob deleted it: status = %d, want %d: %s",
			path, recorder.Code, http.StatusNotFound, recorder.Body)
	}
}
