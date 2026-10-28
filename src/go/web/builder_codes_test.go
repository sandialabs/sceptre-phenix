package web

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

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
		t.Errorf("POST /builder/drafts issues = %+v, want the document ID refused with its code", body.Issues)
	}

	// A draft that changed between a read and a write is a conflict about the
	// draft. Any other conflict keeps the generic code.
	draftConflict := builderCodedError(builderWebError(
		&bapi.ConflictError{Kind: bapi.KindDraft, ID: draft.ID, Expected: 1, Actual: 2, Reason: ""}, "saving",
	))
	iconConflict := builderCodedError(builderWebError(
		&bapi.ConflictError{Kind: "icon", ID: "plc", Expected: 1, Actual: 2, Reason: ""}, "renaming",
	))

	for _, tt := range []struct {
		name string
		err  error
		want bdoc.Code
	}{
		{name: "draft", err: draftConflict, want: bdoc.CodeDraftConflict},
		{name: "icon", err: iconConflict, want: bdoc.CodeRequestConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var web *weberror.WebError
			if !errors.As(tt.err, &web) {
				t.Fatalf("builderCodedError(%v) = %T, want a *weberror.WebError", tt.err, tt.err)
			}

			got := builderAnswer{Status: web.Status, Code: web.Code}
			want := builderAnswer{Status: http.StatusConflict, Code: string(tt.want)}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("builderCodedError(%v) mismatch (-want +got):\n%s", tt.err, diff)
			}
		})
	}
}

// builderAnswer is the part of an answer a test of its code compares.
type builderAnswer struct {
	Status      int
	Code        string
	Allow       string
	ContentType string
	Body        string
}

// TestBuilderMethodNotAllowedCarriesCode answers a method no Builder route
// takes with 405, Allow and a code. Another route keeps the status alone.
func TestBuilderMethodNotAllowedCarriesCode(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.api.Handle("/configs/{kind}/{name}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods(http.MethodGet)

	recorder := harness.do(builderRequest{method: http.MethodPatch, path: "/builder/drafts", user: builderAlice})

	var body builderErrorBody

	harness.decode(recorder, &body)

	got := builderAnswer{
		Status: recorder.Code, Code: body.Code, Allow: recorder.Header().Get("Allow"),
		ContentType: recorder.Header().Get("Content-Type"),
	}
	want := builderAnswer{
		Status: http.StatusMethodNotAllowed, Code: string(bdoc.CodeRequestMethodNotAllowed), Allow: "GET, POST, OPTIONS",
		ContentType: mimeJSON,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PATCH /builder/drafts mismatch (-want +got):\n%s", diff)
	}

	if body.Message == "" {
		t.Errorf("PATCH /builder/drafts message = %q, want one", body.Message)
	}

	other := harness.do(builderRequest{method: http.MethodPatch, path: "/configs/x/x", user: builderAlice})

	got = builderAnswer{Status: other.Code, Allow: other.Header().Get("Allow"), Body: other.Body.String()}
	want = builderAnswer{Status: http.StatusMethodNotAllowed}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PATCH /configs/x/x mismatch (-want +got):\n%s", diff)
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
		t.Fatalf("GET /configs/Topology/missing: status = %d, want an error: %s", recorder.Code, recorder.Body)
	}

	var body map[string]any

	harness.decode(recorder, &body)

	if _, said := body["message"]; !said {
		t.Errorf("GET /configs/Topology/missing = %v, want a message", body)
	}

	for _, key := range []string{"code", "issues"} {
		if _, found := body[key]; found {
			t.Errorf("GET /configs/Topology/missing = %s, want no %q", recorder.Body, key)
		}
	}

	// A Builder route of the same server names its code.
	builder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/drafts/missing", user: builderAlice,
	})

	var builderBody map[string]any

	harness.decode(builder, &builderBody)

	if builderBody["code"] != string(bdoc.CodeRequestNotFound) {
		t.Errorf("GET /builder/drafts/missing = %s, want the code %s", builder.Body, bdoc.CodeRequestNotFound)
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
		name   string
		body   string
		status int
		code   bdoc.Code
	}{
		{name: "topology exists", body: create, status: http.StatusConflict, code: bdoc.CodePublishTopologyExists},
		{
			name:   "topology missing",
			body:   `{"mode":"topology","topology":{"name":"absent","action":"update"}}`,
			status: http.StatusConflict, code: bdoc.CodePublishTopologyMissing,
		},
		{
			name:   "topology not the source",
			body:   `{"mode":"topology","topology":{"name":"codes","action":"update"}}`,
			status: http.StatusConflict, code: bdoc.CodePublishTopologyNotSource,
		},
		{
			name:   "target invalid",
			body:   `{"mode":"topology","topology":{"name":"two words","action":"create"}}`,
			status: http.StatusBadRequest, code: bdoc.CodePublishTargetInvalid,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, refusal := publishBuilderDraftAs(t, harness, other, nil, tt.body, tt.status)

			if refusal.Code != string(tt.code) {
				t.Errorf("publish %s: refusal code = %q, want %q: %+v", tt.body, refusal.Code, tt.code, refusal)
			}
		})
	}
}
