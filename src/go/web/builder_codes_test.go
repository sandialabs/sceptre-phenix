package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/web/weberror"
)

// builderErrorBody is what a Builder route answers a refused request with.
type builderErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Cause   string       `json:"cause"`
	Issues  []bdoc.Issue `json:"issues"`
}

// TestBuilderErrorsCarryCodes asserts a refused Builder request is answered
// with the code of its failure, which the registry holds, and, for a document
// that does not validate, each issue with its code and where it is.
func TestBuilderErrorsCarryCodes(t *testing.T) {
	harness := newBuilderHarness(t)

	document := bdoc.NewDocument("codes")
	document.Metadata.ID = "not-a-uuid"

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the document: %v", err)
	}

	tests := []struct {
		name    string
		request builderRequest
		status  int
		code    bdoc.Code
	}{
		{
			name: "a draft that does not exist",
			request: builderRequest{
				method: http.MethodGet, path: "/builder/drafts/" + builderTestOwner + "/missing", user: builderTestOwner,
			},
			status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name:    "a body that is not JSON",
			request: builderRequest{method: http.MethodPost, path: "/builder/drafts", body: "nope", user: builderTestOwner},
			status:  http.StatusBadRequest, code: bdoc.CodeRequestInvalid,
		},
		{
			name: "a document that does not validate",
			request: builderRequest{
				method: http.MethodPost, path: "/builder/drafts", body: `{"document":` + string(data) + `}`, user: builderTestOwner,
			},
			status: http.StatusUnprocessableEntity, code: bdoc.CodeDocumentInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(tt.request)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			var body builderErrorBody

			harness.decode(recorder, &body)

			if _, known := bdoc.LookupCode(bdoc.Code(body.Code)); body.Code != string(tt.code) || !known || body.Message == "" {
				t.Fatalf("answer = %+v, want code %s and a message", body, tt.code)
			}

			if tt.code != bdoc.CodeDocumentInvalid {
				if body.Issues != nil {
					t.Fatalf("issues = %+v, want none", body.Issues)
				}

				return
			}

			if !slices.ContainsFunc(body.Issues, func(issue bdoc.Issue) bool {
				return issue.Code == bdoc.CodeMetadataIDInvalid && issue.Path == "metadata.id" &&
					issue.Severity == bdoc.SeverityError && issue.Message != ""
			}) {
				t.Fatalf("issues = %+v, want the document ID refused with its code", body.Issues)
			}
		})
	}
}

// TestBuilderDraftAnswersCarryDraftCodes asserts the answers about a draft
// a client tells apart carry codes of their own: a stale If-Match of the
// draft or of its share list, and the current version deleted, while a
// conflict that is not about a draft keeps the generic code.
func TestBuilderDraftAnswersCarryDraftCodes(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := harness.createDraft(builderTestOwner, "codes")
	path := "/builder/drafts/" + builderTestOwner + "/" + draft.ID

	for _, tt := range []struct {
		name    string
		request builderRequest
		status  int
		code    bdoc.Code
	}{
		{
			name: "a draft at another version",
			request: builderRequest{
				method: http.MethodDelete, path: path, user: builderTestOwner, ifMatch: `"999"`,
			},
			status: http.StatusPreconditionFailed, code: bdoc.CodeDraftStale,
		},
		{
			name: "the current version deleted",
			request: builderRequest{
				method: http.MethodDelete, path: path + "/snapshots/" + builderCurrentSnapshot,
				user: builderTestOwner, ifMatch: draft.ETag,
			},
			status: http.StatusConflict, code: bdoc.CodeDraftSnapshotCurrent,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.do(tt.request)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			var body builderErrorBody

			harness.decode(recorder, &body)

			if body.Code != string(tt.code) {
				t.Fatalf("answer = %+v, want code %s", body, tt.code)
			}
		})
	}

	// The share list's own entity tag, of a draft whose owner has a user
	// account to share it from.
	shares := newBuilderShareFixture(t)
	stale := shares.put(`"shares-99"`, builderShareBody(t))

	var refused builderErrorBody

	shares.harness.decode(stale, &refused)

	if stale.Code != http.StatusPreconditionFailed || refused.Code != string(bdoc.CodeDraftSharesStale) {
		t.Fatalf("a share list at another version = %d %s, want 412 with code %s",
			stale.Code, stale.Body, bdoc.CodeDraftSharesStale)
	}

	// A draft that changed between a handler's read and its write is a
	// conflict about the draft; any other conflict keeps the generic code.
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

// TestBuilderMethodNotAllowedCarriesCode asserts a method no Builder route
// takes is answered with 405, the methods the path takes in Allow and the
// code of the refusal, while another route keeps the status alone.
func TestBuilderMethodNotAllowedCarriesCode(t *testing.T) {
	harness := newBuilderHarness(t)
	harness.api.Handle("/configs/{kind}/{name}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods(http.MethodGet)

	recorder := harness.do(builderRequest{method: http.MethodPatch, path: "/builder/drafts", user: builderTestOwner})

	var body builderErrorBody

	harness.decode(recorder, &body)

	if recorder.Code != http.StatusMethodNotAllowed || body.Code != string(bdoc.CodeRequestMethodNotAllowed) ||
		body.Message == "" || recorder.Header().Get("Allow") != "GET, POST, OPTIONS" ||
		recorder.Header().Get("Content-Type") != mimeJSON {
		t.Fatalf("PATCH /builder/drafts = %d %v %s, want 405, Allow GET, POST, OPTIONS and the code %s",
			recorder.Code, recorder.Header(), recorder.Body, bdoc.CodeRequestMethodNotAllowed)
	}

	other := harness.do(builderRequest{method: http.MethodPatch, path: "/configs/x/x", user: builderTestOwner})

	if other.Code != http.StatusMethodNotAllowed || other.Body.Len() != 0 || other.Header().Get("Allow") != "" {
		t.Fatalf("PATCH /configs/x/x = %d %v %q, want 405 alone", other.Code, other.Header(), other.Body)
	}
}

// TestNonBuilderErrorsCarryNoCode pins that the code and the issues of an
// error body are the Builder's own: a route outside the Builder answers
// through the same error handler without either key.
func TestNonBuilderErrorsCarryNoCode(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)

	recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/configs/Topology/missing", user: builderTestOwner,
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
		method: http.MethodGet, path: "/builder/drafts/" + builderTestOwner + "/missing", user: builderTestOwner,
	})

	harness.decode(builder, &body)

	if body["code"] != string(bdoc.CodeRequestNotFound) {
		t.Fatalf("a Builder route answered %s, want the code %s", builder.Body, bdoc.CodeRequestNotFound)
	}
}

// TestBuilderPublishRefusalsCarryCodes asserts the refusals of a
// publication the Publish dialog tells apart carry the codes that name the
// config they are about and why.
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
