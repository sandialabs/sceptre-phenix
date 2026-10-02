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

// builderFileMarker is text the Builder files of these tests hold that no
// response and no log line may repeat, unless the file is a valid document
// the caller may read.
const builderFileMarker = "MARKER-FROM-THE-FILE"

// builderFileUnusable is what the server logs for a Builder file it could
// not use.
const builderFileUnusable = "builder document file not usable"

// writeBuilderFile writes content to the file named by the path elements
// below the directory Builder files are read from, and returns its path.
func (h *builderV2Harness) writeBuilderFile(content []byte, elements ...string) string {
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
func (h *builderV2Harness) addFileTopology(name string, reference bapi.DocumentReference) {
	h.t.Helper()

	topology := builderV2Config(h.t, builderV2KindTopology, name)
	topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: encodeBuilderReference(h.t, reference)}
	h.configs = append(h.configs, topology)
}

// getTopologyDocument asks for the Builder document the topology references.
func (h *builderV2Harness) getTopologyDocument(name string) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.do(builderV2Request{
		method: http.MethodGet, path: "/builder-v2/topologies/" + name + "/document", user: builderV2TestOwner,
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

// builderFileRefusal is what the server says of a request it refused.
func builderFileRefusal(t *testing.T, harness *builderV2Harness, recorder *httptest.ResponseRecorder) builderPublishRefusal {
	t.Helper()

	var refusal builderPublishRefusal

	harness.decode(recorder, &refusal)

	return refusal
}

// TestBuilderV2TopologyDocument reads the Builder document a topology
// references by the topology's name: the stored document when the reference
// names one, and otherwise the Builder file at its path, read from the
// directory the server reads Builder files from. A digest beside the path
// pins the file.
func TestBuilderV2TopologyDocument(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	stored := builderV2Publish(t, harness, "stored")
	content := builderV2Document(t, "file")
	digest := builderDocumentDigest(t, content)
	other := builderV2Document(t, "other")

	asJSON := harness.writeBuilderFile(builderCompactJSON(t, content), "topologies", "site", "site.builder.json")
	asYAML := harness.writeBuilderFile(builderYAML(t, content), "topologies", "site", "site.builder.yaml")

	// The stored document wins over the file the reference names too.
	both := builderV2Publish(t, harness, "both")
	setBuilderTopologyReference(t, harness, "both", bapi.DocumentReference{Digest: both.Digest, ID: both.ID, Path: asJSON})

	harness.addFileTopology("json", bapi.DocumentReference{Path: asJSON})
	harness.addFileTopology("yaml", bapi.DocumentReference{Path: asYAML})
	harness.addFileTopology("pinned", bapi.DocumentReference{Digest: digest, Path: asYAML})
	// As on a server the topology was copied to, with its file but not its
	// stored document.
	harness.addFileTopology("copied", bapi.DocumentReference{
		Digest: digest, ID: bapi.PublishedDocumentID("copied", digest), Path: asJSON,
	})
	// An ID says nothing of the file: this one names no stored document.
	harness.addFileTopology("renamed", bapi.DocumentReference{ID: stored.ID, Path: asJSON})

	for name, test := range map[string]struct {
		source, path, digest, id string
		document                 []byte
	}{
		"stored":  {source: "store", digest: stored.Digest, id: stored.ID, document: builderV2Document(t, "stored")},
		"both":    {source: "store", digest: both.Digest, id: both.ID, document: builderV2Document(t, "both")},
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
				Target: name, Kind: builderV2KindTopology, Config: "Topology/" + name, Path: test.path,
				// The topologies of this test are not what their documents publish.
				TopologyDiffers: test.source == "file",
				Document:        builderCompactJSON(t, test.document),
			}

			if test.source == "store" {
				want.CreatedAt, want.CreatedBy = response.CreatedAt, builderV2TestOwner

				if response.CreatedAt.IsZero() {
					t.Error("a stored document has no time")
				}
			}

			if got, _ := json.Marshal(response); !bytes.Equal(got, mustBuilderJSON(t, want)) {
				t.Fatalf("response = %s, want %s", got, mustBuilderJSON(t, want))
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
	if said := builderFileRefusal(t, harness, recorder); recorder.Code != http.StatusUnprocessableEntity ||
		said.Message != "Builder file "+asJSON+" does not match the digest topology copied records for it." || said.Cause != "" {
		t.Fatalf("a pinned file that changed: status = %d, said %+v", recorder.Code, said)
	}
}

// mustBuilderJSON returns value as JSON.
func mustBuilderJSON(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}

	return data
}

// TestBuilderV2TopologyDocumentRefusals asks for the document of topologies
// whose Builder file cannot be used, each answered with the fixed sentence
// and status of its reason, and logged with that reason. Nothing the files
// hold is in a response or in the log.
func TestBuilderV2TopologyDocumentRefusals(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	logs := plogtest.Capture(t)

	valid := builderV2Document(t, builderFileMarker)
	outside := filepath.Join(t.TempDir(), "secret.json")

	if err := os.WriteFile(outside, valid, 0o600); err != nil {
		t.Fatalf("writing %s: %v", outside, err)
	}

	link := filepath.Join(harness.files, "link.json")
	harness.writeBuilderFile(valid, "topologies", "valid.json")

	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("linking %s: %v", link, err)
	}

	if err := os.Mkdir(filepath.Join(harness.files, "directory.yaml"), 0o750); err != nil {
		t.Fatalf("creating a directory: %v", err)
	}

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
		"mounted": {
			path:   harness.writeBuilderFile(valid, "mounts", "vm", "guest.json"),
			status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileOutside,
			said: "Builder file %s is outside " + harness.files + ", the directory phenix reads Builder files from.",
		},
		"missing": {
			path: filepath.Join(harness.files, "topologies", "gone.yaml"), status: http.StatusNotFound,
			reason: bapi.DocumentFileMissing, said: "Builder file %s does not exist on this phenix server.",
		},
		"linked": {
			path: link, status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileUnreadable,
			said: "Builder file %s cannot be read by phenix.",
		},
		"directory": {
			path: filepath.Join(harness.files, "directory.yaml"), status: http.StatusUnprocessableEntity,
			reason: bapi.DocumentFileNotRegular, said: "Builder file %s is not a regular file.",
		},
		"large": {
			path:   harness.writeBuilderFile(append(bytes.Clone(valid), bytes.Repeat([]byte(" "), bapi.MaxDocumentBytes)...), "large.json"),
			status: http.StatusRequestEntityTooLarge, reason: bapi.DocumentFileTooLarge, said: "Builder file %s is larger than 5 MiB.",
		},
		"invalid": {
			path: harness.writeBuilderFile(
				bytes.ReplaceAll(builderV2Document(t, "site"), []byte(bdoc.SchemaURI), []byte(builderFileMarker)), "invalid.json",
			),
			status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileInvalid,
			said: "Builder file %s is not a valid Builder document. Upload it in the Builder to see why.",
		},
		"not-a-document": {
			path:   harness.writeBuilderFile([]byte("root:x:0:0:"+builderFileMarker+":/root:/bin/sh\n"), "passwd.yaml"),
			status: http.StatusUnprocessableEntity, reason: bapi.DocumentFileInvalid,
			said: "Builder file %s is not a valid Builder document. Upload it in the Builder to see why.",
		},
		"repinned": {
			path:   filepath.Join(harness.files, "topologies", "valid.json"),
			digest: builderDocumentDigest(t, builderV2Document(t, "another")),
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
			said := builderFileRefusal(t, harness, recorder)

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

// TestBuilderV2TopologyDocumentNoLeak asks for the document of a topology
// the caller may not get, one that does not exist, and ones that reference no
// document: each is answered as the others are, and no file is read for a
// caller who may not get the topology.
func TestBuilderV2TopologyDocumentNoLeak(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	stored := builderV2Publish(t, harness, "stored")

	path := harness.writeBuilderFile(builderV2Document(t, "site"), "site.json")
	harness.addFileTopology("site", bapi.DocumentReference{Path: path})
	harness.addFileTopology("elsewhere", bapi.DocumentReference{ID: stored.ID})
	harness.addFileTopology("dangling", bapi.DocumentReference{Digest: stored.Digest})

	harness.configs = append(harness.configs, builderV2Config(t, builderV2KindTopology, "plain"))

	// References a write past the Topology config hook left, each naming a
	// file a reference may not: none is read.
	secret := filepath.Join(t.TempDir(), "store.bdb")
	if err := os.WriteFile(secret, builderV2Document(t, "site"), 0o600); err != nil {
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
		topology := builderV2Config(t, builderV2KindTopology, name)
		topology.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: `{"path":"` + path + `"}`}
		harness.configs = append(harness.configs, topology)
	}

	missing := func(name string, recorder *httptest.ResponseRecorder) {
		t.Helper()

		said := builderFileRefusal(t, harness, recorder)

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
	role := builderV2Role(builderV2Policy([]string{"configs"}, []string{"Topology/other"}, []string{"list", "get", "create", "update"}))

	for _, name := range []string{"site", "stored"} {
		request := builderV2Request{
			method: http.MethodGet, path: "/builder-v2/topologies/" + name + "/document", user: builderV2TestPeer, role: &role,
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
	none := builderV2Role(builderV2Policy([]string{"configs"}, []string{"*"}, []string{"list"}))

	if recorder := harness.do(builderV2Request{
		method: http.MethodGet, path: "/builder-v2/topologies/site/document", user: builderV2TestPeer, role: &none,
	}); recorder.Code != http.StatusForbidden {
		t.Fatalf("without configs get: status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuilderV2TopologyDocumentStoredIsNotPassedOver asks for the document
// of a topology whose stored document is damaged and whose reference names a
// file too: the damage is reported, and the file is not read in its place.
func TestBuilderV2TopologyDocumentStoredIsNotPassedOver(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
	stored := builderV2Publish(t, harness, "site")
	path := harness.writeBuilderFile(builderV2Document(t, "file"), "site.json")

	setBuilderTopologyReference(t, harness, "site", bapi.DocumentReference{Digest: stored.Digest, ID: stored.ID, Path: path})

	for _, key := range harness.store.Keys(bapi.NamespaceChunks) {
		harness.store.Drop(bapi.NamespaceChunks, key)
	}

	recorder := harness.getTopologyDocument("site")
	if recorder.Code != http.StatusInternalServerError || harness.fileReads != 0 {
		t.Fatalf("status = %d after %d file reads, want the damaged document reported: %s", recorder.Code, harness.fileReads, recorder.Body)
	}
}

// TestBuilderV2ListDocumentsListsFiles lists a row for each topology whose
// reference names a Builder file and no current stored document, with where
// the file is and nothing else. The listing reads no file, so a file that is
// missing is listed like any other.
func TestBuilderV2ListDocumentsListsFiles(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)

	stored := builderV2Publish(t, harness, "stored")
	both := builderV2Publish(t, harness, "both")
	present := harness.writeBuilderFile(builderV2Document(t, "file"), "topologies", "present.json")
	absent := filepath.Join(harness.files, "topologies", "absent.yaml")

	setBuilderTopologyReference(t, harness, "both", bapi.DocumentReference{Digest: both.Digest, ID: both.ID, Path: present})

	harness.addFileTopology("file", bapi.DocumentReference{Path: present})
	harness.addFileTopology("gone", bapi.DocumentReference{Path: absent})
	harness.addFileTopology("pinned", bapi.DocumentReference{Digest: stored.Digest, ID: "another-document", Path: absent})
	harness.addFileTopology("hidden", bapi.DocumentReference{Path: present})
	harness.addFileTopology("digest-only", bapi.DocumentReference{Digest: stored.Digest})
	harness.configs = append(harness.configs, builderV2Config(t, builderV2KindTopology, "plain"))

	role := builderV2Role(builderV2Policy(
		[]string{"configs"},
		[]string{
			"Topology/stored", "Topology/both", "Topology/file", "Topology/gone", "Topology/pinned", "Topology/plain",
			"Topology/digest-only",
		},
		[]string{"list"},
	))

	harness.configLists, harness.configGets, harness.fileReads = nil, 0, 0

	recorder := harness.do(builderV2Request{method: http.MethodGet, path: "/builder-v2/documents", user: builderV2TestOwner, role: &role})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}

	if harness.fileReads != 0 || harness.configGets != 0 || !slices.Equal(harness.configLists, []string{builderV2KindTopology}) {
		t.Fatalf("the listing read %d files, got %d configs and listed %q; want no file read and the topologies listed once",
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
			"source": "file", "target": name, "kind": builderV2KindTopology, "config": "Topology/" + name, "path": path,
		}

		if !maps.Equal(rows[name], want) {
			t.Errorf("row of %s = %v, want %v", name, rows[name], want)
		}
	}

	for name, document := range map[string]*bapi.PublishedDocument{"stored": stored, "both": both} {
		row := rows[name]

		if row["source"] != "store" || row["id"] != document.ID || row["digest"] != document.Digest ||
			row["createdBy"] != builderV2TestOwner {
			t.Errorf("row of %s = %v, want the stored document %s", name, row, document.ID)
		}

		if _, present := row["path"]; present {
			t.Errorf("row of %s = %v, want no path for a stored document", name, row)
		}
	}
}

// openedBuilderFile is the Builder file of a topology as the editor opens it.
type openedBuilderFileDocument struct {
	token    string
	digest   string
	document *bdoc.Document
	differs  bool
}

// openBuilderFile reads the document of a topology that names a Builder
// file through GET /builder-v2/topologies/{topology}/document.
func openBuilderFile(t *testing.T, harness *builderV2Harness, topology string) openedBuilderFileDocument {
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

// newFileBackedTopology stores the topology name as a new diagram with the
// device "aa" publishes it, then keeps that diagram's document only as a
// Builder file the topology names: the topology is what the file publishes,
// and no document of it is stored. It returns the file's path.
func newFileBackedTopology(t *testing.T, harness *builderV2Harness, name string, pinned bool) string {
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
	harness *builderV2Harness,
	document *bdoc.Document,
	token string,
) (*httptest.ResponseRecorder, builderDraftResponse) {
	t.Helper()

	data, err := bapi.EncodeDocument(document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder := harness.do(builderV2Request{
		method: http.MethodPost, path: "/builder-v2/drafts", user: builderV2TestOwner,
		body: string(mustBuilderJSON(t, map[string]any{"document": json.RawMessage(data), "sourceToken": token})),
	})

	var draft builderDraftResponse

	if recorder.Code == http.StatusCreated {
		harness.decode(recorder, &draft)
	}

	return recorder, draft
}

// TestBuilderV2CreateDraftFromBuilderFile creates drafts whose source token
// names the Builder file of a topology. The file is read as the document
// route reads it: the draft is made only for a caller who may read the
// topology's document, while the file still holds what the token names.
func TestBuilderV2CreateDraftFromBuilderFile(t *testing.T) { //nolint:paralleltest // mutates package options
	harness := newBuilderV2Harness(t)
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

	stored := builderV2Publish(t, harness, "stored")
	setBuilderTopologyReference(t, harness, "stored", bapi.DocumentReference{Digest: stored.Digest, ID: stored.ID, Path: path})

	harness.addFileTopology("gone", bapi.DocumentReference{Path: filepath.Join(harness.files, "gone.yaml")})
	harness.addFileTopology("invalid", bapi.DocumentReference{
		Path: harness.writeBuilderFile([]byte("name: "+builderFileMarker+"\n"), "invalid.yaml"),
	})

	foreign := builderDocumentDigest(t, builderV2Document(t, "another"))
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
		said := builderFileRefusal(t, harness, recorder)

		if recorder.Code != test.status || said.Message != test.said || said.Cause != "" {
			t.Errorf("%s: status = %d, said %+v; want %d and %q", name, recorder.Code, said, test.status, test.said)
		}
	}

	// A caller who may create configs but not get this topology learns
	// nothing of its file, and the file is not read.
	role := builderV2Role(builderV2Policy([]string{"configs"}, []string{"Topology/other"}, []string{"list", "get", "create", "update"}))
	harness.fileReads = 0

	data, err := bapi.EncodeDocument(opened.document)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	recorder = harness.do(builderV2Request{
		method: http.MethodPost, path: "/builder-v2/drafts", user: builderV2TestPeer, role: &role,
		body: string(mustBuilderJSON(t, map[string]any{"document": json.RawMessage(data), "sourceToken": opened.token})),
	})

	if said := builderFileRefusal(t, harness, recorder); recorder.Code != http.StatusNotFound ||
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
	if said := builderFileRefusal(t, harness, recorder); recorder.Code != http.StatusConflict ||
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

// TestBuilderV2PublishToFileBackedTopology updates a topology that is read
// from the Builder file it names. A draft opened from the file updates it
// while the file still holds what the draft was opened from and the topology
// is still what the file publishes. The topology then names the stored
// document and still names the file, which is never written, and the result
// warns that the file is now behind.
func TestBuilderV2PublishToFileBackedTopology(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	for _, pinned := range []bool{false, true} {
		harness := newBuilderV2Harness(t)
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

		// The draft as it was opened holds the file's content: the topology
		// is written, never taken for already published.
		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		draft = response.Draft

		warning := "Topology site names the Builder file " + path +
			", which Publish does not change. Export the diagram and replace the file to keep it in step."

		if response.Stages[1].Status != "updated" || !slices.Contains(response.Warnings, warning) {
			t.Fatalf("pinned %t: stages = %#v, warnings = %q; want the topology updated with the warning",
				pinned, response.Stages, response.Warnings)
		}

		want := bapi.DocumentReference{Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: path}
		if got := builderTopologyReference(t, harness, "site"); got != want || draft.Digest != opened.digest {
			t.Fatalf("pinned %t: reference = %+v, want %+v", pinned, got, want)
		}

		// The topology is read from the store from now on, and the draft that
		// published it goes on updating it.
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

// TestBuilderV2PublishFromEditedFileDraft updates a file-backed topology
// from a draft edited after it was opened from the file, which the draft's
// source token alone ties to the file. It is refused once the file holds
// something else, or is gone, and once the topology is no longer what the
// file publishes.
func TestBuilderV2PublishFromEditedFileDraft(t *testing.T) { //nolint:paralleltest // mutates feature options
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	// open returns a draft opened from the file of a new file-backed
	// topology and edited since, with its document and the file's path.
	open := func(pinned bool) (*builderV2Harness, builderDraftResponse, *bdoc.Document, string) {
		harness := newBuilderV2Harness(t)
		path := newFileBackedTopology(t, harness, "site", pinned)
		opened := openBuilderFile(t, harness, "site")

		recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("creating the draft: status = %d: %s", recorder.Code, recorder.Body)
		}

		return harness, editBuilderDraft(t, harness, draft, opened.document, "bb"), opened.document, path
	}

	refused := func(harness *builderV2Harness, draft builderDraftResponse, name, want string) {
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

	for _, pinned := range []bool{false, true} {
		harness, draft, _, _ := open(pinned)

		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		if response.Stages[1].Status != "updated" {
			t.Fatalf("pinned %t: stages = %#v, want the topology updated", pinned, response.Stages)
		}

		if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb"}) {
			t.Fatalf("pinned %t: topology nodes = %v, want aa and bb", pinned, got)
		}

		// A fork of the draft is tied to the file as the draft is.
		harness, draft, document, _ := open(pinned)

		var fork builderDraftResponse

		recorder := forkBuilderDraft(t, harness, builderV2TestOwner, nil, draft.Owner+"/"+draft.ID, document)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("pinned %t: forking the draft: status = %d: %s", pinned, recorder.Code, recorder.Body)
		}

		harness.decode(recorder, &fork)
		publishBuilderDraft(t, harness, fork, update, http.StatusOK)

		// The file holds another document now.
		harness, draft, _, path := open(pinned)

		another, err := bapi.EncodeDocument(bdoc.NewDocument("site"))
		if err != nil {
			t.Fatalf("EncodeDocument returned error: %v", err)
		}

		if err := os.WriteFile(path, another, 0o600); err != nil {
			t.Fatalf("writing the file: %v", err)
		}

		refused(harness, draft, "the file changed", changed)

		// The file is gone.
		harness, draft, _, path = open(pinned)

		if err := os.Remove(path); err != nil {
			t.Fatalf("removing the file: %v", err)
		}

		refused(harness, draft, "the file is gone", changed)

		// The topology was edited by hand.
		harness, draft, _, _ = open(pinned)

		for i := range harness.configs {
			if harness.configs[i].FullName() == "Topology/site" {
				edited := cloneBuilderConfig(&harness.configs[i])
				nodes, _ := edited.Spec["nodes"].([]any)
				edited.Spec["nodes"] = append(nodes, includeNode("by-hand"))
				harness.configs[i] = *edited
			}
		}

		if opened := openBuilderFile(t, harness, "site"); !opened.differs {
			t.Fatalf("pinned %t: the document route does not say the topology differs from its file", pinned)
		}

		refused(harness, draft, "the topology was edited", differs)
	}
}

// TestBuilderV2PublishPinnedFileIsNotApplied publishes, to a topology that
// was never published, a draft holding exactly the document its reference
// pins its Builder file to. The reference names that content, but no
// document of it is stored: the topology is not taken for already published,
// so one that is not what the file publishes is refused.
func TestBuilderV2PublishPinnedFileIsNotApplied(t *testing.T) { //nolint:paralleltest // mutates feature options
	harness := newBuilderV2Harness(t)
	path := newFileBackedTopology(t, harness, "pump", true)
	opened := openBuilderFile(t, harness, "pump")

	// The reference also holds the ID the digest gives, as a topology copied
	// from another server does.
	setBuilderTopologyReference(t, harness, "pump", bapi.DocumentReference{
		Digest: opened.digest, ID: bapi.PublishedDocumentID("pump", opened.digest), Path: path,
	})

	for i := range harness.configs {
		if harness.configs[i].FullName() == "Topology/pump" {
			edited := cloneBuilderConfig(&harness.configs[i])
			edited.Spec["nodes"] = []any{includeNode("by-hand")}
			harness.configs[i] = *edited
		}
	}

	recorder, draft := createBuilderFileDraft(t, harness, opened.document, opened.token)
	if recorder.Code != http.StatusCreated || draft.Digest != opened.digest {
		t.Fatalf("creating the draft: status = %d, digest %s: %s", recorder.Code, draft.Digest, recorder.Body)
	}

	writes := harness.configWrites

	for _, action := range []string{"update", "create"} {
		_, reason := publishBuilderDraft(t, harness, draft,
			`{"mode":"topology","topology":{"name":"pump","action":"`+action+`"}}`, http.StatusConflict)

		want := map[string]string{
			"update": "topology pump is not what its Builder file publishes, so this draft cannot update it",
			"create": "config pump already exists; choose update explicitly",
		}[action]

		if reason != want || harness.configWrites != writes {
			t.Fatalf("%s: refusal = %q after %d writes, want %q", action, reason, harness.configWrites-writes, want)
		}
	}

	if got := topologyHostnames(t, harness, "pump"); !slices.Equal(got, []string{"by-hand"}) {
		t.Fatalf("topology nodes = %v, want the topology left as it was", got)
	}
}
