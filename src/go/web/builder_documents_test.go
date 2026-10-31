package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
)

// TestBuilderDamagedDocument stores published records whose metadata does
// not validate. GET /builder/documents lists the readable document, and
// DELETE /builder/documents/{id} removes each damaged record and its content,
// with the permissions of the config the record still names, or of every
// config.
func TestBuilderDamagedDocument(t *testing.T) {
	harness := newBuilderHarness(t)
	readable := builderPublish(t, harness, "lab")

	const (
		named   = "damaged-named"
		unnamed = "damaged-unnamed"
	)

	for id, value := range map[string]string{
		named:   `{"id":"damaged-named","kind":"Topology","target":"secret"}`,
		unnamed: `not metadata`,
	} {
		if _, err := harness.store.CreateRecord(bapi.NamespacePublished, id, []byte(value)); err != nil {
			t.Fatalf("CreateRecord(%s) returned error: %v", id, err)
		}

		if _, err := harness.store.CreateRecord(bapi.NamespaceContent, "published/"+id, []byte("content")); err != nil {
			t.Fatalf("CreateRecord(content of %s) returned error: %v", id, err)
		}
	}

	var listed struct {
		Documents []struct {
			ID string `json:"id"`
		} `json:"documents"`
	}

	harness.as(builderAlice, nil).doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents"},
		http.StatusOK, &listed)

	if len(listed.Documents) != 1 || listed.Documents[0].ID != readable.ID {
		t.Errorf("GET /builder/documents = %+v, want only %s", listed.Documents, readable.ID)
	}

	record, err := harness.store.GetRecord(bapi.NamespacePublished, named)
	if err != nil {
		t.Fatalf("GetRecord(%s) returned error: %v", named, err)
	}

	etag := bapi.RevisionETag(record.Revision)

	unnamedRecord, err := harness.store.GetRecord(bapi.NamespacePublished, unnamed)
	if err != nil {
		t.Fatalf("GetRecord(%s) returned error: %v", unnamed, err)
	}

	unnamedETag := bapi.RevisionETag(unnamedRecord.Revision)

	remove := func(id, ifMatch string) builderRequest {
		return builderRequest{method: http.MethodDelete, path: "/builder/documents/" + id, ifMatch: ifMatch}
	}

	var (
		public   = builderRole(builderPolicy([]string{"configs"}, []string{"Topology/public"}, []string{"get", "delete"}))
		readOnly = builderRole(
			builderPolicy([]string{"configs"}, []string{"Topology/secret"}, []string{"get"}),
			builderPolicy([]string{"configs"}, []string{"Topology/public"}, []string{"delete"}),
		)
		everyKind = builderRole(builderPolicy([]string{"configs"},
			[]string{"Topology/*", "Scenario/*", "Experiment/*"}, []string{"get", "delete"}))
		getEvery = builderRole(builderPolicy([]string{"configs"}, []string{"*/*"}, []string{"get"}))
	)

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "a damaged document of a config the role may not read", user: builderAlice, role: public,
			request: remove(named, etag), status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a damaged document of a config the role may not delete", user: builderAlice, role: readOnly,
			request: remove(named, etag), status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "a damaged document that names no config, by a role limited to one config", user: builderAlice,
			role: public, request: remove(unnamed, unnamedETag), status: http.StatusNotFound,
			code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a damaged document that names no config, by a role limited to config kinds", user: builderAlice,
			role: everyKind, request: remove(unnamed, unnamedETag), status: http.StatusNotFound,
			code: bdoc.CodeRequestNotFound,
		},
		{
			name: "a damaged document that names no config, by a role that may not delete every config",
			user: builderAlice, role: getEvery, request: remove(unnamed, unnamedETag),
			status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
	})

	stale := harness.as(builderAlice, nil).do(remove(named, `"999"`))
	if stale.Code != http.StatusPreconditionFailed {
		t.Errorf("DELETE %s with a stale tag: status = %d, want %d: %s",
			named, stale.Code, http.StatusPreconditionFailed, stale.Body)
	}

	if got := stale.Header().Get("ETag"); got != etag {
		t.Errorf("DELETE %s with a stale tag: ETag = %q, want %q", named, got, etag)
	}

	for id, ifMatch := range map[string]string{named: etag, unnamed: unnamedETag} {
		if recorder := harness.as(builderAlice, nil).do(remove(id, ifMatch)); recorder.Code != http.StatusNoContent {
			t.Errorf("DELETE /builder/documents/%s: status = %d, want %d: %s",
				id, recorder.Code, http.StatusNoContent, recorder.Body)
		}
	}

	if diff := cmp.Diff([]string{readable.ID}, harness.store.Keys(bapi.NamespacePublished)); diff != "" {
		t.Errorf("published records after the deletes mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"published/" + readable.ID}, harness.store.Keys(bapi.NamespaceContent)); diff != "" {
		t.Errorf("content after the deletes mismatch (-want +got):\n%s", diff)
	}

	if _, err := harness.getConfig("Topology/lab"); err != nil {
		t.Errorf("getConfig(Topology/lab) after the deletes returned error: %v", err)
	}
}
