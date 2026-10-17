package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog"
	"phenix/util/plog/plogtest"
)

// builderFileMarker is text in the Builder files of these tests that no refusal
// and no log line may repeat.
const builderFileMarker = "MARKER-FROM-THE-FILE"

// builderFileUnusable is what the server logs for a Builder file it cannot use.
const builderFileUnusable = "builder document file not usable"

// writeBuilderFile writes content to the file named by the path elements
// below the directory Builder files are read from, and returns its path.
func (h *builderHarness) writeBuilderFile(content []byte, elements ...string) string {
	h.t.Helper()

	path := filepath.Join(append([]string{h.files}, elements...)...)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		h.t.Fatalf("creating the directory of %s: %v", path, err)
	}

	if err := os.WriteFile(path, content, 0o600); err != nil {
		h.t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// addFileTopology stores a topology that holds the document reference.
func (h *builderHarness) addFileTopology(name string, reference bapi.DocumentReference) {
	h.t.Helper()

	topology := builderConfig(h.t, builderKindTopology, name)
	topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: encodeBuilderReference(h.t, reference)}
	h.configs = append(h.configs, topology)
}

// getTopologyDocument asks for the Builder document the topology references.
func (h *builderHarness) getTopologyDocument(name string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.do(builderRequest{
		method: http.MethodGet, path: "/builder/topologies/" + name + "/document", user: builderAlice,
	})
}

// builderDocumentDigest returns the digest of a document's canonical JSON.
func builderDocumentDigest(t *testing.T, data []byte) string {
	t.Helper()

	file, err := bapi.ParseDocumentText(data)
	if err != nil {
		t.Fatalf("ParseDocumentText returned error: %v", err)
	}

	return file.Digest
}

// builderCompactJSON returns data without its white space, as a response
// holds a document.
func builderCompactJSON(t *testing.T, data []byte) []byte {
	t.Helper()

	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatalf("compacting the document: %v", err)
	}

	return compact.Bytes()
}

// builderYAML returns the JSON document data written as YAML.
func builderYAML(t *testing.T, data []byte) []byte {
	t.Helper()

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("decoding the document: %v", err)
	}

	text, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the document as YAML: %v", err)
	}

	return text
}

// TestBuilderTopologyDocument reads a topology's document: the stored one when
// the reference names one, and otherwise the Builder file at its path.
func TestBuilderTopologyDocument(t *testing.T) {
	harness := newBuilderHarness(t)

	stored := builderPublish(t, harness, "stored")
	content := builderDocument(t, "file")
	digest := builderDocumentDigest(t, content)
	other := builderDocument(t, "other")

	asJSON := harness.writeBuilderFile(builderCompactJSON(t, content), "topologies", "site", "site.builder.json")
	asYAML := harness.writeBuilderFile(builderYAML(t, content), "topologies", "site", "site.builder.yaml")

	// The stored document wins over the file the reference names too.
	both := builderPublish(t, harness, "both")
	setBuilderTopologyReference(t, harness, "both", bapi.DocumentReference{Digest: both.Digest, ID: both.ID, Path: asJSON})

	harness.addFileTopology("json", bapi.DocumentReference{Path: asJSON})
	harness.addFileTopology("yaml", bapi.DocumentReference{Path: asYAML})
	harness.addFileTopology("pinned", bapi.DocumentReference{Digest: digest, Path: asYAML})
	// As on a server the topology was copied to, with its file but not its stored document.
	harness.addFileTopology("copied", bapi.DocumentReference{
		Digest: digest, ID: bapi.PublishedDocumentID("copied", digest), Path: asJSON,
	})
	// An ID says nothing of the file: this one names no stored document.
	harness.addFileTopology("renamed", bapi.DocumentReference{ID: stored.ID, Path: asJSON})

	for name, test := range map[string]struct {
		source, path, digest, id string
		document                 []byte
	}{
		"stored":  {source: "store", digest: stored.Digest, id: stored.ID, document: builderDocument(t, "stored")},
		"both":    {source: "store", digest: both.Digest, id: both.ID, document: builderDocument(t, "both")},
		"json":    {source: "file", path: asJSON, digest: digest, document: content},
		"yaml":    {source: "file", path: asYAML, digest: digest, document: content},
		"pinned":  {source: "file", path: asYAML, digest: digest, document: content},
		"copied":  {source: "file", path: asJSON, digest: digest, document: content},
		"renamed": {source: "file", path: asJSON, digest: digest, document: content},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := harness.getTopologyDocument(name)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
			}

			var response builderDocumentResponse

			harness.decode(recorder, &response)

			want := builderDocumentResponse{
				Source: test.source, ID: test.id, Digest: test.digest, Size: int64(len(test.document)),
				Target: name, Kind: builderKindTopology, Config: "Topology/" + name, Path: test.path,
				// The topologies of this test are not what their documents publish.
				TopologyDiffers: test.source == "file",
				Document:        builderCompactJSON(t, test.document),
			}

			if test.source == "store" {
				want.CreatedAt, want.CreatedBy = response.CreatedAt, builderAlice

				if response.CreatedAt.IsZero() {
					t.Error("a stored document has no time")
				}
			}

			if got, _ := json.Marshal(response); string(got) != builderJSON(t, want) {
				t.Fatalf("response = %s, want %s", got, builderJSON(t, want))
			}

			// A file has no ID, author or time, and says so by leaving them out.
			var fields map[string]json.RawMessage

			harness.decode(recorder, &fields)

			for _, field := range []string{"id", "createdAt", "createdBy", "draftId", "snapshotId"} {
				if _, present := fields[field]; present && test.source == "file" {
					t.Errorf("the response of a file holds %s", field)
				}
			}
		})
	}

	// A file is read on every request, and never for a stored document.
	harness.fileReads = 0

	harness.getTopologyDocument("stored")
	harness.getTopologyDocument("both")

	if harness.fileReads != 0 {
		t.Fatalf("reading stored documents read %d files", harness.fileReads)
	}

	harness.writeBuilderFile(builderCompactJSON(t, other), "topologies", "site", "site.builder.json")

	var changed builderDocumentResponse

	harness.decode(harness.getTopologyDocument("json"), &changed)

	if changed.Digest != builderDocumentDigest(t, other) || harness.fileReads != 1 {
		t.Fatalf("after the file changed: digest = %s after %d reads, want the new content read once", changed.Digest, harness.fileReads)
	}

	// The pinned file no longer has the digest its topology records.
	recorder := harness.getTopologyDocument("copied")
	if said := builderRefusal(t, recorder); recorder.Code != http.StatusUnprocessableEntity ||
		said.Message != "Builder file "+asJSON+" does not match the digest topology copied records for it." || said.Cause != "" {
		t.Fatalf("a pinned file that changed: status = %d, said %+v", recorder.Code, said)
	}

	// A damaged stored document is reported, and its file is not read in its place.
	for _, key := range harness.store.Keys(bapi.NamespaceChunks) {
		harness.store.Drop(bapi.NamespaceChunks, key)
	}

	harness.fileReads = 0

	if recorder := harness.getTopologyDocument("both"); recorder.Code != http.StatusInternalServerError || harness.fileReads != 0 {
		t.Fatalf("a damaged document: status = %d after %d file reads: %s", recorder.Code, harness.fileReads, recorder.Body)
	}
}

// TestBuilderTopologyDocumentRefusals asserts an unusable Builder file is
// answered and logged with its reason, repeating nothing the file holds.
func TestBuilderTopologyDocumentRefusals(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)

	valid := builderDocument(t, builderFileMarker)
	outside := filepath.Join(t.TempDir(), "secret.json")

	if err := os.WriteFile(outside, valid, 0o600); err != nil {
		t.Fatalf("writing %s: %v", outside, err)
	}

	harness.writeBuilderFile(valid, "topologies", "valid.json")

	tests := map[string]struct {
		path   string
		digest string
		status int
		reason bapi.DocumentFileReason
		// said is the answer's message, with %s for the path.
		said string
	}{
		"outside": {
			path: outside, status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileOutside,
			said: "Builder file %s is outside " + harness.files + ", the directory phenix reads Builder files from.",
		},
		"missing": {
			path: filepath.Join(harness.files, "topologies", "gone.yaml"), status: http.StatusNotFound,
			reason: bapi.DocumentFileMissing, said: "Builder file %s does not exist on this phenix server.",
		},
		"large": {
			path:   harness.writeBuilderFile(append(bytes.Clone(valid), bytes.Repeat([]byte(" "), bapi.MaxDocumentBytes)...), "large.json"),
			status: http.StatusRequestEntityTooLarge, reason: bapi.DocumentFileTooLarge, said: "Builder file %s is larger than 5 MiB.",
		},
		"invalid": {
			path: harness.writeBuilderFile(
				bytes.ReplaceAll(builderDocument(t, "site"), []byte(bdoc.SchemaURI), []byte(builderFileMarker)), "invalid.json",
			),
			status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileInvalid,
			said: "Builder file %s is not a valid Builder document. Upload it in the Builder to see why.",
		},
		"repinned": {
			path:   filepath.Join(harness.files, "topologies", "valid.json"),
			digest: builderDocumentDigest(t, builderDocument(t, "another")),
			status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileDigest,
			said: "Builder file %s does not match the digest topology repinned records for it.",
		},
	}

	for name, test := range tests {
		harness.addFileTopology(name, bapi.DocumentReference{Digest: test.digest, Path: test.path})
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			logs.Take(t, nil)

			recorder := harness.getTopologyDocument(name)
			said := builderRefusal(t, recorder)

			if recorder.Code != test.status || said.Message != strings.ReplaceAll(test.said, "%s", test.path) || said.Cause != "" {
				t.Fatalf("status = %d, said %+v; want %d and %q", recorder.Code, said, test.status, test.said)
			}

			if strings.Contains(recorder.Body.String(), builderFileMarker) {
				t.Fatalf("the response repeats what the file holds: %s", recorder.Body)
			}

			logged := logs.String()
			if strings.Contains(logged, builderFileMarker) {
				t.Fatalf("the log repeats what the file holds: %s", logged)
			}

			records := logs.Take(t, plogtest.Message(builderFileUnusable))
			if len(records) != 1 || records[0]["level"] != slog.LevelWarn.String() || records[0]["type"] != string(plog.TypeSystem) ||
				records[0]["topology"] != name || records[0]["path"] != test.path || records[0]["reason"] != string(test.reason) {
				t.Fatalf("logged %v, want one warning with the reason %q", records, test.reason)
			}
		})
	}
}

// TestBuilderTopologyDocumentNoLeak asserts a topology the caller may not get
// is answered as a missing one, and no file is read for it.
func TestBuilderTopologyDocumentNoLeak(t *testing.T) {
	harness := newBuilderHarness(t)
	stored := builderPublish(t, harness, "stored")

	path := harness.writeBuilderFile(builderDocument(t, "site"), "site.json")
	harness.addFileTopology("site", bapi.DocumentReference{Path: path})
	harness.addFileTopology("elsewhere", bapi.DocumentReference{ID: stored.ID})
	harness.addFileTopology("dangling", bapi.DocumentReference{Digest: stored.Digest})

	harness.configs = append(harness.configs, builderConfig(t, builderKindTopology, "plain"))

	// References past the Topology config hook, each naming a file it may not: none is read.
	secret := filepath.Join(t.TempDir(), "store.bdb")
	if err := os.WriteFile(secret, builderDocument(t, "site"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", secret, err)
	}

	unreadable := map[string]string{
		"relative":   "site.json",
		"climbing":   harness.files + "/../" + filepath.Base(filepath.Dir(secret)) + "/site.json",
		"dotted":     harness.files + "/./site.json",
		"store-file": secret,
		"no-suffix":  filepath.Join(harness.files, "site"),
		"upper-case": filepath.Join(harness.files, "site.JSON"),
		"control":    harness.files + "/site\u0000.json",
		"too-long":   harness.files + "/" + strings.Repeat("d", bapi.MaxDocumentPathLength) + ".json",
	}

	for name, path := range unreadable {
		topology := builderConfig(t, builderKindTopology, name)
		topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: `{"path":"` + path + `"}`}
		harness.configs = append(harness.configs, topology)
	}

	missing := func(name string, recorder *httptest.ResponseRecorder) {
		t.Helper()

		said := builderRefusal(t, recorder)

		if recorder.Code != http.StatusNotFound || said.Message != "builder document of topology "+name+" not found" || said.Cause != "" {
			t.Fatalf("topology %s: status = %d, said %+v; want it not found", name, recorder.Code, said)
		}
	}

	names := append(slices.Collect(maps.Keys(unreadable)),
		"plain", "elsewhere", "dangling", "absent", strings.Repeat("n", bapi.MaxTargetLength+1))

	for _, name := range names {
		missing(name, harness.getTopologyDocument(name))
	}

	if harness.fileReads != 0 {
		t.Fatalf("topologies that name no file read %d files", harness.fileReads)
	}

	// A caller who may get other topologies, but not these two.
	role := builderRole(builderPolicy([]string{"configs"}, []string{"Topology/other"}, []string{"list", "get", "create", "update"}))

	for _, name := range []string{"site", "stored"} {
		request := builderRequest{
			method: http.MethodGet, path: "/builder/topologies/" + name + "/document", user: builderBob, role: role,
		}

		forbidden := harness.do(request)
		missing(name, forbidden)

		if allowed := harness.getTopologyDocument(name); allowed.Code != http.StatusOK {
			t.Fatalf("topology %s: status = %d for a caller who may get it: %s", name, allowed.Code, allowed.Body)
		}

		harness.fileReads = 0

		// The same answer once the topology is gone.
		harness.configs = slices.DeleteFunc(harness.configs, func(config store.Config) bool {
			return config.FullName() == "Topology/"+name
		})

		gone := harness.do(request)

		if gone.Code != forbidden.Code || gone.Body.String() != forbidden.Body.String() ||
			!maps.EqualFunc(gone.Header(), forbidden.Header(), slices.Equal) {
			t.Fatalf("topology %s: a forbidden topology is answered %d %s, a missing one %d %s",
				name, forbidden.Code, forbidden.Body, gone.Code, gone.Body)
		}
	}

	if harness.fileReads != 0 {
		t.Fatalf("a caller who may not get the topology made the server read %d files", harness.fileReads)
	}

	// Without the base permission the request is refused before any lookup.
	none := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"list"}))

	if recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/topologies/site/document", user: builderBob, role: none,
	}); recorder.Code != http.StatusForbidden {
		t.Fatalf("without configs get: status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuilderListDocumentsListsFiles asserts a topology that names only a
// Builder file is listed with its path, and the listing reads no file.
func TestBuilderListDocumentsListsFiles(t *testing.T) {
	harness := newBuilderHarness(t)

	stored := builderPublish(t, harness, "stored")
	both := builderPublish(t, harness, "both")
	present := harness.writeBuilderFile(builderDocument(t, "file"), "topologies", "present.json")
	absent := filepath.Join(harness.files, "topologies", "absent.yaml")

	setBuilderTopologyReference(t, harness, "both", bapi.DocumentReference{Digest: both.Digest, ID: both.ID, Path: present})

	harness.addFileTopology("file", bapi.DocumentReference{Path: present})
	harness.addFileTopology("gone", bapi.DocumentReference{Path: absent})
	harness.addFileTopology("pinned", bapi.DocumentReference{Digest: stored.Digest, ID: "another-document", Path: absent})
	harness.addFileTopology("hidden", bapi.DocumentReference{Path: present})
	harness.addFileTopology("digest-only", bapi.DocumentReference{Digest: stored.Digest})
	harness.configs = append(harness.configs, builderConfig(t, builderKindTopology, "plain"))

	role := builderRole(builderPolicy(
		[]string{"configs"},
		[]string{
			"Topology/stored", "Topology/both", "Topology/file", "Topology/gone", "Topology/pinned", "Topology/plain",
			"Topology/digest-only",
		},
		[]string{"list"},
	))

	harness.configLists, harness.configGets, harness.fileReads = nil, 0, 0

	recorder := harness.do(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderAlice, role: role})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}

	// The experiments are listed for the stored rows, which may name one.
	if harness.fileReads != 0 || harness.configGets != 0 ||
		!slices.Equal(harness.configLists, []string{builderKindTopology, kindExperiment}) {
		t.Fatalf("the listing read %d files, got %d configs and listed %q; "+
			"want no file read, and the topologies and the experiments listed once each",
			harness.fileReads, harness.configGets, harness.configLists)
	}

	var response struct {
		Documents []map[string]any `json:"documents"`
	}

	harness.decode(recorder, &response)

	rows := map[string]map[string]any{}
	for _, row := range response.Documents {
		target, _ := row["target"].(string)
		rows[target] = row
	}

	if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, []string{"both", "file", "gone", "pinned", "stored"}) ||
		len(response.Documents) != len(rows) {
		t.Fatalf("listed %v in %d rows, want one row each for both, file, gone, pinned and stored", got, len(response.Documents))
	}

	for name, path := range map[string]string{"file": present, "gone": absent, "pinned": absent} {
		want := map[string]any{
			"source": "file", "target": name, "kind": builderKindTopology, "config": "Topology/" + name, "path": path,
		}

		if !maps.Equal(rows[name], want) {
			t.Errorf("row of %s = %v, want %v", name, rows[name], want)
		}
	}

	for name, document := range map[string]*bapi.PublishedDocument{"stored": stored, "both": both} {
		row := rows[name]

		if row["source"] != "store" || row["id"] != document.ID || row["digest"] != document.Digest ||
			row["createdBy"] != builderAlice {
			t.Errorf("row of %s = %v, want the stored document %s", name, row, document.ID)
		}

		if _, present := row["path"]; present {
			t.Errorf("row of %s = %v, want no path for a stored document", name, row)
		}
	}
}

// openedBuilderFileDocument is the Builder file of a topology as the editor opens it.
type openedBuilderFileDocument struct {
	token    string
	digest   string
	document *bdoc.Document
	differs  bool
}

// openBuilderFile reads the document of a topology that names a Builder
// file through GET /builder/topologies/{topology}/document.
func openBuilderFile(t *testing.T, harness *builderHarness, topology string) openedBuilderFileDocument {
	t.Helper()

	recorder := harness.getTopologyDocument(topology)
	if recorder.Code != http.StatusOK {
		t.Fatalf("opening the document of topology %s: status = %d: %s", topology, recorder.Code, recorder.Body)
	}

	var response builderDocumentResponse

	harness.decode(recorder, &response)

	if response.Source != builderDocumentSourceFile {
		t.Fatalf("the document of topology %s is read from the %s, want its file", topology, response.Source)
	}

	document, err := bdoc.Decode(response.Document)
	if err != nil {
		t.Fatalf("decoding the document of topology %s: %v", topology, err)
	}

	return openedBuilderFileDocument{
		token:  builderFileTokenPrefix + topology + "/" + response.Digest,
		digest: response.Digest, document: document, differs: response.TopologyDiffers,
	}
}

// newFileBackedTopology publishes topology name with device aa, then keeps its
// document only as the Builder file the topology names, and returns the path.
func newFileBackedTopology(t *testing.T, harness *builderHarness, name string, pinned bool) string {
	t.Helper()

	document := bdoc.NewDocument(name)
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	publishBuilderDraft(t, harness, draft, `{"mode":"topology","topology":{"name":"`+name+`","action":"create"}}`, http.StatusOK)

	reference := builderTopologyReference(t, harness, name)

	_, data, err := harness.service.GetPublishedDocumentData(t.Context(), reference.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	if _, err := harness.service.DeleteTargetDocuments(t.Context(), name); err != nil {
		t.Fatalf("DeleteTargetDocuments returned error: %v", err)
	}

	path := harness.writeBuilderFile(builderYAML(t, data), "topologies", name, name+".builder.yaml")
	file := bapi.DocumentReference{Path: path}

	if pinned {
		file.Digest = reference.Digest
	}

	setBuilderTopologyReference(t, harness, name, file)

	return path
}

// createBuilderFileDraft creates a draft with a source token, answering
// with the status and what the server said.
func createBuilderFileDraft(
	t *testing.T,
	harness *builderHarness,
	document *bdoc.Document,
	token string,
) (*httptest.ResponseRecorder, builderDraftResponse) {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: builderJSON(t, map[string]any{"document": json.RawMessage(data), "sourceToken": token}),
	})

	var draft builderDraftResponse

	if recorder.Code == http.StatusCreated {
		harness.decode(recorder, &draft)
	}

	return recorder, draft
}

// TestBuilderCreateDraftFromBuilderFile asserts a draft whose source token
// names a Builder file is made only while the caller may read it and it is unchanged.
func TestBuilderCreateDraftFromBuilderFile(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)

	path := newFileBackedTopology(t, harness, "site", false)
	opened := openBuilderFile(t, harness, "site")

	if opened.differs {
		t.Fatal("the topology differs from the file it was written from")
	}

	recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
	if recorder.Code != http.StatusCreated || draft.SourceToken != opened.token {
		t.Fatalf("creating the draft: status = %d, token %q: %s", recorder.Code, draft.SourceToken, recorder.Body)
	}

	stored := builderPublish(t, harness, "stored")
	setBuilderTopologyReference(t, harness, "stored", bapi.DocumentReference{Digest: stored.Digest, ID: stored.ID, Path: path})

	harness.addFileTopology("gone", bapi.DocumentReference{Path: filepath.Join(harness.files, "gone.yaml")})
	harness.addFileTopology("invalid", bapi.DocumentReference{
		Path: harness.writeBuilderFile([]byte("name: "+builderFileMarker+"\n"), "invalid.yaml"),
	})

	foreign := builderDocumentDigest(t, builderDocument(t, "another"))
	drafts := len(harness.store.Keys(bapi.NamespaceDrafts))

	for name, test := range map[string]struct {
		token  string
		status int
		said   string
	}{
		"another digest": {
			token: builderFileTokenPrefix + "site/" + foreign, status: http.StatusConflict,
			said: "The Builder file of topology site changed since it was opened. Open its diagram again.",
		},
		"a topology read from the store": {
			token: builderFileTokenPrefix + "stored/" + stored.Digest, status: http.StatusConflict,
			said: "Topology stored is no longer read from its Builder file. Open its diagram again.",
		},
		"a file that is gone": {
			token: builderFileTokenPrefix + "gone/" + opened.digest, status: http.StatusNotFound,
			said: "Builder file " + filepath.Join(harness.files, "gone.yaml") + " does not exist on this phenix server.",
		},
		"a file that is not a document": {
			token: builderFileTokenPrefix + "invalid/" + opened.digest, status: http.StatusUnprocessableEntity,
			said: "Builder file " + filepath.Join(harness.files, "invalid.yaml") +
				" is not a valid Builder document. Upload it in the Builder to see why.",
		},
		"a topology that does not exist": {
			token: builderFileTokenPrefix + "absent/" + opened.digest, status: http.StatusNotFound,
			said: "builder document of topology absent not found",
		},
		"no digest": {
			token: builderFileTokenPrefix + "site", status: http.StatusNotFound, said: "builder document of topology site not found",
		},
		"a digest that is not one": {
			token: builderFileTokenPrefix + "site/sha256:abc", status: http.StatusNotFound,
			said: "builder document of topology site/sha256:abc not found",
		},
		"no topology": {
			token: builderFileTokenPrefix + "/" + opened.digest, status: http.StatusNotFound,
			said: "builder document of topology /" + opened.digest + " not found",
		},
		"a topology with a slash": {
			token: builderFileTokenPrefix + "site/site/" + opened.digest, status: http.StatusNotFound,
			said: "builder document of topology site/site/" + opened.digest + " not found",
		},
	} {
		recorder, _ := createBuilderFileDraft(t, harness, opened.document, test.token)
		said := builderRefusal(t, recorder)

		if recorder.Code != test.status || said.Message != test.said || said.Cause != "" {
			t.Errorf("%s: status = %d, said %+v; want %d and %q", name, recorder.Code, said, test.status, test.said)
		}
	}

	// A caller who may not get this topology learns nothing of its file.
	role := builderRole(builderPolicy([]string{"configs"}, []string{"Topology/other"}, []string{"list", "get", "create", "update"}))
	harness.fileReads = 0

	data, err := bapi.EncodeDocument(opened.document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder = harness.do(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderBob, role: role,
		body: builderJSON(t, map[string]any{"document": json.RawMessage(data), "sourceToken": opened.token}),
	})

	if said := builderRefusal(t, recorder); recorder.Code != http.StatusNotFound ||
		said.Message != "builder document of topology site not found" || harness.fileReads != 0 {
		t.Fatalf("a caller who may not get the topology: status = %d, said %+v, %d file reads", recorder.Code, said, harness.fileReads)
	}

	// The file changes after it was opened: the token names what it held.
	other := bdoc.NewDocument("site")
	otherData, err := bapi.EncodeDocument(other)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	harness.writeBuilderFile(otherData, "topologies", "site", "site.builder.yaml")

	recorder, _ = createBuilderFileDraft(t, harness, opened.document, opened.token)
	if said := builderRefusal(t, recorder); recorder.Code != http.StatusConflict ||
		said.Message != "The Builder file of topology site changed since it was opened. Open its diagram again." {
		t.Fatalf("after the file changed: status = %d, said %+v", recorder.Code, said)
	}

	if got := len(harness.store.Keys(bapi.NamespaceDrafts)); got != drafts {
		t.Fatalf("the refused requests made %d drafts", got-drafts)
	}

	if logged := logs.String(); strings.Contains(logged, builderFileMarker) {
		t.Fatalf("the log repeats what a file holds: %s", logged)
	}
}

// TestBuilderPublishToFileBackedTopology asserts a draft opened from a Builder
// file updates its topology, never writes the file, and warns the file is behind.
func TestBuilderPublishToFileBackedTopology(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	for _, pinned := range []bool{false, true} {
		harness := newBuilderHarness(t)
		path := newFileBackedTopology(t, harness, "site", pinned)

		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the file: %v", err)
		}

		opened := openBuilderFile(t, harness, "site")

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("pinned %t: creating the draft: status = %d: %s", pinned, recorder.Code, recorder.Body)
		}

		// Another draft has no part in the topology or its file.
		unrelated := bdoc.NewDocument("unrelated")
		stranger := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, unrelated), unrelated, "zz")
		writes := harness.configWrites

		if _, reason := publishBuilderDraft(t, harness, stranger, update, http.StatusConflict); reason !=
			"topology site is not the source this draft was loaded from" || harness.configWrites != writes {
			t.Fatalf("pinned %t: refusal = %q after %d writes, want another draft refused", pinned, reason, harness.configWrites-writes)
		}

		// The draft holds the file's content: the topology is written, not taken for published.
		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		draft = response.Draft

		warning := bdoc.NewIssue(bdoc.CodePublishFileUnchanged, "", "Topology site names the Builder file "+path+
			", which Publish does not change. Download the diagram and replace the file to keep it in step.")

		if response.Stages[1].Status != "updated" || !slices.Contains(response.Warnings, warning) {
			t.Fatalf("pinned %t: stages = %#v, warnings = %q; want the topology updated with the warning",
				pinned, response.Stages, response.Warnings)
		}

		want := bapi.DocumentReference{Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: path}
		if got := builderTopologyReference(t, harness, "site"); got != want || draft.Digest != opened.digest {
			t.Fatalf("pinned %t: reference = %+v, want %+v", pinned, got, want)
		}

		// The topology is read from the store now, and the draft goes on updating it.
		var current builderDocumentResponse

		harness.decode(harness.getTopologyDocument("site"), &current)

		if current.Source != builderDocumentSourceStore || current.ID != want.ID {
			t.Fatalf("pinned %t: the topology's document = %+v, want the stored document", pinned, current)
		}

		draft = editBuilderDraft(t, harness, draft, opened.document, "bb")
		response, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)

		if !slices.Contains(response.Warnings, warning) || builderTopologyReference(t, harness, "site").Path != path {
			t.Fatalf("pinned %t: warnings = %q, want the file still named, with the warning", pinned, response.Warnings)
		}

		if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb"}) {
			t.Fatalf("pinned %t: topology nodes = %v, want aa and bb", pinned, got)
		}

		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("pinned %t: the file was written: %v", pinned, err)
		}
	}
}

// TestBuilderPublishFromEditedFileDraft asserts an edited draft opened from a
// Builder file is refused once the file changes or the topology is not what it publishes.
func TestBuilderPublishFromEditedFileDraft(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	// open returns an edited draft opened from the file of a new file-backed topology.
	open := func(pinned bool) (*builderHarness, builderDraftResponse, *bdoc.Document, string) {
		harness := newBuilderHarness(t)
		path := newFileBackedTopology(t, harness, "site", pinned)
		opened := openBuilderFile(t, harness, "site")

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("creating the draft: status = %d: %s", recorder.Code, recorder.Body)
		}

		return harness, editBuilderDraft(t, harness, draft, opened.document, "bb"), opened.document, path
	}

	refused := func(harness *builderHarness, draft builderDraftResponse, name, want string) {
		t.Helper()

		writes := harness.configWrites

		_, reason := publishBuilderDraft(t, harness, draft, update, http.StatusConflict)
		if reason != want || harness.configWrites != writes {
			t.Fatalf("%s: refusal = %q after %d writes, want %q", name, reason, harness.configWrites-writes, want)
		}
	}

	const (
		changed = "topology site or its Builder file changed after this draft was opened from the file"
		differs = "topology site is not what its Builder file publishes, so this draft cannot update it"
	)

	// A fork of the draft is tied to the file as the draft is.
	harness, draft, document, _ := open(true)

	var fork builderDraftResponse

	recorder := forkBuilderDraft(t, harness, builderAlice, nil, draft.ID, document)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("forking the draft: status = %d: %s", recorder.Code, recorder.Body)
	}

	harness.decode(recorder, &fork)

	if response, _ := publishBuilderDraft(t, harness, fork, update, http.StatusOK); response.Stages[1].Status != "updated" ||
		!slices.Equal(topologyHostnames(t, harness, "site"), []string{"aa", "bb"}) {
		t.Fatalf("stages = %#v, want the topology updated with aa and bb", response.Stages)
	}

	harness, draft, _, path := open(false)

	another, err := bapi.EncodeDocument(bdoc.NewDocument("site"))
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	if err := os.WriteFile(path, another, 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	refused(harness, draft, "the file changed", changed)

	harness, draft, _, path = open(false)

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the file: %v", err)
	}

	refused(harness, draft, "the file is gone", changed)

	harness, draft, _, _ = open(false)
	editBuilderTopologyByHand(harness, "site")

	if opened := openBuilderFile(t, harness, "site"); !opened.differs {
		t.Fatal("the document route does not say the topology differs from its file")
	}

	refused(harness, draft, "the topology was edited", differs)

	// A file document imported from the topology is refused as any other once
	// the file changes, whether or not the topology is what the file published.
	for _, edited := range []bool{false, true} {
		harness := newBuilderHarness(t)
		path := newFileBackedTopology(t, harness, "site", false)

		var source string

		for i := range harness.configs {
			if harness.configs[i].FullName() != "Topology/site" {
				continue
			}

			if edited {
				editBuilderTopologyByHand(harness, "site")
			}

			digest, err := bdoc.SourceDigest(harness.configs[i])
			if err != nil {
				t.Fatalf("SourceDigest returned error: %v", err)
			}

			source = digest
		}

		imported := openBuilderFile(t, harness, "site").document
		imported.Source = &bdoc.Source{Kind: bdoc.SourceKindTopology, Name: "site", Digest: source}

		data, err := bapi.EncodeDocument(imported)
		if err != nil {
			t.Fatalf("EncodeDocument returned error: %v", err)
		}

		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}

		opened := openBuilderFile(t, harness, "site")
		if opened.differs != edited {
			t.Fatalf("edited %t: the document route says the topology differs: %t", edited, opened.differs)
		}

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("edited %t: creating the draft: status = %d: %s", edited, recorder.Code, recorder.Body)
		}

		draft = editBuilderDraft(t, harness, draft, opened.document, "bb")

		another, err := bapi.EncodeDocument(bdoc.NewDocument("site"))
		if err != nil {
			t.Fatalf("EncodeDocument returned error: %v", err)
		}

		if err := os.WriteFile(path, another, 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}

		refused(harness, draft, "the file of an imported document changed", changed)
	}

	// A draft holding exactly the document a pinned file names is not taken for
	// published, so a topology the file does not publish is refused.
	harness = newBuilderHarness(t)
	path = newFileBackedTopology(t, harness, "pump", true)
	opened := openBuilderFile(t, harness, "pump")

	setBuilderTopologyReference(t, harness, "pump", bapi.DocumentReference{
		Digest: opened.digest, ID: bapi.PublishedDocumentID("pump", opened.digest), Path: path,
	})
	editBuilderTopologyByHand(harness, "pump")

	recorder, draft = createBuilderFileDraft(t, harness, opened.document, opened.token)
	if recorder.Code != http.StatusCreated || draft.Digest != opened.digest {
		t.Fatalf("creating the draft: status = %d, digest %s: %s", recorder.Code, draft.Digest, recorder.Body)
	}

	writes := harness.configWrites

	for action, want := range map[string]string{
		"update": "topology pump is not what its Builder file publishes, so this draft cannot update it",
		"create": "config pump already exists. Choose update explicitly",
	} {
		if _, reason := publishBuilderDraft(t, harness, draft,
			`{"mode":"topology","topology":{"name":"pump","action":"`+action+`"}}`, http.StatusConflict); reason != want {
			t.Fatalf("%s: refusal = %q, want %q", action, reason, want)
		}
	}

	if got := topologyHostnames(t, harness, "pump"); !slices.Contains(got, "by-hand") || harness.configWrites != writes {
		t.Fatalf("topology nodes = %v after %d writes, want the topology left as it was", got, harness.configWrites-writes)
	}
}

// editBuilderTopologyByHand adds node by-hand to the topology name, as an edit
// outside the Builder does.
func editBuilderTopologyByHand(harness *builderHarness, name string) {
	for i := range harness.configs {
		if harness.configs[i].FullName() == "Topology/"+name {
			edited := cloneBuilderConfig(&harness.configs[i])
			nodes, _ := edited.Spec["nodes"].([]any)
			edited.Spec["nodes"] = append(nodes, includeNode("by-hand"))
			harness.configs[i] = *edited
		}
	}
}
