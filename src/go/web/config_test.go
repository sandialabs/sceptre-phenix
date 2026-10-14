package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// revisionTestTopology is a valid topology, as the UI config editor sends it.
const revisionTestTopology = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"demo"},` +
	`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"host-00"},` +
	`"hardware":{"os_type":"linux","drives":[{"image":"ubuntu.qc2"}]}}]}}`

// useRevisionTestStore stores the demo topology in a BoltDB store of the
// test's own, which enforces revisions, and returns its revision.
func useRevisionTestStore(t *testing.T) uint64 {
	t.Helper()

	storetest.Use(t)

	c, err := store.NewConfigFromJSON([]byte(revisionTestTopology))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Create(c); err != nil {
		t.Fatal(err)
	}

	return c.Metadata.Revision
}

// configRequest is a request for the demo topology with the given headers.
func configRequest(t *testing.T, method string, headers map[string]string) *http.Request {
	t.Helper()

	req := validationTestRequest(t, method, "/api/v1/configs/topology/demo", mimeJSON,
		[]byte(revisionTestTopology), map[string]string{"kind": "topology", "name": "demo"})

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req
}

// webErrorStatus returns the status of a handler's error, or 0 for none.
func webErrorStatus(t *testing.T, err error) (int, *weberror.WebError) {
	t.Helper()

	if err == nil {
		return 0, nil
	}

	var werr *weberror.WebError
	if !errors.As(err, &werr) {
		t.Fatalf("expected a *weberror.WebError, got %v", err)
	}

	return werr.Status, werr
}

func TestGetConfigRevision(t *testing.T) {
	rev := useRevisionTestStore(t)
	etag := `"r` + strconv.FormatUint(rev, 10) + `"`

	w := httptest.NewRecorder()
	if err := GetConfig(w, configRequest(t, http.MethodGet, nil)); err != nil {
		t.Fatal(err)
	}

	if got := w.Header().Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}

	if got := w.Header().Get("X-Phenix-Revision"); got != strconv.FormatUint(rev, 10) {
		t.Errorf("X-Phenix-Revision = %q, want %d", got, rev)
	}

	var body store.Config
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Metadata.Revision != rev {
		t.Errorf("body metadata = %+v, %v; want revision %d", body.Metadata, err, rev)
	}

	w = httptest.NewRecorder()
	if err := GetConfig(w, configRequest(t, http.MethodGet, map[string]string{"If-None-Match": etag})); err != nil {
		t.Fatal(err)
	}

	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("If-None-Match of the current ETag: %d with %d bytes, want 304 and no body", w.Code, w.Body.Len())
	}
}

func TestUpdateConfigIfMatch(t *testing.T) {
	rev := useRevisionTestStore(t)
	etag := `"r` + strconv.FormatUint(rev, 10) + `"`
	stale := `"r` + strconv.FormatUint(rev-1, 10) + `"`

	status, werr := webErrorStatus(t, UpdateConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodPut, map[string]string{"If-Match": stale})))
	if status != http.StatusPreconditionFailed || werr.UserMetadata["revision"] != strconv.FormatUint(rev, 10) {
		t.Fatalf("PUT with a stale If-Match: %d %+v, want 412 with metadata.revision %d", status, werr, rev)
	}

	if status, _ := webErrorStatus(t, UpdateConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodPut, map[string]string{"If-Match": "r1"}))); status != http.StatusBadRequest {
		t.Fatalf("PUT with a malformed If-Match: %d, want 400", status)
	}

	w := httptest.NewRecorder()
	if err := UpdateConfig(w, configRequest(t, http.MethodPut, map[string]string{"If-Match": etag})); err != nil {
		t.Fatalf("PUT with the current If-Match: %v", err)
	}

	newETag := w.Header().Get("ETag")
	if w.Code != http.StatusNoContent || newETag == "" || newETag == etag {
		t.Fatalf("PUT with the current If-Match: %d with ETag %q, want 204 and a new ETag", w.Code, newETag)
	}

	got, _ := store.NewConfig("topology/demo")
	if err := store.Get(got); err != nil || `"r`+strconv.FormatUint(got.Metadata.Revision, 10)+`"` != newETag {
		t.Fatalf("stored revision %d, %v; the PUT answered %s", got.Metadata.Revision, err, newETag)
	}

	// without If-Match, a PUT writes whatever the revision
	if err := UpdateConfig(httptest.NewRecorder(), configRequest(t, http.MethodPut, nil)); err != nil {
		t.Fatalf("PUT without If-Match: %v", err)
	}

	status, _ = webErrorStatus(t, DeleteConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodDelete, map[string]string{"If-Match": newETag})))
	if status != http.StatusPreconditionFailed {
		t.Fatalf("DELETE with a stale If-Match: %d, want 412", status)
	}

	if err := store.Get(got); err != nil {
		t.Fatalf("a refused DELETE deleted the config: %v", err)
	}

	if err := DeleteConfig(httptest.NewRecorder(),
		configRequest(t, http.MethodDelete, map[string]string{"If-Match": "*"})); err != nil {
		t.Fatalf("DELETE with If-Match *: %v", err)
	}
}

func TestGetConfigsEmptyList(t *testing.T) {
	useRevisionTestStore(t)

	req := validationTestRequest(t, http.MethodGet, "/api/v1/configs", "", nil, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole,
		testRole("configs", "list", "Scenario/*")))

	w := httptest.NewRecorder()
	if err := GetConfigs(w, req); err != nil {
		t.Fatal(err)
	}

	if got := w.Body.String(); got != `{"configs":[]}` {
		t.Errorf("GET /configs that the role sees none of = %s, want {\"configs\":[]}", got)
	}
}
