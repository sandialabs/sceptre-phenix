package web

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/store"
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

// documentsTestDigest is a digest of the shape a reference holds.
const documentsTestDigest = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// documentsTestTopology returns a stored topology config named name whose
// Builder annotation holds value, or no annotation when value is "".
func documentsTestTopology(t *testing.T, name, value string) store.Config {
	t.Helper()

	config := builderConfig(t, builderKindTopology, name)
	if value != "" {
		config.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: value}
	}

	return config
}

func TestBuilderDocumentReferences(t *testing.T) {
	configs := store.Configs{
		documentsTestTopology(t, "stored", encodeBuilderReference(t, bapi.DocumentReference{Digest: documentsTestDigest})),
		documentsTestTopology(t, "file", encodeBuilderReference(t, bapi.DocumentReference{Path: "/phenix/topologies/file.json"})),
		documentsTestTopology(t, "bad", `{"id":""}`),
		documentsTestTopology(t, "plain", ""),
	}

	var lists []string

	references := newBuilderDocumentReferences(func(kind string) (store.Configs, error) {
		lists = append(lists, kind)

		if kind != builderKindTopology {
			return store.Configs{}, nil
		}

		return configs, nil
	})

	stored := &bapi.PublishedDocument{
		ID: bapi.PublishedDocumentID("stored", documentsTestDigest), Kind: builderKindTopology,
		Target: "stored", Digest: documentsTestDigest,
	}
	superseded := &bapi.PublishedDocument{
		ID: bapi.PublishedDocumentID("stored", "sha256:"+strings.Repeat("f", 64)), Kind: builderKindTopology,
		Target: "stored", Digest: "sha256:" + strings.Repeat("f", 64),
	}

	for _, tt := range []struct {
		name     string
		document *bapi.PublishedDocument
		want     bool
	}{
		{name: "the referenced document", document: stored, want: true},
		{name: "a superseded document", document: superseded, want: false},
		{name: "a topology with an invalid reference", document: &bapi.PublishedDocument{ID: "x", Kind: builderKindTopology, Target: "bad"}},
		{name: "a topology without a reference", document: &bapi.PublishedDocument{ID: "x", Kind: builderKindTopology, Target: "plain"}},
		{name: "a missing topology", document: &bapi.PublishedDocument{ID: "x", Kind: builderKindTopology, Target: "gone"}},
		{name: "a missing scenario", document: &bapi.PublishedDocument{ID: "x", Kind: "Scenario", Target: "gone"}},
		{name: "an unknown kind", document: &bapi.PublishedDocument{ID: "x", Kind: "Unknown", Target: "stored"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := references.current(tt.document)
			if err != nil || got != tt.want {
				t.Errorf("current(%s/%s) = %t, %v, want %t, nil", tt.document.Kind, tt.document.Target, got, err, tt.want)
			}
		})
	}

	files, err := references.files()
	if err != nil {
		t.Fatalf("files() returned error: %v", err)
	}

	want := []builderFileReference{{topology: "file", path: "/phenix/topologies/file.json"}}
	if diff := cmp.Diff(want, files, cmp.AllowUnexported(builderFileReference{})); diff != "" {
		t.Errorf("files() mismatch (-want +got):\n%s", diff)
	}

	// Each kind is listed once, however many documents and files ask for it.
	if diff := cmp.Diff([]string{builderKindTopology, "Scenario"}, lists); diff != "" {
		t.Errorf("config listings mismatch (-want +got):\n%s", diff)
	}
}

func TestBuilderDocumentReferencesListingFails(t *testing.T) {
	listErr := errors.New("injected listing failure")
	references := newBuilderDocumentReferences(func(string) (store.Configs, error) { return nil, listErr })

	if _, err := references.reference("Topology/lab", "x"); !errors.Is(err, listErr) {
		t.Errorf("reference(Topology/lab) error = %v, want %v", err, listErr)
	}

	if _, err := references.current(&bapi.PublishedDocument{ID: "x", Kind: builderKindTopology, Target: "lab"}); !errors.Is(err, listErr) {
		t.Errorf("current(Topology/lab) error = %v, want %v", err, listErr)
	}

	if _, err := references.files(); !errors.Is(err, listErr) {
		t.Errorf("files() error = %v, want %v", err, listErr)
	}

	// A failed listing is not kept: the next call lists again.
	if _, listed := references.kinds[builderKindTopology]; listed {
		t.Error("kinds holds Topology after its listing failed, want it left out")
	}
}

func TestBuilderFileError(t *testing.T) {
	tests := []struct {
		reason bapi.DocumentFileReason
		status int
	}{
		{reason: bapi.DocumentFileMissing, status: http.StatusNotFound},
		{reason: bapi.DocumentFileTooLarge, status: http.StatusRequestEntityTooLarge},
		{reason: bapi.DocumentFileOutside, status: http.StatusUnprocessableEntity},
		{reason: bapi.DocumentFileUnreadable, status: http.StatusUnprocessableEntity},
		{reason: bapi.DocumentFileNotRegular, status: http.StatusUnprocessableEntity},
		{reason: bapi.DocumentFileInvalid, status: http.StatusUnprocessableEntity},
		{reason: bapi.DocumentFileDigest, status: http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			fileErr := &bapi.DocumentFileError{Reason: tt.reason, Path: "/phenix/lab.json", Root: "/phenix", Topology: "lab"}
			web := builderFileError(fileErr)

			if web.Status != tt.status {
				t.Errorf("builderFileError(%s) status = %d, want %d", tt.reason, web.Status, tt.status)
			}

			// The answer says only the fixed sentence, with no cause.
			if web.Message != fileErr.Error() || web.Cause != "" {
				t.Errorf("builderFileError(%s) = %q, cause %q, want %q and no cause", tt.reason, web.Message, web.Cause, fileErr.Error())
			}
		})
	}
}

func TestBuilderConfigName(t *testing.T) {
	tests := []struct {
		kind, target, want string
	}{
		{kind: builderKindTopology, target: "lab", want: "Topology/lab"},
		{kind: "topology", target: "lab", want: "Topology/lab"},
		{kind: "Experiment", target: "exp", want: "Experiment/exp"},
		{kind: "Unknown", target: "lab", want: ""},
		{kind: "", target: "lab", want: ""},
	}

	for _, tt := range tests {
		document := &bapi.PublishedDocument{Kind: tt.kind, Target: tt.target}
		if got := builderConfigName(document); got != tt.want {
			t.Errorf("builderConfigName(%q, %q) = %q, want %q", tt.kind, tt.target, got, tt.want)
		}
	}
}

func TestBuilderCurrentBuilderDocument(t *testing.T) {
	getErr := errors.New("injected get failure")
	document := &bapi.PublishedDocument{
		ID: bapi.PublishedDocumentID("lab", documentsTestDigest), Kind: builderKindTopology,
		Target: "lab", Digest: documentsTestDigest,
	}
	current := documentsTestTopology(t, "lab", encodeBuilderReference(t, bapi.DocumentReference{Digest: documentsTestDigest}))
	other := documentsTestTopology(t, "lab", encodeBuilderReference(t, bapi.DocumentReference{Digest: "sha256:" + strings.Repeat("f", 64)}))

	tests := []struct {
		name     string
		document *bapi.PublishedDocument
		config   *store.Config
		err      error
		want     bool
		wantErr  error
	}{
		{name: "current", document: document, config: &current, want: true},
		{name: "superseded", document: document, config: &other, want: false},
		{name: "missing", document: document, err: store.ErrNotExist, want: false},
		{name: "unreadable", document: document, err: getErr, wantErr: getErr},
		{name: "an unknown kind", document: &bapi.PublishedDocument{ID: document.ID, Kind: "Unknown", Target: "lab"}, err: getErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &builderAPI{getConfig: func(name string) (*store.Config, error) {
				if name != "Topology/lab" {
					t.Errorf("getConfig(%q), want Topology/lab", name)
				}

				return tt.config, tt.err
			}}

			config, got, err := api.currentBuilderDocument(tt.document)

			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("currentBuilderDocument() = %t, %v, want %t, %v", got, err, tt.want, tt.wantErr)
			}

			if got && config != tt.config {
				t.Errorf("currentBuilderDocument() config = %p, want the config read, %p", config, tt.config)
			}
		})
	}
}

func TestBuilderDocumentETag(t *testing.T) {
	document := &bapi.PublishedDocument{ID: "first", Kind: builderKindTopology, Target: "lab"}
	config := builderConfig(t, builderKindTopology, "lab")

	etag, err := builderDocumentETag(document, &config)
	if err != nil {
		t.Fatalf("builderDocumentETag returned error: %v", err)
	}

	if !regexp.MustCompile(`^"[0-9a-f]{32}"$`).MatchString(etag) || !builderStrongETag(etag) {
		t.Errorf("builderDocumentETag() = %q, want a strong tag of 32 hex digits", etag)
	}

	again, err := builderDocumentETag(document, &config)
	if err != nil || again != etag {
		t.Errorf("builderDocumentETag() again = %q, %v, want %q", again, err, etag)
	}

	changed := builderConfig(t, builderKindTopology, "lab")
	changed.Spec["nodes"] = []any{map[string]any{"general": map[string]any{"hostname": "aa"}}}

	for name, got := range map[string]func() (string, error){
		"another document": func() (string, error) {
			return builderDocumentETag(&bapi.PublishedDocument{ID: "second", Kind: builderKindTopology, Target: "lab"}, &config)
		},
		"a changed config": func() (string, error) { return builderDocumentETag(document, &changed) },
	} {
		if tag, err := got(); err != nil || tag == etag {
			t.Errorf("builderDocumentETag() of %s = %q, %v, want a tag other than %q", name, tag, err, etag)
		}
	}

	broken := builderConfig(t, builderKindTopology, "lab")
	broken.Spec["broken"] = make(chan int)

	_, err = builderDocumentETag(document, &broken)
	if status := builderWebStatus(err); status != http.StatusInternalServerError {
		t.Errorf("builderDocumentETag(a config JSON cannot encode) error status = %d, want %d (error %v)",
			status, http.StatusInternalServerError, err)
	}
}

func TestTopologyHoldsProjection(t *testing.T) {
	data := builderDocument(t, "lab")

	document, err := bapi.ParseDocument(data)
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}

	published, _, err := document.ToTopologyConfig("lab")
	if err != nil {
		t.Fatalf("ToTopologyConfig returned error: %v", err)
	}

	edited := builderConfig(t, builderKindTopology, "lab")
	edited.Spec["nodes"] = []any{map[string]any{"general": map[string]any{"hostname": "by-hand"}}}

	broken := builderConfig(t, builderKindTopology, "lab")
	broken.Spec["broken"] = make(chan int)

	tests := []struct {
		name     string
		data     []byte
		topology *store.Config
		want     bool
		status   int
	}{
		{name: "the projection", data: data, topology: published, want: true},
		{name: "a topology edited by hand", data: data, topology: &edited, want: false},
		{name: "a document that does not decode", data: []byte("{"), topology: published, want: false},
		{name: "a topology that cannot be digested", data: data, topology: &broken, status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := topologyHoldsProjection(tt.data, tt.topology)

			if got != tt.want {
				t.Errorf("topologyHoldsProjection() = %t, want %t", got, tt.want)
			}

			if status := builderWebStatus(err); status != tt.status {
				t.Errorf("topologyHoldsProjection() error status = %d, want %d (error %v)", status, tt.status, err)
			}
		})
	}
}

func TestBuilderDeleteTopologyConfig(t *testing.T) {
	deleteErr := errors.New("injected delete failure")

	for _, want := range []error{nil, deleteErr} {
		var deleted []string

		api := &builderAPI{publish: builderPublishOps{deleteConfig: func(name string) error {
			deleted = append(deleted, name)

			return want
		}}}

		if err := api.deleteTopologyConfig("Topology/lab", "lab"); !errors.Is(err, want) {
			t.Errorf("deleteTopologyConfig(Topology/lab) error = %v, want %v", err, want)
		}

		if diff := cmp.Diff([]string{"Topology/lab"}, deleted); diff != "" {
			t.Errorf("deleted configs mismatch (-want +got):\n%s", diff)
		}
	}
}

// TestBuilderDocumentRoutesConfigFailures answers 500 when the configs a
// document route reads fail, and 404 when the topology to delete is gone.
func TestBuilderDocumentRoutesConfigFailures(t *testing.T) {
	configErr := errors.New("injected config failure")

	failList := func(h *builderHarness) []builderOption {
		return []builderOption{withBuilderConfigs(func(string) (store.Configs, error) { return nil, configErr }, h.getConfig)}
	}
	failGet := func(h *builderHarness) []builderOption {
		return []builderOption{withBuilderConfigs(h.listConfigs, func(string) (*store.Config, error) { return nil, configErr })}
	}
	failDelete := func(err error) func(h *builderHarness) []builderOption {
		return func(h *builderHarness) []builderOption {
			ops := h.publishOps()
			ops.deleteConfig = func(string) error { return err }

			return []builderOption{withBuilderPublishOps(ops)}
		}
	}

	tests := []struct {
		name      string
		published bool
		fail      func(*builderHarness) []builderOption
		method    string
		path      string
		status    int
	}{
		{
			name:      "list a stored document",
			published: true,
			fail:      failList,
			method:    http.MethodGet,
			path:      "/builder/documents",
			status:    http.StatusInternalServerError,
		},
		{
			name:   "list the files",
			fail:   failList,
			method: http.MethodGet,
			path:   "/builder/documents",
			status: http.StatusInternalServerError,
		},
		{
			name:      "get a document",
			published: true,
			fail:      failGet,
			method:    http.MethodGet,
			path:      "/builder/documents/{id}",
			status:    http.StatusInternalServerError,
		},
		{
			name:      "get a topology document",
			published: true,
			fail:      failGet,
			method:    http.MethodGet,
			path:      "/builder/topologies/lab/document",
			status:    http.StatusInternalServerError,
		},
		{
			name:      "delete a document",
			published: true,
			fail:      failGet,
			method:    http.MethodDelete,
			path:      "/builder/documents/{id}",
			status:    http.StatusInternalServerError,
		},
		{
			name:      "delete a config that fails",
			published: true,
			fail:      failDelete(configErr),
			method:    http.MethodDelete,
			path:      "/builder/documents/{id}",
			status:    http.StatusInternalServerError,
		},
		{
			name:      "delete a config that is gone",
			published: true,
			fail:      failDelete(store.ErrNotExist),
			method:    http.MethodDelete,
			path:      "/builder/documents/{id}",
			status:    http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			request := builderRequest{method: tt.method, path: tt.path}

			if tt.published {
				document := builderPublish(t, harness, "lab")
				request.path = strings.ReplaceAll(tt.path, "{id}", document.ID)

				config, err := harness.getConfig("Topology/lab")
				if err != nil {
					t.Fatalf("getConfig(Topology/lab) returned error: %v", err)
				}

				if request.ifMatch, err = builderDocumentETag(document, config); err != nil {
					t.Fatalf("builderDocumentETag returned error: %v", err)
				}
			}

			harness.reroute(tt.fail(harness)...)

			recorder := harness.as(builderAlice, nil).do(request)
			if err := builderStatusError(recorder, tt.status); err != nil {
				t.Errorf("%s %s: %v", request.method, request.path, err)
			}

			if tt.method == http.MethodDelete && tt.status != http.StatusNoContent {
				if _, err := harness.getConfig("Topology/lab"); err != nil {
					t.Errorf("getConfig(Topology/lab) after the refused delete returned error: %v", err)
				}
			}
		})
	}
}

// TestBuilderTopologyDocumentOfADamagedRecord answers 500 for a topology
// whose reference names a stored record that does not decode.
func TestBuilderTopologyDocumentOfADamagedRecord(t *testing.T) {
	harness := newBuilderHarness(t)
	id := bapi.PublishedDocumentID("lab", documentsTestDigest)

	if _, err := harness.store.CreateRecord(bapi.NamespacePublished, id, []byte("not metadata")); err != nil {
		t.Fatalf("CreateRecord(%s) returned error: %v", id, err)
	}

	harness.configs = append(harness.configs,
		documentsTestTopology(t, "lab", encodeBuilderReference(t, bapi.DocumentReference{ID: id})))

	recorder := harness.as(builderAlice, nil).do(builderRequest{method: http.MethodGet, path: "/builder/topologies/lab/document"})
	if err := builderStatusError(recorder, http.StatusInternalServerError); err != nil {
		t.Fatalf("GET /builder/topologies/lab/document: %v", err)
	}

	if got := builderRefusal(t, recorder).Code; got != string(bdoc.CodeServerError) {
		t.Errorf("GET /builder/topologies/lab/document code = %q, want %q", got, bdoc.CodeServerError)
	}
}
