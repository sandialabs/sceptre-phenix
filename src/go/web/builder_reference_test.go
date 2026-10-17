package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	bdoc "phenix/types/builder"
)

// builderTestFile is a Builder file path the references name, outside the
// directory the tests' server reads Builder files from.
const builderTestFile = "/phenix/topologies/site/phenix-configs/builder.yaml"

// builderTopologyReference returns the document reference the stored topology
// name holds.
func builderTopologyReference(t *testing.T, harness *builderHarness, name string) bapi.DocumentReference {
	t.Helper()

	topology, err := harness.getConfig(builderKindTopology + "/" + name)
	if err != nil {
		t.Fatalf("topology %s missing: %v", name, err)
	}

	reference, err := bapi.DecodeReference(topology.Metadata.Annotations[bapi.DocumentAnnotation])
	if err != nil {
		t.Fatalf("topology %s: DecodeReference returned error: %v", name, err)
	}

	return reference
}

// setBuilderTopologyReference stores reference as the document annotation of
// topology name, past the config hook; an empty reference removes it.
func setBuilderTopologyReference(t *testing.T, harness *builderHarness, name string, reference bapi.DocumentReference) {
	t.Helper()

	for i := range harness.configs {
		if harness.configs[i].FullName() != builderKindTopology+"/"+name {
			continue
		}

		annotations := store.Annotations{}
		maps.Copy(annotations, harness.configs[i].Metadata.Annotations)

		if reference == (bapi.DocumentReference{}) {
			delete(annotations, bapi.DocumentAnnotation)
		} else {
			annotations[bapi.DocumentAnnotation] = encodeBuilderReference(t, reference)
		}

		harness.configs[i].Metadata.Annotations = annotations

		return
	}

	t.Fatalf("topology %s missing", name)
}

// encodeBuilderReference returns ref as a topology's annotation holds it.
func encodeBuilderReference(t *testing.T, ref bapi.DocumentReference) string {
	t.Helper()

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	return encoded
}

// listedBuilderDocuments returns what GET /builder/documents lists.
func listedBuilderDocuments(t *testing.T, harness *builderHarness) []builderDocumentResponse {
	t.Helper()

	var response struct {
		Documents []builderDocumentResponse `json:"documents"`
	}

	harness.doDecode(builderRequest{method: http.MethodGet, path: "/builder/documents", user: builderAlice}, http.StatusOK, &response)

	return response.Documents
}

// TestBuilderDocumentsByReferenceForm lists and reads a published document
// through the forms of reference its topology may hold.
func TestBuilderDocumentsByReferenceForm(t *testing.T) {
	harness := newBuilderHarness(t,
		builderConfig(t, builderKindTopology, "site"), builderConfig(t, builderKindTopology, "copy"))

	published := builderPutDocument(t, harness, builderKindTopology, "site", "site")
	other := builderPutDocument(t, harness, builderKindTopology, "copy", "copy")

	for _, test := range []struct {
		name string
		// site and copy are the references the two topologies hold.
		site, copy bapi.DocumentReference
		listed     bool
		// file is set when topology site is listed with its file.
		file bool
	}{
		{name: "digest", site: bapi.DocumentReference{Digest: published.Digest}, listed: true},
		{name: "id", site: bapi.DocumentReference{ID: published.ID}, listed: true},
		{
			name: "digest, id and path", listed: true,
			site: bapi.DocumentReference{Digest: published.Digest, ID: published.ID, Path: builderTestFile},
		},
		{name: "path", site: bapi.DocumentReference{Path: builderTestFile}, file: true},
		{name: "another digest", site: bapi.DocumentReference{Digest: other.Digest, ID: published.ID}},
		{name: "only another topology names it", copy: bapi.DocumentReference{ID: published.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setBuilderTopologyReference(t, harness, "site", test.site)
			setBuilderTopologyReference(t, harness, "copy", test.copy)

			listed := listedBuilderDocuments(t, harness)

			if test.listed != (len(listed) == 1 && listed[0].ID == published.ID && listed[0].Target == "site" &&
				listed[0].Source == builderDocumentSourceStore && listed[0].Path == "") {
				t.Fatalf("listed documents = %+v, want the document of topology site listed: %t", listed, test.listed)
			}

			if test.file != (len(listed) == 1 && reflect.DeepEqual(listed[0], newBuilderFileResponse("site", builderTestFile))) {
				t.Fatalf("listed documents = %+v, want the file of topology site listed: %t", listed, test.file)
			}

			if !test.listed && !test.file && len(listed) != 0 {
				t.Fatalf("listed documents = %+v, want none", listed)
			}

			recorder := harness.do(builderRequest{
				method: http.MethodGet, path: "/builder/documents/" + published.ID, user: builderAlice,
			})

			if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[test.listed]; recorder.Code != want {
				t.Fatalf("getting the document: status = %d, want %d: %s", recorder.Code, want, recorder.Body.String())
			}

			if !test.listed {
				return
			}

			var response builderDocumentResponse
			harness.decode(recorder, &response)

			// The response holds the canonical JSON, compacted.
			var want bytes.Buffer
			if err := json.Compact(&want, builderDocument(t, "site")); err != nil {
				t.Fatalf("compacting the document: %v", err)
			}

			if response.Digest != published.Digest || !bytes.Equal(response.Document, want.Bytes()) {
				t.Fatalf("document = %+v, want the published content", response)
			}
		})
	}
}

// TestBuilderPublishKeepsReferencePath keeps the file of a topology's
// reference when it publishes to it, and names none for a new topology.
func TestBuilderPublishKeepsReferencePath(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"site","action":"update"}}`

	site := builderConfig(t, builderKindTopology, "site")
	site.Spec = map[string]any{"nodes": []any{includeNode("aa")}}
	site.Metadata.Annotations = store.Annotations{
		"keep":                  "value",
		bapi.DocumentAnnotation: encodeBuilderReference(t, bapi.DocumentReference{Path: builderTestFile}),
	}

	broken := builderConfig(t, builderKindTopology, "broken")
	broken.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: "{"}

	harness := newBuilderHarness(t, site, broken)

	document := generateBuilderDocument(t, harness, "Topology/site")
	draft := createBuilderPublishDraft(t, harness, document, "Topology/site")

	// The first publication updates the topology the draft was imported from,
	// and the second the topology that holds the draft's own document.
	for _, hostname := range []string{"bb", "cc"} {
		draft = editBuilderDraft(t, harness, draft, document, hostname)

		response, _ := publishBuilderDraft(t, harness, draft, update, http.StatusOK)
		if response.Stages[1].Status != "updated" {
			t.Fatalf("stages = %#v, want the topology updated", response.Stages)
		}

		if !slices.Contains(response.Warnings, bdoc.NewIssue(
			bdoc.CodePublishFileUnchanged, "", builderFileNotWrittenWarning("site", builderTestFile),
		)) {
			t.Fatalf("warnings = %+v, want one saying the file is not written", response.Warnings)
		}

		draft = response.Draft

		want := bapi.DocumentReference{
			Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: builderTestFile,
		}

		if got := builderTopologyReference(t, harness, "site"); got != want {
			t.Fatalf("reference after publishing %s = %+v, want %+v", hostname, got, want)
		}

		if listed := listedBuilderDocuments(t, harness); len(listed) != 1 || listed[0].ID != want.ID {
			t.Fatalf("listed documents = %+v, want the stored document %s", listed, want.ID)
		}
	}

	stored, err := harness.getConfig("Topology/site")
	if err != nil || stored.Metadata.Annotations["keep"] != "value" {
		t.Fatalf("topology = %+v, %v; want its other annotation kept", stored, err)
	}

	if got := topologyHostnames(t, harness, "site"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
		t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
	}

	created, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"elsewhere","action":"create"}}`, http.StatusOK)

	if got := builderTopologyReference(t, harness, "elsewhere"); got.Path != "" || got.Digest != draft.Digest {
		t.Fatalf("reference of a created topology = %+v, want the document and no file", got)
	}

	if slices.ContainsFunc(created.Warnings, func(warning bdoc.Issue) bool { return strings.Contains(warning.Message, "Builder file") }) {
		t.Fatalf("warnings = %q, want none of a file for a topology that names none", created.Warnings)
	}

	other := generateBuilderDocument(t, harness, "Topology/broken")
	publishBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, other, "Topology/broken"),
		`{"mode":"topology","topology":{"name":"broken","action":"update"}}`, http.StatusOK)

	if got := builderTopologyReference(t, harness, "broken"); got.Path != "" || got.ID == "" || got.Digest == "" {
		t.Fatalf("reference replacing an undecodable one = %+v, want the document and no file", got)
	}
}

// TestBuilderPublishAppliedByReferenceForm skips the topology stage when the
// topology already names the snapshot's document, by any form of reference.
func TestBuilderPublishAppliedByReferenceForm(t *testing.T) {
	const create = `{"mode":"topology","topology":{"name":"lab","action":"create"}}`

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
	draft = response.Draft

	own := builderTopologyReference(t, harness, "lab")
	foreign := "sha256:" + strings.Repeat("0", 64)

	for _, test := range []struct {
		name    string
		ref     bapi.DocumentReference
		applied bool
	}{
		{name: "digest and id", ref: own, applied: true},
		{name: "digest", ref: bapi.DocumentReference{Digest: own.Digest}, applied: true},
		{name: "id", ref: bapi.DocumentReference{ID: own.ID}, applied: true},
		{name: "digest, id and path", ref: bapi.DocumentReference{Digest: own.Digest, ID: own.ID, Path: builderTestFile}, applied: true},
		{name: "path", ref: bapi.DocumentReference{Path: builderTestFile}},
		{name: "its id with another digest", ref: bapi.DocumentReference{Digest: foreign, ID: own.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setBuilderTopologyReference(t, harness, "lab", test.ref)

			writes := harness.configWrites

			if !test.applied {
				_, reason := publishBuilderDraft(t, harness, draft, create, http.StatusConflict)
				if reason != "config lab already exists. Choose update explicitly" || harness.configWrites != writes {
					t.Fatalf("refusal = %q after %d writes, want the existing topology refused", reason, harness.configWrites-writes)
				}

				return
			}

			response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
			draft = response.Draft

			if response.Stages[1].Status != bapi.PublishSkipped || harness.configWrites != writes {
				t.Fatalf("stages = %#v after %d writes, want the topology stage skipped", response.Stages, harness.configWrites-writes)
			}

			// An applied stage leaves the reference as it is.
			if got := builderTopologyReference(t, harness, "lab"); got != test.ref {
				t.Fatalf("reference = %+v, want %+v left as it was", got, test.ref)
			}
		})
	}
}

// TestBuilderPublishUpdateNeedsTheDraftsDocument updates a topology only while
// it holds a document this draft published.
func TestBuilderPublishUpdateNeedsTheDraftsDocument(t *testing.T) {
	const update = `{"mode":"topology","topology":{"name":"lab","action":"update"}}`

	harness := newBuilderHarness(t)
	document := bdoc.NewDocument("lab")
	draft := editBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, document), document, "aa")

	response, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"lab","action":"create"}}`, http.StatusOK)
	draft = response.Draft

	stored, err := harness.getConfig("Topology/lab")
	if err != nil {
		t.Fatalf("published topology missing: %v", err)
	}

	first := cloneBuilderConfig(stored)

	// A copy that names the draft's document by its ID does not hold it.
	copied := cloneBuilderConfig(first)
	copied.Metadata.Name = "copy"
	harness.configs = append(harness.configs, *copied)
	setBuilderTopologyReference(t, harness, "copy", bapi.DocumentReference{ID: draft.Publication.DocumentID})

	draft = editBuilderDraft(t, harness, draft, document, "bb")
	if _, reason := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"copy","action":"update"}}`, http.StatusConflict); reason !=
		"topology copy changed after this draft published it" {
		t.Fatalf("refusal = %q, want the copy refused", reason)
	}

	other := harness.createDraft(builderAlice, "other")
	if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
		"topology lab is not the source this draft was loaded from" {
		t.Fatalf("refusal = %q, want another draft refused", reason)
	}

	// Put back as the first publication wrote it, lab holds a document whose
	// record names this draft.
	response, _ = publishBuilderDraft(t, harness, draft, update, http.StatusOK)

	for i := range harness.configs {
		if harness.configs[i].FullName() == "Topology/lab" {
			harness.configs[i] = *cloneBuilderConfig(first)
		}
	}

	publishBuilderDraft(t, harness, editBuilderDraft(t, harness, response.Draft, document, "cc"), update, http.StatusOK)

	if got := topologyHostnames(t, harness, "lab"); !slices.Equal(got, []string{"aa", "bb", "cc"}) {
		t.Fatalf("topology nodes = %v, want aa, bb and cc", got)
	}
}

// TestBuilderCleanupKeepsOnlyATopologysOwnDocument removes an orphaned
// document past the grace period, unless a reference cannot be decoded.
func TestBuilderCleanupKeepsOnlyATopologysOwnDocument(t *testing.T) {
	var (
		fake  = memrecord.New()
		clock = new(atomic.Int64)
		now   = func() time.Time { return memrecord.Time(clock.Load()) }
	)

	fake.Stamp = now

	service, err := bapi.New(bapi.WithStore(fake), bapi.WithClock(now))
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{withBuilderService(service)})
	harness.service = service

	kept := builderPutDocument(t, harness, builderKindTopology, "kept", "kept")
	orphan := builderPutDocument(t, harness, builderKindTopology, "gone", "gone")

	topology := builderConfig(t, builderKindTopology, "kept")
	topology.Metadata.Annotations = store.Annotations{
		bapi.DocumentAnnotation: encodeBuilderReference(t, bapi.DocumentReference{ID: kept.ID}),
	}
	broken := builderConfig(t, builderKindTopology, "broken")
	broken.Metadata.Annotations = store.Annotations{bapi.DocumentAnnotation: `{"id":"` + kept.ID + `","extra":"x"}`}
	harness.configs = store.Configs{topology, broken}

	stored := func(document *bapi.PublishedDocument) bool {
		t.Helper()

		_, _, err := service.GetPublishedDocumentData(context.Background(), document.ID)
		if err != nil && !errors.Is(err, bapi.ErrNotFound) {
			t.Fatalf("reading document %s: %v", document.ID, err)
		}

		return err == nil
	}

	clock.Add(int64(2 * bapi.OrphanGracePeriod / time.Second))

	// Registering the routes runs the cleanup.
	harness.reroute(withBuilderService(service))

	if !stored(orphan) {
		t.Fatal("a document was removed although a topology's reference could not be decoded")
	}

	harness.configs = harness.configs[:1]
	harness.reroute(withBuilderService(service))

	if !stored(kept) || stored(orphan) {
		t.Fatalf("kept stored: %t, orphan stored: %t; want only the document its topology names", stored(kept), stored(orphan))
	}
}

// TestConfigRoutesShowBuilderDocumentAsMap shows builder-doc as a map through
// /configs, stores it as a string, and refuses an invalid one.
func TestConfigRoutesShowBuilderDocumentAsMap(t *testing.T) {
	harness := newBuilderStoreHarness(t, nil)

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	topology := func(reference string) string {
		return `{"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology", "metadata": {"name": "site",` +
			` "annotations": {"keep": "v", "builder-doc": ` + reference + `}}, "spec": {"nodes": []}}`
	}

	type shown struct {
		Metadata struct {
			Name        string         `json:"name"`
			Annotations map[string]any `json:"annotations"`
		} `json:"metadata"`
	}

	recorder := harness.do(builderRequest{
		method: http.MethodPost, path: "/configs", user: builderAlice, contentType: "application/json",
		body: topology(`{"path": "` + builderTestFile + `", "digest": "` + digest + `"}`),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /configs: status = %d: %s", recorder.Code, recorder.Body)
	}

	want := map[string]any{"keep": "v", "builder-doc": map[string]any{"digest": digest, "path": builderTestFile}}

	check := func(stage string) {
		t.Helper()

		var got shown

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs/topology/site", user: builderAlice,
		}), &got)

		if !reflect.DeepEqual(got.Metadata.Annotations, want) {
			t.Fatalf("%s: GET /configs/topology/site shows annotations %#v, want %#v", stage, got.Metadata.Annotations, want)
		}

		var listed struct {
			Configs []shown `json:"configs"`
		}

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs?kind=Topology", user: builderAlice,
		}), &listed)

		if len(listed.Configs) != 1 || !reflect.DeepEqual(listed.Configs[0].Metadata.Annotations, want) {
			t.Fatalf("%s: GET /configs lists %#v, want the annotations %#v", stage, listed.Configs, want)
		}

		stored, err := config.Get("Topology/site", false)
		if err != nil {
			t.Fatalf("%s: getting the topology returned error: %v", stage, err)
		}

		reference := encodeBuilderReference(t, bapi.DocumentReference{Digest: digest, Path: builderTestFile})
		if got := stored.Metadata.Annotations[bapi.DocumentAnnotation]; got != reference {
			t.Fatalf("%s: the stored annotation = %q, want the string %q", stage, got, reference)
		}
	}

	check("create")

	// The body GET returns is accepted back as it is.
	body := harness.do(builderRequest{method: http.MethodGet, path: "/configs/topology/site", user: builderAlice}).Body.String()

	recorder = harness.do(builderRequest{
		method: http.MethodPut, path: "/configs/topology/site", user: builderAlice, contentType: "application/json", body: body,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs: status = %d: %s", recorder.Code, recorder.Body)
	}

	check("update")

	for name, test := range map[string]struct {
		reference, reason string
		// unparsed is set for a body the config routes cannot parse at all.
		unparsed bool
	}{
		"an unknown sub-key": {reference: `{"file": "/phenix/builder.yaml"}`, reason: `property "file" is unsupported`},
		"a number":           {reference: `42`, reason: "annotation builder-doc", unparsed: true},
	} {
		for _, request := range []builderRequest{
			{method: http.MethodPut, path: "/configs/topology/site"},
			{method: http.MethodPost, path: "/configs"},
		} {
			request.user, request.contentType = builderAlice, "application/json"
			request.body = strings.Replace(topology(test.reference), `"name": "site"`, `"name": "refused"`, 1)

			if request.method == http.MethodPut {
				request.body = topology(test.reference)
			}

			recorder := harness.do(request)
			refused := builderRefusal(t, recorder)

			said := refused.Message + " " + refused.Cause + " " + fmt.Sprint(refused.Metadata)
			if !strings.Contains(said, test.reason) || recorder.Code < http.StatusBadRequest ||
				(!test.unparsed && recorder.Code != http.StatusBadRequest) {
				t.Errorf("%s: %s %s: status = %d, said %q; want it refused with %q",
					name, request.method, request.path, recorder.Code, said, test.reason)
			}
		}

		if _, err := config.Get("Topology/refused", false); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s: the refused topology was stored: error = %v", name, err)
		}
	}

	check("the refusals")
}
