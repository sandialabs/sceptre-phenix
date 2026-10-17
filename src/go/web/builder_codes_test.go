package web

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/web/weberror"
)

// TestBuilderErrorsCarryCodes answers each refused request with the code of
// its failure, and a document that does not validate with its issues.
func TestBuilderErrorsCarryCodes(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderAlice, "codes")
	path := "/builder/drafts/" + draft.ID

	document := bdoc.NewDocument("codes")
	document.Metadata.ID = "not-a-uuid"
	invalid := builderRequest{method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + builderJSON(t, document) + `}`}

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "a draft that does not exist", user: builderAlice,
			request: builderRequest{method: http.MethodGet, path: "/builder/drafts/missing"},
			status:  http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a body that is not JSON", user: builderAlice,
			request: builderRequest{method: http.MethodPost, path: "/builder/drafts", body: "nope"},
			status:  http.StatusBadRequest, code: bdoc.CodeRequestInvalid,
		},
		{
			name: "a document that does not validate", user: builderAlice, request: invalid,
			status: http.StatusUnprocessableEntity, code: bdoc.CodeDocumentInvalid,
		},
		{
			name: "a draft at another version", user: builderAlice,
			request: builderRequest{method: http.MethodDelete, path: path, ifMatch: `"999"`},
			status:  http.StatusPreconditionFailed, code: bdoc.CodeDraftStale,
		},
		{
			name: "the current version deleted", user: builderAlice,
			request: builderRequest{
				method: http.MethodDelete, path: path + "/snapshots/" + builderCurrentSnapshot, ifMatch: draft.ETag,
			},
			status: http.StatusConflict, code: bdoc.CodeDraftSnapshotCurrent,
		},
	})

	var body builderErrorBody

	harness.as(builderAlice, nil).doDecode(invalid, http.StatusUnprocessableEntity, &body)

	if !slices.ContainsFunc(body.Issues, func(issue bdoc.Issue) bool {
		return issue.Code == bdoc.CodeMetadataIDInvalid && issue.Path == "metadata.id" && issue.Severity == bdoc.SeverityError
	}) {
		t.Fatalf("issues = %+v, want the document ID refused with its code", body.Issues)
	}

	// A draft that changed between a read and a write is a conflict about the
	// draft; any other conflict keeps the generic code.
	draftConflict := builderCodedError(builderWebError(
		&bapi.ConflictError{Kind: bapi.KindDraft, ID: draft.ID, Expected: 1, Actual: 2, Reason: ""}, "saving",
	))
	iconConflict := builderCodedError(builderWebError(
		&bapi.ConflictError{Kind: "icon", ID: "plc", Expected: 1, Actual: 2, Reason: ""}, "renaming",
	))

	for err, want := range map[error]bdoc.Code{draftConflict: bdoc.CodeDraftConflict, iconConflict: bdoc.CodeRequestConflict} {
		var web *weberror.WebError

		if !errors.As(err, &web) || web.Status != http.StatusConflict || web.Code != string(want) {
			t.Errorf("error %v: answer %+v, want 409 with code %s", err, web, want)
		}
	}
}

// TestBuilderMethodNotAllowedCarriesCode answers a method no Builder route
// takes with 405, Allow and a code; another route keeps the status alone.
func TestBuilderMethodNotAllowedCarriesCode(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.api.Handle("/configs/{kind}/{name}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods(http.MethodGet)

	recorder := harness.do(builderRequest{method: http.MethodPatch, path: "/builder/drafts", user: builderAlice})

	var body builderErrorBody

	harness.decode(recorder, &body)

	if recorder.Code != http.StatusMethodNotAllowed || body.Code != string(bdoc.CodeRequestMethodNotAllowed) ||
		body.Message == "" || recorder.Header().Get("Allow") != "GET, POST, OPTIONS" ||
		recorder.Header().Get("Content-Type") != mimeJSON {
		t.Fatalf("PATCH /builder/drafts = %d %v %s, want 405, Allow GET, POST, OPTIONS and the code %s",
			recorder.Code, recorder.Header(), recorder.Body, bdoc.CodeRequestMethodNotAllowed)
	}

	other := harness.do(builderRequest{method: http.MethodPatch, path: "/configs/x/x", user: builderAlice})

	if other.Code != http.StatusMethodNotAllowed || other.Body.Len() != 0 || other.Header().Get("Allow") != "" {
		t.Fatalf("PATCH /configs/x/x = %d %v %q, want 405 alone", other.Code, other.Header(), other.Body)
	}
}

// TestNonBuilderErrorsCarryNoCode asserts a route outside the Builder
// answers without the code and issues keys of the Builder's errors.
func TestNonBuilderErrorsCarryNoCode(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/configs/Topology/missing", user: builderAlice,
	})
	if recorder.Code < http.StatusBadRequest {
		t.Fatalf("status = %d, want an error: %s", recorder.Code, recorder.Body)
	}

	var body map[string]any

	harness.decode(recorder, &body)

	if _, said := body["message"]; !said {
		t.Fatalf("answer %v has no message", body)
	}

	for _, key := range []string{"code", "issues"} {
		if _, found := body[key]; found {
			t.Errorf("a route outside the Builder answered with %q: %s", key, recorder.Body)
		}
	}

	// A Builder route of the same server names its code.
	builder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/missing", user: builderAlice,
	})

	harness.decode(builder, &body)

	if body["code"] != string(bdoc.CodeRequestNotFound) {
		t.Fatalf("a Builder route answered %s, want the code %s", builder.Body, bdoc.CodeRequestNotFound)
	}
}

// TestBuilderPublishRefusalsCarryCodes asserts the publish refusals the
// Publish dialog tells apart carry their codes.
func TestBuilderPublishRefusalsCarryCodes(t *testing.T) {
	harness := newBuilderHarness(t)

	first := bdoc.NewDocument("codes")
	addExportHost(first, "aa", map[string]any{"name": "eth0", "vlan": "EXP"})

	second := bdoc.NewDocument("codes")
	addExportHost(second, "bb", map[string]any{"name": "eth0", "vlan": "EXP"})

	const create = `{"mode":"topology","topology":{"name":"codes","action":"create"}}`

	publishBuilderDraftAs(t, harness, createBuilderPublishDraft(t, harness, first), nil, create, http.StatusOK)

	other := createBuilderPublishDraft(t, harness, second)

	for _, tt := range []struct {
		body   string
		status int
		code   bdoc.Code
	}{
		{body: create, status: http.StatusConflict, code: bdoc.CodePublishTopologyExists},
		{
			body:   `{"mode":"topology","topology":{"name":"absent","action":"update"}}`,
			status: http.StatusConflict, code: bdoc.CodePublishTopologyMissing,
		},
		{
			body:   `{"mode":"topology","topology":{"name":"codes","action":"update"}}`,
			status: http.StatusConflict, code: bdoc.CodePublishTopologyNotSource,
		},
		{
			body:   `{"mode":"topology","topology":{"name":"two words","action":"create"}}`,
			status: http.StatusBadRequest, code: bdoc.CodePublishTargetInvalid,
		},
	} {
		_, refusal := publishBuilderDraftAs(t, harness, other, nil, tt.body, tt.status)

		if refusal.Code != string(tt.code) {
			t.Errorf("publish %s: refusal = %+v, want code %s", tt.body, refusal, tt.code)
		}
	}
}
