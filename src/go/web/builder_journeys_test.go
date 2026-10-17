package web

import (
	"context"
	"net/http"
	"slices"
	"testing"
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
	}) || len(listing.Damaged) != 1 || listing.Damaged[0].ID != damaged.ID || listing.Damaged[0].CreatedBy != builderAlice {
		t.Fatalf("bob lists %+v, want alice's draft and her damaged draft", listing)
	}

	var opened, saved builderDraftResponse

	bob.doDecode(builderRequest{method: http.MethodGet, path: path}, http.StatusOK, &opened)

	bob.doDecode(builderRequest{
		method: http.MethodPost, path: path + "/snapshots", ifMatch: opened.ETag,
		body: `{"document":` + string(builderDocument(t, "edited")) + `}`,
	}, http.StatusCreated, &saved)

	bob.mustDo("bob deleting alice's damaged draft", builderRequest{
		method: http.MethodDelete, path: "/builder/drafts/" + damaged.ID, ifMatch: damagedETag,
	}, http.StatusNoContent)

	if _, _, err := harness.service.AddIcon(context.Background(), builderAlice, "plc", builderIconPNG(t, 1, 1, 31)); err != nil {
		t.Fatalf("adding alice's icon: %v", err)
	}

	bob.mustDo("bob renaming alice's icon", builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`,
	}, http.StatusOK)
	bob.mustDo("bob deleting alice's icon", builderRequest{
		method: http.MethodDelete, path: builderIconsRoute + "/plc-2",
	}, http.StatusNoContent)

	var seen builderDraftResponse

	alice.doDecode(builderRequest{method: http.MethodGet, path: path}, http.StatusOK, &seen)

	if seen.ETag != saved.ETag || seen.Snapshots != 2 || seen.CreatedBy != builderAlice ||
		seen.LastModifiedBy != builderBob {
		t.Fatalf("alice opened %+v, want bob's version of her draft", seen)
	}

	bob.mustDo("bob deleting alice's draft", builderRequest{
		method: http.MethodDelete, path: path, ifMatch: saved.ETag,
	}, http.StatusNoContent)

	alice.mustDo("alice after bob deleted her draft", builderRequest{method: http.MethodGet, path: path}, http.StatusNotFound)
}
