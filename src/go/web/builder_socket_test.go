package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bapi "phenix/api/builder"
)

// TestBuilderRoutesAnswerOnUnixSocket checks that the unix socket's router
// serves the Builder routes of the API router, with the same handlers, as
// global-admin and with the Builder's response headers, and no other route
// of the API router.
func TestBuilderRoutesAnswerOnUnixSocket(t *testing.T) {
	// The socket's NoAuth middleware reads the global-admin role from the
	// config store.
	useUsersTestStore(t)

	harness := newBuilderHarness(t)
	own := harness.createDraft("global-admin", "socket-draft")
	harness.createDraft(builderTestOwner, "other-draft")

	harness.api.Handle("/configs", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods(http.MethodGet)

	socket := newSocketRouter(harness.api)

	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()

		recorder := httptest.NewRecorder()
		socket.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil))

		return recorder
	}

	recorder := get("/builder/drafts")

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /builder/drafts on the socket: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	var listing struct {
		Drafts []builderDraftResponse `json:"drafts"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decoding the listing: %v", err)
	}

	if len(listing.Drafts) != 1 || listing.Drafts[0].ID != own.ID || listing.Drafts[0].Owner != "global-admin" {
		t.Errorf("the socket listed %+v, want only the draft %s of global-admin", listing.Drafts, own.ID)
	}

	if recorder := get("/builder/drafts/global-admin/" + own.ID); recorder.Code != http.StatusOK {
		t.Errorf("GET of the draft on the socket: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if recorder := get("/configs"); recorder.Code != http.StatusNotFound {
		t.Errorf(
			"GET /configs on the socket: status = %d, want %d: the socket serves only its own routes",
			recorder.Code, http.StatusNotFound,
		)
	}
}

// TestBuilderSocketPostKeepsHandlersAndLimits checks that a POST route the
// unix socket's router mirrors answers as on the API router: a request it
// accepts, a body past the Builder's limit, and a method the route lacks.
func TestBuilderSocketPostKeepsHandlersAndLimits(t *testing.T) {
	useUsersTestStore(t)

	harness := newBuilderHarness(t)
	socket := newSocketRouter(harness.api)

	serve := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()

		recorder := httptest.NewRecorder()
		socket.ServeHTTP(recorder, httptest.NewRequest(method, "/api/v1/builder/export/topology", strings.NewReader(body)))

		return recorder
	}

	data, err := bapi.EncodeDocument(exportVLANDocument(map[string]any{"name": "eth0", "vlan": "EXP"}))
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	request, err := json.Marshal(builderTopologyExportRequest{Document: data, Name: ""})
	if err != nil {
		t.Fatalf("encoding the export request: %v", err)
	}

	recorder := serve(http.MethodPost, string(request))

	if recorder.Code != http.StatusOK {
		t.Fatalf("POST /builder/export/topology on the socket: status = %d, want %d: %s",
			recorder.Code, http.StatusOK, recorder.Body)
	}

	var exported builderTopologyExportResponse

	if err := json.Unmarshal(recorder.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decoding the export: %v", err)
	}

	if !strings.Contains(exported.YAML, "hostname: host") {
		t.Errorf("the socket exported %+v, want the topology of the document", exported)
	}

	if recorder := serve(http.MethodPost, strings.Repeat(" ", builderMaxRequestBytes+1)); recorder.Code !=
		http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized POST on the socket: status = %d, want %d: %s",
			recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body)
	}

	if recorder := serve(http.MethodGet, ""); recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /builder/export/topology on the socket: status = %d, want %d",
			recorder.Code, http.StatusMethodNotAllowed)
	}
}
