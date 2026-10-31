package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

// builderRefusalAnswer is the status of a refused request and what it says.
type builderRefusalAnswer struct {
	Status  int
	Message string
	Cause   string
}

// refusalAnswerOf returns the status of the answer and what its refusal says.
func refusalAnswerOf(t *testing.T, recorder *httptest.ResponseRecorder) builderRefusalAnswer {
	t.Helper()

	said := builderRefusal(t, recorder)

	return builderRefusalAnswer{Status: recorder.Code, Message: said.Message, Cause: said.Cause}
}

// builderRawAnswer is the status, headers and body of an answer.
type builderRawAnswer struct {
	Status int
	Header http.Header
	Body   string
}

func builderRawAnswerOf(recorder *httptest.ResponseRecorder) builderRawAnswer {
	return builderRawAnswer{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
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
				t.Fatalf("GET topology %s document: status = %d, want %d: %s", name, recorder.Code, http.StatusOK, recorder.Body)
			}

			var response builderDocumentResponse

			decodeBuilderAnswer(t, recorder, &response)

			want := builderDocumentResponse{
				Source: test.source, ID: test.id, Digest: test.digest, Size: int64(len(test.document)),
				Target: name, Kind: builderKindTopology, Config: "Topology/" + name, Path: test.path,
				// The topologies of this test are not what their documents publish.
				TopologyDiffers: test.source == "file",
				Document:        builderCompactJSON(t, test.document),
			}

			if test.source == "store" {
				want.CreatedAt, want.CreatedBy = response.CreatedAt, builderAlice
				want.ETag = builderTopologyDocumentTag(t, harness, name, test.id)

				if response.CreatedAt.IsZero() {
					t.Errorf("GET topology %s document: createdAt = zero, want the time it was stored", name)
				}
			}

			if diff := cmp.Diff(want, response); diff != "" {
				t.Errorf("GET topology %s document mismatch (-want +got):\n%s", name, diff)
			}

			// A file has no ID, author or time, and says so by leaving them out.
			var fields map[string]json.RawMessage

			decodeBuilderAnswer(t, recorder, &fields)

			for _, field := range []string{"id", "createdAt", "createdBy", "draftId", "snapshotId"} {
				if _, present := fields[field]; present && test.source == "file" {
					t.Errorf("GET topology %s document of a file holds %s, want it left out", name, field)
				}
			}
		})
	}

	// A file is read on every request, and never for a stored document.
	harness.fileReads = 0

	harness.getTopologyDocument("stored")
	harness.getTopologyDocument("both")

	if harness.fileReads != 0 {
		t.Errorf("reading stored documents read %d files, want none", harness.fileReads)
	}

	harness.writeBuilderFile(builderCompactJSON(t, other), "topologies", "site", "site.builder.json")

	var changed builderDocumentResponse

	harness.decode(harness.getTopologyDocument("json"), &changed)

	if want := builderDocumentDigest(t, other); changed.Digest != want {
		t.Errorf("GET topology json document after the file changed: digest = %s, want %s", changed.Digest, want)
	}

	if harness.fileReads != 1 {
		t.Errorf("GET topology json document after the file changed read %d files, want 1", harness.fileReads)
	}

	// The pinned file no longer has the digest its topology records.
	want := builderRefusalAnswer{
		Status:  http.StatusUnprocessableEntity,
		Message: "Builder file " + asJSON + " does not match the digest topology copied records for it.",
	}
	if diff := cmp.Diff(want, refusalAnswerOf(t, harness.getTopologyDocument("copied"))); diff != "" {
		t.Errorf("GET topology copied document of a changed pinned file mismatch (-want +got):\n%s", diff)
	}

	// A damaged stored document is reported, and its file is not read in its place.
	for _, key := range harness.store.Keys(bapi.NamespaceContent) {
		harness.store.Drop(bapi.NamespaceContent, key)
	}

	harness.fileReads = 0

	if recorder := harness.getTopologyDocument("both"); recorder.Code != http.StatusInternalServerError {
		t.Errorf("GET topology both document when damaged: status = %d, want %d: %s",
			recorder.Code, http.StatusInternalServerError, recorder.Body)
	}

	if harness.fileReads != 0 {
		t.Errorf("GET topology both document when damaged read %d files, want none", harness.fileReads)
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

			want := builderRefusalAnswer{Status: test.status, Message: strings.ReplaceAll(test.said, "%s", test.path)}
			if diff := cmp.Diff(want, refusalAnswerOf(t, recorder)); diff != "" {
				t.Errorf("GET topology %s document mismatch (-want +got):\n%s", name, diff)
			}

			if strings.Contains(recorder.Body.String(), builderFileMarker) {
				t.Errorf("GET topology %s document = %s, want nothing the file holds", name, recorder.Body)
			}

			logged := logs.String()
			if strings.Contains(logged, builderFileMarker) {
				t.Errorf("the log of GET topology %s document = %s, want nothing the file holds", name, logged)
			}

			records := logs.Take(t, plogtest.Message(builderFileUnusable))
			if len(records) != 1 {
				t.Fatalf("logged %v, want one warning with the reason %q", records, test.reason)
			}

			wantRecord := map[string]any{
				"level": slog.LevelWarn.String(), "type": string(plog.TypeSystem), "topology": name, "path": test.path,
				"reason": string(test.reason),
			}
			gotRecord := map[string]any{}

			for key := range wantRecord {
				gotRecord[key] = records[0][key]
			}

			if diff := cmp.Diff(wantRecord, gotRecord); diff != "" {
				t.Errorf("logged %s mismatch (-want +got):\n%s", builderFileUnusable, diff)
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

	// missingDiff returns how the answer differs from the one of a missing topology.
	missingDiff := func(t *testing.T, name string, recorder *httptest.ResponseRecorder) string {
		t.Helper()

		want := builderRefusalAnswer{Status: http.StatusNotFound, Message: "phenix cannot find Builder document of topology " + name}

		return cmp.Diff(want, refusalAnswerOf(t, recorder))
	}

	names := append(slices.Collect(maps.Keys(unreadable)),
		"plain", "elsewhere", "dangling", "absent", strings.Repeat("n", bapi.MaxTargetLength+1))

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if diff := missingDiff(t, name, harness.getTopologyDocument(name)); diff != "" {
				t.Errorf("GET topology %s document mismatch (-want +got):\n%s", name, diff)
			}
		})
	}

	if harness.fileReads != 0 {
		t.Errorf("topologies that name no file read %d files, want none", harness.fileReads)
	}

	// A caller who may get other topologies, but not these two.
	role := builderRole(builderPolicy([]string{"configs"}, []string{"Topology/other"}, []string{"list", "get", "create", "update"}))

	for _, name := range []string{"site", "stored"} {
		t.Run(name, func(t *testing.T) {
			request := builderRequest{
				method: http.MethodGet, path: "/builder/topologies/" + name + "/document", user: builderBob, role: role,
			}

			forbidden := harness.do(request)
			if diff := missingDiff(t, name, forbidden); diff != "" {
				t.Errorf("GET topology %s document as bob mismatch (-want +got):\n%s", name, diff)
			}

			if allowed := harness.getTopologyDocument(name); allowed.Code != http.StatusOK {
				t.Errorf("GET topology %s document as alice: status = %d, want %d: %s", name, allowed.Code, http.StatusOK, allowed.Body)
			}

			harness.fileReads = 0

			// The same answer once the topology is gone.
			harness.configs = slices.DeleteFunc(harness.configs, func(config store.Config) bool {
				return config.FullName() == "Topology/"+name
			})

			gone := harness.do(request)

			if diff := cmp.Diff(builderRawAnswerOf(forbidden), builderRawAnswerOf(gone)); diff != "" {
				t.Errorf("GET topology %s document once gone mismatch with when forbidden (-forbidden +gone):\n%s", name, diff)
			}
		})
	}

	if harness.fileReads != 0 {
		t.Errorf("a caller who may not get the topology made the server read %d files, want none", harness.fileReads)
	}

	// Without the base permission the request is refused before any lookup.
	none := builderRole(builderPolicy([]string{"configs"}, []string{"*"}, []string{"list"}))

	if recorder := harness.do(builderRequest{
		method: http.MethodGet, path: "/builder/topologies/site/document", user: builderBob, role: none,
	}); recorder.Code != http.StatusForbidden {
		t.Errorf("GET topology site document without configs get: status = %d, want %d", recorder.Code, http.StatusForbidden)
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
		t.Fatalf("GET /builder/documents: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	// The experiments are listed for the stored rows, which may name one: no
	// file is read, and the topologies and the experiments are listed once each.
	type reads struct {
		FileReads   int
		ConfigGets  int
		ConfigLists []string
	}

	wantReads := reads{ConfigLists: []string{builderKindTopology, kindExperiment}}
	gotReads := reads{FileReads: harness.fileReads, ConfigGets: harness.configGets, ConfigLists: harness.configLists}

	if diff := cmp.Diff(wantReads, gotReads); diff != "" {
		t.Errorf("GET /builder/documents reads mismatch (-want +got):\n%s", diff)
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

	if got, want := slices.Sorted(maps.Keys(rows)), []string{"both", "file", "gone", "pinned", "stored"}; !slices.Equal(got, want) {
		t.Errorf("GET /builder/documents listed %q, want %q", got, want)
	}

	if len(response.Documents) != len(rows) {
		t.Errorf("GET /builder/documents listed %d rows of %d targets, want one row each", len(response.Documents), len(rows))
	}

	for name, path := range map[string]string{"file": present, "gone": absent, "pinned": absent} {
		want := map[string]any{
			"source": "file", "target": name, "kind": builderKindTopology, "config": "Topology/" + name, "path": path,
		}

		if diff := cmp.Diff(want, rows[name]); diff != "" {
			t.Errorf("GET /builder/documents row %s mismatch (-want +got):\n%s", name, diff)
		}
	}

	for name, document := range map[string]*bapi.PublishedDocument{"stored": stored, "both": both} {
		row := rows[name]

		want := map[string]any{"source": "store", "id": document.ID, "digest": document.Digest, "createdBy": builderAlice}
		got := map[string]any{}

		for key := range want {
			got[key] = row[key]
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET /builder/documents row %s mismatch (-want +got):\n%s", name, diff)
		}

		if _, present := row["path"]; present {
			t.Errorf("GET /builder/documents row %s = %v, want no path for a stored document", name, row)
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

	decodeBuilderAnswer(t, recorder, &response)

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
		decodeBuilderAnswer(t, recorder, &draft)
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
		t.Errorf("GET topology site document: topologyDiffers = true, want false for the file it was written from")
	}

	recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
	if recorder.Code != http.StatusCreated {
		t.Errorf("POST /builder/drafts from the file: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	if draft.SourceToken != opened.token {
		t.Errorf("POST /builder/drafts from the file: source token = %q, want %q", draft.SourceToken, opened.token)
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
			said: "The Builder file of topology site changed after Builder opened it. Open its diagram again.",
		},
		"a topology read from the store": {
			token: builderFileTokenPrefix + "stored/" + stored.Digest, status: http.StatusConflict,
			said: "The server no longer reads topology stored from its Builder file. Open its diagram again.",
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
			said: "phenix cannot find Builder document of topology absent",
		},
		"no digest": {
			token: builderFileTokenPrefix + "site", status: http.StatusNotFound, said: "phenix cannot find Builder document of topology site",
		},
		"a digest that is not one": {
			token: builderFileTokenPrefix + "site/sha256:abc", status: http.StatusNotFound,
			said: "phenix cannot find Builder document of topology site/sha256:abc",
		},
		"no topology": {
			token: builderFileTokenPrefix + "/" + opened.digest, status: http.StatusNotFound,
			said: "phenix cannot find Builder document of topology /" + opened.digest,
		},
		"a topology with a slash": {
			token: builderFileTokenPrefix + "site/site/" + opened.digest, status: http.StatusNotFound,
			said: "phenix cannot find Builder document of topology site/site/" + opened.digest,
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder, _ := createBuilderFileDraft(t, harness, opened.document, test.token)

			want := builderRefusalAnswer{Status: test.status, Message: test.said}
			if diff := cmp.Diff(want, refusalAnswerOf(t, recorder)); diff != "" {
				t.Errorf("POST /builder/drafts with source token %s mismatch (-want +got):\n%s", test.token, diff)
			}
		})
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

	// What a refusal of the token says is compared without its cause.
	ignoreCause := cmpopts.IgnoreFields(builderRefusalAnswer{}, "Cause")

	want := builderRefusalAnswer{Status: http.StatusNotFound, Message: "phenix cannot find Builder document of topology site"}
	if diff := cmp.Diff(want, refusalAnswerOf(t, recorder), ignoreCause); diff != "" {
		t.Errorf("POST /builder/drafts as bob, who may not get the topology, mismatch (-want +got):\n%s", diff)
	}

	if harness.fileReads != 0 {
		t.Errorf("POST /builder/drafts as bob, who may not get the topology, read %d files, want none", harness.fileReads)
	}

	// The file changes after it was opened: the token names what it held.
	other := bdoc.NewDocument("site")
	otherData, err := bapi.EncodeDocument(other)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	harness.writeBuilderFile(otherData, "topologies", "site", "site.builder.yaml")

	recorder, _ = createBuilderFileDraft(t, harness, opened.document, opened.token)

	want = builderRefusalAnswer{
		Status: http.StatusConflict, Message: "The Builder file of topology site changed after Builder opened it. Open its diagram again.",
	}
	if diff := cmp.Diff(want, refusalAnswerOf(t, recorder), ignoreCause); diff != "" {
		t.Errorf("POST /builder/drafts after the file changed mismatch (-want +got):\n%s", diff)
	}

	if got := len(harness.store.Keys(bapi.NamespaceDrafts)); got != drafts {
		t.Errorf("the refused requests made %d drafts, want none", got-drafts)
	}

	if logged := logs.String(); strings.Contains(logged, builderFileMarker) {
		t.Errorf("the log = %s, want nothing a file holds", logged)
	}
}

// TestBuilderPublishToFileBackedTopology asserts a draft opened from a Builder
// file updates its topology, never writes the file, and warns the file is behind.
func TestBuilderPublishToFileBackedTopology(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "not pinned", true: "pinned"}[pinned], func(t *testing.T) {
			harness := newBuilderHarness(t)
			path := newFileBackedTopology(t, harness, "site", pinned)

			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			opened := openBuilderFile(t, harness, "site")

			recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("POST /builder/drafts from the file: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
			}

			// Another draft has no part in the topology or its file.
			unrelated := bdoc.NewDocument("unrelated")
			stranger := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, unrelated), unrelated, "zz")
			writes := harness.configWrites

			const notSource = "topology site is not the source of this draft"
			if _, reason := publishBuilderDraft(t, harness, stranger, update, http.StatusConflict); reason != notSource {
				t.Errorf("publish of another draft: refusal = %q, want %q", reason, notSource)
			}

			if harness.configWrites != writes {
				t.Errorf("publish of another draft wrote %d configs, want none", harness.configWrites-writes)
			}

			// The draft holds the file's content: the topology is written, not taken for published.
			response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
			draft = response.Draft

			warning := bdoc.NewIssue(bdoc.CodePublishFileUnchanged, "", "Topology site names the Builder file "+path+
				", which Publish does not change. Download the diagram and replace the file with it, so that the file and the topology stay the same.")

			if len(response.Stages) < 2 || response.Stages[1].Status != "updated" {
				t.Errorf("publish: stages = %#v, want the topology updated", response.Stages)
			}

			if !slices.Contains(response.Warnings, warning) {
				t.Errorf("publish: warnings = %q, want %q among them", response.Warnings, warning)
			}

			want := bapi.DocumentReference{Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: path}
			if diff := cmp.Diff(want, builderTopologyReference(t, harness, "site")); diff != "" {
				t.Errorf("topology site reference after publish mismatch (-want +got):\n%s", diff)
			}

			if draft.Digest != opened.digest {
				t.Errorf("publish: draft digest = %s, want the file's %s", draft.Digest, opened.digest)
			}

			// The topology is read from the store now, and the draft goes on updating it.
			var current builderDocumentResponse

			decodeBuilderAnswer(t, harness.getTopologyDocument("site"), &current)

			type documentSource struct{ Source, ID string }

			if diff := cmp.Diff(
				documentSource{Source: builderDocumentSourceStore, ID: want.ID}, documentSource{Source: current.Source, ID: current.ID},
			); diff != "" {
				t.Errorf("GET topology site document mismatch (-want +got):\n%s", diff)
			}

			draft = editBuilderDraft(t, harness, draft, opened.document, "bb")
			response, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)

			if !slices.Contains(response.Warnings, warning) {
				t.Errorf("publish again: warnings = %q, want %q among them", response.Warnings, warning)
			}

			if got := builderTopologyReference(t, harness, "site").Path; got != path {
				t.Errorf("topology site reference path after publishing again = %q, want the file %q", got, path)
			}

			if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb"}) {
				t.Errorf("topology site nodes = %v, want [aa bb]", got)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			if diff := cmp.Diff(string(before), string(after)); diff != "" {
				t.Errorf("the file was written (-before +after):\n%s", diff)
			}
		})
	}
}

// TestBuilderPublishFromEditedFileDraft asserts an edited draft opened from a
// Builder file is refused once the file changes or the topology is not what it publishes.
func TestBuilderPublishFromEditedFileDraft(t *testing.T) {
	// open returns an edited draft opened from the file of a new file-backed topology.
	open := func(pinned bool) (*builderHarness, builderDraftResponse, *bdoc.Document, string) {
		t.Helper()

		harness := newBuilderHarness(t)
		path := newFileBackedTopology(t, harness, "site", pinned)
		opened := openBuilderFile(t, harness, "site")

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("POST /builder/drafts from the file: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
		}

		return harness, editBuilderDraft(t, harness, draft, opened.document, "bb"), opened.document, path
	}

	// A fork of the draft is tied to the file as the draft is.
	harness, draft, document, _ := open(true)

	var fork builderDraftResponse

	recorder := forkBuilderDraft(t, harness, builderAlice, nil, draft.ID, document)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /builder/drafts forking %s: status = %d, want %d: %s", draft.ID, recorder.Code, http.StatusCreated, recorder.Body)
	}

	harness.decode(recorder, &fork)

	if response, _ := publishBuilderDraft(t, harness, fork, fileDraftUpdate, http.StatusOK); len(response.Stages) < 2 ||
		response.Stages[1].Status != "updated" {
		t.Errorf("publish of the fork: stages = %#v, want the topology updated", response.Stages)
	}

	if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb"}) {
		t.Errorf("topology site nodes after publishing the fork = %v, want [aa bb]", got)
	}

	harness, draft, _, path := open(false)

	another, err := bapi.EncodeDocument(bdoc.NewDocument("site"))
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	if err := os.WriteFile(path, another, 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	if err := fileDraftRefusalError(t, harness, draft, fileDraftChanged); err != nil {
		t.Errorf("publish after the file changed: %v", err)
	}

	harness, draft, _, path = open(false)

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the file: %v", err)
	}

	if err := fileDraftRefusalError(t, harness, draft, fileDraftChanged); err != nil {
		t.Errorf("publish after the file is gone: %v", err)
	}

	harness, draft, _, _ = open(false)
	editBuilderTopologyByHand(harness, "site")

	if opened := openBuilderFile(t, harness, "site"); !opened.differs {
		t.Errorf("GET topology site document after an edit by hand: topologyDiffers = false, want true")
	}

	if err := fileDraftRefusalError(t, harness, draft, fileDraftDiffers); err != nil {
		t.Errorf("publish after the topology was edited: %v", err)
	}

	// A file document imported from the topology is refused as any other once
	// the file changes, whether or not the topology is what the file published.
	for _, edited := range []bool{false, true} {
		t.Run(map[bool]string{false: "topology not edited", true: "topology edited"}[edited], func(t *testing.T) {
			harness := newBuilderHarness(t)
			path := newFileBackedTopology(t, harness, "site", false)

			source := siteSourceDigest(t, harness, edited)

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
				t.Errorf("GET topology site document: topologyDiffers = %t, want %t", opened.differs, edited)
			}

			recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("POST /builder/drafts from the file: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
			}

			draft = editBuilderDraft(t, harness, draft, opened.document, "bb")

			another, err := bapi.EncodeDocument(bdoc.NewDocument("site"))
			if err != nil {
				t.Fatalf("EncodeDocument returned error: %v", err)
			}

			if err := os.WriteFile(path, another, 0o600); err != nil {
				t.Fatalf("writing the file: %v", err)
			}

			if err := fileDraftRefusalError(t, harness, draft, fileDraftChanged); err != nil {
				t.Errorf("publish after the file of an imported document changed: %v", err)
			}
		})
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
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /builder/drafts from the file: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	if draft.Digest != opened.digest {
		t.Errorf("POST /builder/drafts from the file: digest = %s, want the file's %s", draft.Digest, opened.digest)
	}

	writes := harness.configWrites

	for action, want := range map[string]string{
		"update": "topology pump is not what its Builder file publishes. Thus this draft cannot update it",
		"create": "config pump already exists. Choose update explicitly",
	} {
		t.Run(action, func(t *testing.T) {
			if _, reason := publishBuilderDraft(t, harness, draft,
				`{"mode":"topology","topology":{"name":"pump","action":"`+action+`"}}`, http.StatusConflict); reason != want {
				t.Errorf("publish with action %s: refusal = %q, want %q", action, reason, want)
			}
		})
	}

	if got := topologyHostnames(t, harness, "pump"); !slices.Contains(got, "by-hand") {
		t.Errorf("topology pump nodes = %v, want by-hand among them, the topology left as it was", got)
	}

	if harness.configWrites != writes {
		t.Errorf("the refused publications wrote %d configs, want none", harness.configWrites-writes)
	}
}

// The update of topology site that a draft opened from its Builder file
// publishes, and two refusals of it.
const (
	fileDraftUpdate  = `{"mode":"topology","topology":{"name":"site","action":"update"}}`
	fileDraftChanged = "topology site or its Builder file changed after Builder opened this draft from the file"
	fileDraftDiffers = "topology site is not what its Builder file publishes. Thus this draft cannot update it"
)

// fileDraftRefusalError publishes the draft as an update of topology site,
// which must be refused, and reports a refusal other than want or a config written.
func fileDraftRefusalError(t *testing.T, harness *builderHarness, draft builderDraftResponse, want string) error {
	t.Helper()

	writes := harness.configWrites

	_, reason := publishBuilderDraft(t, harness, draft, fileDraftUpdate, http.StatusConflict)
	if reason != want || harness.configWrites != writes {
		return fmt.Errorf("refusal = %q after %d writes, want %q and none", reason, harness.configWrites-writes, want)
	}

	return nil
}

// siteSourceDigest edits topology site by hand when edited, and returns the
// source digest of the topology.
func siteSourceDigest(t *testing.T, harness *builderHarness, edited bool) string {
	t.Helper()

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

	return source
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
