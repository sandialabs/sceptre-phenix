package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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
// topology name, past the config hook. An empty reference removes it.
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

// builderTopologyDocumentTag returns the entity tag of the published
// document id while the stored topology references it (see
// [builderDocumentETag]).
func builderTopologyDocumentTag(t *testing.T, harness *builderHarness, topology, id string) string {
	t.Helper()

	config, err := harness.getConfig(builderKindTopology + "/" + topology)
	if err != nil {
		t.Fatalf("getting topology %s returned error: %v", topology, err)
	}

	tag, err := builderDocumentETag(&bapi.PublishedDocument{ID: id}, config)
	if err != nil {
		t.Fatalf("builderDocumentETag(%s) returned error: %v", id, err)
	}

	return tag
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

			want := []builderDocumentResponse{}

			switch {
			case test.listed:
				want = []builderDocumentResponse{newBuilderDocumentResponse(published)}
				want[0].ETag = builderTopologyDocumentTag(t, harness, "site", published.ID)
			case test.file:
				want = []builderDocumentResponse{newBuilderFileResponse("site", builderTestFile)}
			}

			if diff := cmp.Diff(want, listedBuilderDocuments(t, harness), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("GET /builder/documents mismatch (-want +got):\n%s", diff)
			}

			recorder := harness.do(builderRequest{
				method: http.MethodGet, path: "/builder/documents/" + published.ID, user: builderAlice,
			})

			if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[test.listed]; recorder.Code != want {
				t.Fatalf("GET /builder/documents/%s: status = %d, want %d: %s", published.ID, recorder.Code, want, recorder.Body)
			}

			if !test.listed {
				return
			}

			var response builderDocumentResponse
			harness.decode(recorder, &response)

			// The response holds the canonical JSON, compacted.
			var document bytes.Buffer
			if err := json.Compact(&document, builderDocument(t, "site")); err != nil {
				t.Fatalf("compacting the document: %v", err)
			}

			if response.Digest != published.Digest {
				t.Errorf("GET /builder/documents/%s: digest = %q, want %q", published.ID, response.Digest, published.Digest)
			}

			if diff := cmp.Diff(document.String(), string(response.Document)); diff != "" {
				t.Errorf("GET /builder/documents/%s: document mismatch (-want +got):\n%s", published.ID, diff)
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
		if got := response.Stages[1].Status; got != "updated" {
			t.Errorf("publishing %s to site: topology stage status = %q, want updated", hostname, got)
		}

		if !slices.Contains(response.Warnings, bdoc.NewIssue(
			bdoc.CodePublishFileUnchanged, "", builderFileNotWrittenWarning("site", builderTestFile),
		)) {
			t.Errorf("publishing %s to site: warnings = %+v, want one saying the file is not written", hostname, response.Warnings)
		}

		draft = response.Draft

		want := bapi.DocumentReference{
			Digest: draft.Digest, ID: bapi.PublishedDocumentID("site", draft.Digest), Path: builderTestFile,
		}

		if diff := cmp.Diff(want, builderTopologyReference(t, harness, "site")); diff != "" {
			t.Errorf("reference of site after publishing %s mismatch (-want +got):\n%s", hostname, diff)
		}

		documents := listedBuilderDocuments(t, harness)
		listed := make([]string, 0, len(documents))

		for _, document := range documents {
			listed = append(listed, document.ID)
		}

		if !slices.Equal(listed, []string{want.ID}) {
			t.Errorf("after publishing %s: GET /builder/documents lists %q, want %q", hostname, listed, want.ID)
		}
	}

	stored, err := harness.getConfig("Topology/site")
	if err != nil {
		t.Fatalf("getConfig(Topology/site) returned error: %v", err)
	}

	if got := stored.Metadata.Annotations["keep"]; got != "value" {
		t.Errorf("Topology/site annotation keep = %q, want %q", got, "value")
	}

	if got, want := topologyHostnames(t, harness, "site"), []string{"aa", "bb", "cc"}; !slices.Equal(got, want) {
		t.Errorf("Topology/site hostnames = %q, want %q", got, want)
	}

	created, _ := publishBuilderDraft(t, harness, draft,
		`{"mode":"topology","topology":{"name":"elsewhere","action":"create"}}`, http.StatusOK)

	want := bapi.DocumentReference{Digest: draft.Digest, ID: bapi.PublishedDocumentID("elsewhere", draft.Digest), Path: ""}
	if diff := cmp.Diff(want, builderTopologyReference(t, harness, "elsewhere")); diff != "" {
		t.Errorf("reference of a created topology mismatch (-want +got):\n%s", diff)
	}

	if slices.ContainsFunc(created.Warnings, func(warning bdoc.Issue) bool { return strings.Contains(warning.Message, "Builder file") }) {
		t.Errorf("creating elsewhere: warnings = %q, want none of a file for a topology that names none", created.Warnings)
	}

	other := generateBuilderDocument(t, harness, "Topology/broken")
	replaced, _ := publishBuilderDraft(t, harness, createBuilderPublishDraft(t, harness, other, "Topology/broken"),
		`{"mode":"topology","topology":{"name":"broken","action":"update"}}`, http.StatusOK)

	want = bapi.DocumentReference{
		Digest: replaced.Draft.Digest, ID: bapi.PublishedDocumentID("broken", replaced.Draft.Digest), Path: "",
	}
	if diff := cmp.Diff(want, builderTopologyReference(t, harness, "broken")); diff != "" {
		t.Errorf("reference replacing an undecodable one mismatch (-want +got):\n%s", diff)
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
				const want = "config lab already exists. Choose update explicitly"

				if _, reason := publishBuilderDraft(t, harness, draft, create, http.StatusConflict); reason != want {
					t.Errorf("creating lab again: refusal = %q, want %q", reason, want)
				}

				if got := harness.configWrites - writes; got != 0 {
					t.Errorf("creating lab again wrote %d configs, want none", got)
				}

				return
			}

			response, _ := publishBuilderDraft(t, harness, draft, create, http.StatusOK)
			draft = response.Draft

			if got := response.Stages[1].Status; got != bapi.PublishSkipped {
				t.Errorf("creating lab again: topology stage status = %q, want %q", got, bapi.PublishSkipped)
			}

			if got := harness.configWrites - writes; got != 0 {
				t.Errorf("creating lab again wrote %d configs, want none", got)
			}

			// An applied stage leaves the reference as it is.
			if diff := cmp.Diff(test.ref, builderTopologyReference(t, harness, "lab")); diff != "" {
				t.Errorf("reference of lab mismatch (-want +got):\n%s", diff)
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
		t.Errorf("updating copy: refusal = %q, want the copy refused", reason)
	}

	other := harness.createDraft(builderAlice, "other")
	if _, reason := publishBuilderDraft(t, harness, other, update, http.StatusConflict); reason !=
		"topology lab is not the source of this draft" {
		t.Errorf("updating lab from another draft: refusal = %q, want it refused", reason)
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

	if got, want := topologyHostnames(t, harness, "lab"), []string{"aa", "bb", "cc"}; !slices.Equal(got, want) {
		t.Errorf("Topology/lab hostnames = %q, want %q", got, want)
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
		t.Error("cleanup removed a document although a topology's reference could not be decoded")
	}

	harness.configs = harness.configs[:1]
	harness.reroute(withBuilderService(service))

	if !stored(kept) {
		t.Error("cleanup removed the document topology kept names, want it kept")
	}

	if stored(orphan) {
		t.Error("cleanup kept the orphaned document, want it removed")
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
		t.Fatalf("POST /configs: status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	type views struct {
		Get    map[string]any
		Listed []map[string]any
		Stored string
	}

	want := map[string]any{"keep": "v", "builder-doc": map[string]any{"digest": digest, "path": builderTestFile}}
	wantViews := views{
		Get: want, Listed: []map[string]any{want},
		Stored: encodeBuilderReference(t, bapi.DocumentReference{Digest: digest, Path: builderTestFile}),
	}

	// shownViews returns the annotations GET /configs/topology/site and
	// GET /configs show, and the annotation the store holds.
	shownViews := func() views {
		t.Helper()

		var got shown

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs/topology/site", user: builderAlice,
		}), &got)

		var listed struct {
			Configs []shown `json:"configs"`
		}

		harness.decode(harness.do(builderRequest{
			method: http.MethodGet, path: "/configs?kind=Topology", user: builderAlice,
		}), &listed)

		stored, err := config.Get("Topology/site")
		if err != nil {
			t.Fatalf("config.Get(Topology/site) returned error: %v", err)
		}

		result := views{Get: got.Metadata.Annotations, Listed: nil, Stored: stored.Metadata.Annotations[bapi.DocumentAnnotation]}
		for _, entry := range listed.Configs {
			result.Listed = append(result.Listed, entry.Metadata.Annotations)
		}

		return result
	}

	if diff := cmp.Diff(wantViews, shownViews()); diff != "" {
		t.Errorf("annotations after POST /configs mismatch (-want +got):\n%s", diff)
	}

	// The body GET returns is accepted back as it is.
	body := harness.do(builderRequest{method: http.MethodGet, path: "/configs/topology/site", user: builderAlice}).Body.String()

	recorder = harness.do(builderRequest{
		method: http.MethodPut, path: "/configs/topology/site", user: builderAlice, contentType: "application/json", body: body,
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("PUT /configs/topology/site: status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body)
	}

	if diff := cmp.Diff(wantViews, shownViews()); diff != "" {
		t.Errorf("annotations after PUT /configs mismatch (-want +got):\n%s", diff)
	}

	for name, test := range map[string]struct {
		reference, reason string
		// unparsed is set for a body the config routes cannot parse at all.
		unparsed bool
	}{
		"an unknown sub-key": {reference: `{"file": "/phenix/builder.yaml"}`, reason: `property "file" is unsupported`},
		"a number":           {reference: `42`, reason: "annotation builder-doc", unparsed: true},
	} {
		t.Run(name, func(t *testing.T) {
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

				switch {
				case test.unparsed && recorder.Code < http.StatusBadRequest:
					t.Errorf("%s %s: status = %d, want at least %d", request.method, request.path, recorder.Code, http.StatusBadRequest)
				case !test.unparsed && recorder.Code != http.StatusBadRequest:
					t.Errorf("%s %s: status = %d, want %d", request.method, request.path, recorder.Code, http.StatusBadRequest)
				}

				refused := builderRefusal(t, recorder)
				if said := refused.Message + " " + refused.Cause + " " + fmt.Sprint(
					refused.Metadata,
				); !strings.Contains(
					said,
					test.reason,
				) {
					t.Errorf("%s %s: refusal = %q, want it to say %q", request.method, request.path, said, test.reason)
				}
			}

			if _, err := config.Get("Topology/refused"); !errors.Is(err, store.ErrNotExist) {
				t.Errorf("config.Get(Topology/refused) error = %v, want %v", err, store.ErrNotExist)
			}
		})
	}

	if diff := cmp.Diff(wantViews, shownViews()); diff != "" {
		t.Errorf("annotations after the refusals mismatch (-want +got):\n%s", diff)
	}
}
