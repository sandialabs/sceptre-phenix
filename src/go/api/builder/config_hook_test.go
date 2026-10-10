package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types"
)

// hookTestStore is a phenix store that keeps configs in a BoltDB and records
// in memory, so a test dates the documents it stores. beforeList, when set,
// runs once before the next record listing, as a writer racing it would.
type hookTestStore struct {
	*memrecord.Store
	hookTestConfigs

	beforeList func()
}

// hookTestConfigs gives hookTestStore its config methods. Embedded a level
// deeper than the record store, it never has its record methods promoted.
type hookTestConfigs struct{ store.Store }

func (s *hookTestStore) ListRecords(namespace, prefix string) (store.Records, error) {
	if run := s.beforeList; run != nil {
		s.beforeList = nil
		run()
	}

	return s.Store.ListRecords(namespace, prefix)
}

// hookTest stores documents and configs in a hookTestStore that is the phenix
// store for the test, which the Topology config hook reads.
type hookTest struct {
	t       *testing.T
	store   *hookTestStore
	service *Service
	// now is when documents are stored: the service's clock and the time
	// records are stamped with.
	now time.Time
}

func newHookTest(t *testing.T) *hookTest {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	h := &hookTest{t: t, now: time.Now()}
	h.store = &hookTestStore{Store: memrecord.New(), hookTestConfigs: hookTestConfigs{Store: db}}
	h.store.Stamp = func() time.Time { return h.now }

	service, err := New(WithStore(h.store), WithChunkSize(1024), WithClock(func() time.Time { return h.now }))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	h.service = service

	previous := store.DefaultStore
	store.DefaultStore = h.store //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	return h
}

// publish stores a published document of target holding content, dated
// ago before now.
func (h *hookTest) publish(target, content string, ago time.Duration) *PublishedDocument {
	h.t.Helper()

	return h.publishAt(target, content, time.Now().Add(-ago))
}

// publishAt stores a published document of target holding content, dated at.
func (h *hookTest) publishAt(target, content string, at time.Time) *PublishedDocument {
	h.t.Helper()

	h.now = at

	doc, err := h.service.PutPublishedDocument(context.Background(), PutPublishedDocumentRequest{
		Target: target, Kind: configKindTopology, Actor: testActor,
		Document: testRandomDocument(h.t, content, 600),
	})
	if err != nil {
		h.t.Fatalf("PutPublishedDocument(%q) returned error: %s", target, fmtErr(err))
	}

	return doc
}

// writeTopology stores the topology name referencing doc, as a publication
// does after storing doc. The store dates it now.
func (h *hookTest) writeTopology(name string, doc *PublishedDocument) error {
	h.t.Helper()

	return h.writeReference(name, doc.Reference())
}

// writeReference stores the topology name with the document reference ref,
// past the config hook. The store dates it now.
func (h *hookTest) writeReference(name string, ref DocumentReference) error {
	h.t.Helper()

	return store.Create(h.topology(name, encodeTestReference(h.t, ref))) //nolint:wrapcheck // the store's own error
}

// topology returns a topology config name whose document annotation holds
// value.
func (h *hookTest) topology(name, value string) *store.Config {
	h.t.Helper()

	c, err := store.NewConfig("Topology/" + name)
	if err != nil {
		h.t.Fatalf("NewConfig returned error: %v", err)
	}

	c.Metadata.Annotations = store.Annotations{DocumentAnnotation: value, "kept": "yes"}
	c.Spec = map[string]any{"nodes": []any{}}

	return c
}

// encodeTestReference returns ref as a topology's annotation holds it.
func encodeTestReference(t *testing.T, ref DocumentReference) string {
	t.Helper()

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	return encoded
}

// topologyUpdated returns the start of the second the topology name was last
// written in, as its metadata says.
func (h *hookTest) topologyUpdated(name string) time.Time {
	h.t.Helper()

	c, err := config.Get("topology/"+name, false)
	if err != nil {
		h.t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	updated := configUpdated(c)
	if updated.IsZero() {
		h.t.Fatalf("topology %s has no updated time: %q", name, c.Metadata.Updated)
	}

	return updated
}

// assertGone fails unless every doc and its content are gone.
func (h *hookTest) assertGone(docs ...*PublishedDocument) {
	h.t.Helper()

	for _, doc := range docs {
		if _, err := h.service.GetPublishedDocument(context.Background(), doc.ID); !errors.Is(err, ErrNotFound) {
			h.t.Errorf("document %s error = %s, want ErrNotFound", doc.ID, fmtErr(err))
		}

		if chunks := chunkKeysOf(&testHarness{store: h.store.Store}, publishedScope(doc.ID)); len(chunks) != 0 {
			h.t.Errorf("chunks of document %s = %v, want none", doc.ID, chunks)
		}
	}
}

// assertReadable fails unless every doc can be read back whole.
func (h *hookTest) assertReadable(docs ...*PublishedDocument) {
	h.t.Helper()

	for _, doc := range docs {
		if _, _, err := h.service.GetPublishedDocumentData(context.Background(), doc.ID); err != nil {
			h.t.Errorf("document %s: %s, want it readable", doc.ID, fmtErr(err))
		}
	}
}

// TestDeletingTopologyDeletesItsDocuments deletes a topology as `phenix
// config delete` does: its documents go, however recent, and nothing of
// another topology is touched.
func TestDeletingTopologyDeletesItsDocuments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	old := h.publish("topo", "topo-v1", 2*OrphanGracePeriod)
	superseded := h.publish("topo", "topo-v2", time.Minute)
	current := h.publish("topo", "topo-v3", time.Minute)
	other := h.publish("other", "other-v1", time.Minute)

	for name, doc := range map[string]*PublishedDocument{"topo": current, "other": other} {
		if err := h.writeTopology(name, doc); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}
	}

	if err := config.Delete("topology/topo"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	h.assertGone(old, superseded, current)
	h.assertReadable(other)

	if err := config.Delete("all"); err != nil {
		t.Fatalf("Delete all returned error: %v", err)
	}

	h.assertGone(other)
}

// TestDeletingTopologyKeepsPublicationInFlight deletes a topology while a
// publication of it stores its document, before the listing of documents to
// delete. That document is kept, whether it is new or the deleted topology's
// own published again, so the topology the publication then writes references
// a document that exists. One the publication never references goes once it
// is past the grace period. A new document stored in the second the topology
// was last written in is kept too: that time is only known to the second.
func TestDeletingTopologyKeepsPublicationInFlight(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	for _, test := range []struct {
		name    string
		content string
		// sameSecond stores the document at the end of the second the
		// topology was last written in, not a minute later.
		sameSecond bool
	}{
		{name: "new content", content: "topo-v2"},
		{name: "the deleted topology's content", content: "topo-v1"},
		{name: "new content in the second of the last write", content: "topo-v2", sameSecond: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHookTest(t)

			deleted := h.publish("topo", "topo-v1", time.Minute)
			if err := h.writeTopology("topo", deleted); err != nil {
				t.Fatalf("storing the topology returned error: %v", err)
			}

			stored := time.Now().Add(time.Minute)
			if test.sameSecond {
				stored = h.topologyUpdated("topo").Add(time.Second - time.Nanosecond)
			}

			var inFlight *PublishedDocument

			h.store.beforeList = func() {
				inFlight = h.publishAt("topo", test.content, stored)
			}

			if err := config.Delete("topology/topo"); err != nil {
				t.Fatalf("Delete returned error: %v", err)
			}

			if inFlight == nil {
				t.Fatal("the delete did not list the documents")
			}

			if inFlight.ID != deleted.ID {
				h.assertGone(deleted)
			}

			h.assertReadable(inFlight)

			if err := h.writeTopology("topo", inFlight); err != nil {
				t.Fatalf("the publication could not store the topology again: %v", err)
			}

			h.assertReadable(inFlight)

			// Had the publication failed, nothing would reference the document.
			if err := config.Delete("topology/topo"); err != nil {
				t.Fatalf("Delete returned error: %v", err)
			}

			if test.sameSecond {
				// Stored no later than the second the topology that named it
				// was written in, it went with that topology.
				h.assertGone(inFlight)

				return
			}

			h.now = time.Now().Add(2 * OrphanGracePeriod)

			if removed, err := h.service.CleanupOrphanedDocuments(t.Context(), nil); err != nil || removed != 1 {
				t.Fatalf("CleanupOrphanedDocuments = %d, %s; want the document removed", removed, fmtErr(err))
			}

			h.assertGone(inFlight)
		})
	}
}

// TestRenamingTopologyDeletesItsDocuments renames a published topology with
// an update that changes its name, as PUT /configs does, and as `phenix config
// edit` does. Its documents go as when it is deleted, and the renamed topology
// no longer names one of them, so nothing keeps them from the startup cleanup
// either. An update that keeps the name keeps the reference.
func TestRenamingTopologyDeletesItsDocuments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	// update stores the topology name as the topology as.
	update := func(t *testing.T, name, as string) {
		t.Helper()

		c, err := config.Get("topology/"+name, false)
		if err != nil {
			t.Fatalf("getting topology %s returned error: %v", name, err)
		}

		c.Metadata.Name = as

		if err := config.Update("topology/"+name, c); err != nil {
			t.Fatalf("updating topology %s as %s returned error: %v", name, as, err)
		}
	}

	// edit renames the topology with an editor that rewrites its name.
	edit := func(t *testing.T, name, as string) {
		t.Helper()

		script := "#!/bin/sh\nsed 's/name: " + name + "$/name: " + as + "/' \"$1\" > \"$1.edited\" && mv \"$1.edited\" \"$1\"\n"
		editor := filepath.Join(t.TempDir(), "editor")

		//nolint:gosec // the editor the test runs
		if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
			t.Fatalf("writing the editor: %v", err)
		}

		t.Setenv("EDITOR", editor)

		if _, err := config.Edit("topology/"+name, false); err != nil {
			t.Fatalf("editing topology %s as %s returned error: %v", name, as, err)
		}
	}

	for _, test := range []struct {
		name   string
		rename func(t *testing.T, name, as string)
	}{
		{name: "an update", rename: update},
		{name: "phenix config edit", rename: edit},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHookTest(t)

			old := h.publish("topo", "topo-v1", 2*OrphanGracePeriod)
			superseded := h.publish("topo", "topo-v2", time.Minute)
			current := h.publish("topo", "topo-v3", time.Minute)
			other := h.publish("other", "other-v1", time.Minute)

			for name, doc := range map[string]*PublishedDocument{"topo": current, "other": other} {
				if err := h.writeTopology(name, doc); err != nil {
					t.Fatalf("storing topology %s returned error: %v", name, err)
				}
			}

			update(t, "other", "other")

			if kept, err := config.Get("topology/other", false); err != nil || !kept.HasAnnotation(DocumentAnnotation) {
				t.Fatalf("an update that keeps the name: error = %v, topology %+v; want its document reference kept", err, kept)
			}

			test.rename(t, "topo", "renamed")

			if _, err := config.Get("topology/topo", false); !errors.Is(err, store.ErrNotExist) {
				t.Fatalf("topology under its old name: error = %v, want it gone", err)
			}

			renamed, err := config.Get("topology/renamed", false)
			if err != nil {
				t.Fatalf("getting the renamed topology returned error: %v", err)
			}

			if renamed.HasAnnotation(DocumentAnnotation) {
				t.Errorf("the renamed topology names a document: %q", renamed.Metadata.Annotations[DocumentAnnotation])
			}

			h.assertGone(old, superseded, current)
			h.assertReadable(other)
		})
	}
}

// TestCreatingTopologyDropsAnotherTopologysDocument stores a copy of a
// published topology under a new name: the copy names no document, and the
// published topology keeps its own.
func TestCreatingTopologyDropsAnotherTopologysDocument(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	doc := h.publish("topo", "topo-v1", time.Minute)
	reference := encodeTestReference(t, doc.Reference())

	for name, named := range map[string]bool{"topo": true, "copy": false} {
		stored, err := config.Create(config.CreateFromConfig(h.topology(name, reference)), config.CreateWithValidation())
		if err != nil {
			t.Fatalf("creating topology %s returned error: %v", name, err)
		}

		if stored.HasAnnotation(DocumentAnnotation) != named || !stored.HasAnnotation("kept") {
			t.Errorf("topology %s annotations = %v; want a document reference: %t, and the others kept",
				name, stored.Metadata.Annotations, named)
		}
	}

	h.assertReadable(doc)
}

// TestStoringTopologyChecksItsReference creates and updates topologies with
// each kind of document reference. A reference that is this topology's, or
// that cannot be told to be another's without reading the store, is stored
// as it is written canonically. The ID of a pair that belongs to another
// topology is dropped, and its digest too unless the reference names a file,
// whose content the digest pins. A reference that does not decode refuses
// the write, with or without config validation.
func TestStoringTopologyChecksItsReference(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	const file = "/phenix/topologies/site/builder.yaml"

	own := h.publish("topo", "topo-v1", time.Minute)
	other := h.publish("other", "other-v1", time.Minute)
	encode := func(ref DocumentReference) string { return encodeTestReference(t, ref) }

	for _, test := range []struct {
		name  string
		value string
		// want is the annotation stored, "" when it is removed.
		want string
		// refused is whether the write is refused.
		refused bool
	}{
		{
			name:  "digest only",
			value: encode(DocumentReference{Digest: own.Digest}),
			want:  encode(DocumentReference{Digest: own.Digest}),
		},
		{
			name:  "its own digest and id",
			value: encode(own.Reference()),
			want:  encode(own.Reference()),
		},
		{
			name:  "an id alone, which only the store can check",
			value: encode(DocumentReference{ID: other.ID}),
			want:  encode(DocumentReference{ID: other.ID}),
		},
		{
			name:  "path only",
			value: encode(DocumentReference{Path: file}),
			want:  encode(DocumentReference{Path: file}),
		},
		{
			name:  "its own digest and id with a path",
			value: encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
			want:  encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
		},
		{
			name:  "another topology's digest and id",
			value: encode(other.Reference()),
			want:  "",
		},
		{
			name:  "another topology's digest and id with a path",
			value: encode(DocumentReference{Digest: other.Digest, ID: other.ID, Path: file}),
			want:  encode(DocumentReference{Digest: other.Digest, Path: file}),
		},
		{
			name:  "its own digest with another id",
			value: encode(DocumentReference{Digest: own.Digest, ID: other.ID}),
			want:  "",
		},
		{
			name:  "written by hand",
			value: ` { "path" : "` + file + `", "id": "` + own.ID + `" ,"digest":"` + own.Digest + `" } `,
			want:  encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
		},
		{name: "not json", value: "{", refused: true},
		{name: "empty", value: "", refused: true},
		{name: "no sub-key", value: "{}", refused: true},
		{name: "an unknown sub-key", value: `{"id":"` + own.ID + `","draftId":"draft-1"}`, refused: true},
		{name: "an invalid id", value: `{"id":"../escape"}`, refused: true},
		{name: "an invalid digest", value: `{"digest":"deadbeef"}`, refused: true},
		{name: "a relative path", value: `{"path":"builder.yaml"}`, refused: true},
		{name: "a path that is not a document", value: `{"path":"/etc/phenix/config.txt"}`, refused: true},
		{
			name: "the reference an earlier Builder wrote",
			value: `{"id":"` + own.ID + `","digest":"` + own.Digest + `","size":600,"chunks":1,"chunkSize":1024,` +
				`"schema":"https://phenix.sceptre.dev/schemas/builder/v1","createdAt":"2026-09-29T10:00:00Z"}`,
			refused: true,
		},
	} {
		check := func(t *testing.T, stage string, stored *store.Config, err error) {
			t.Helper()

			if test.refused {
				if !errors.Is(err, types.ErrValidationFailed) {
					t.Fatalf("%s error = %v, want the write refused as invalid", stage, err)
				}

				if _, getErr := config.Get("topology/topo", false); stage == "create" && !errors.Is(getErr, store.ErrNotExist) {
					t.Fatalf("%s stored the topology: error = %v", stage, getErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("%s returned error: %v", stage, err)
			}

			got, named := stored.Metadata.Annotations[DocumentAnnotation]
			if got != test.want || named != (test.want != "") || !stored.HasAnnotation("kept") {
				t.Fatalf("%s stored annotations %v, want the document reference %q and the others kept",
					stage, stored.Metadata.Annotations, test.want)
			}
		}

		for _, validate := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/create validated %t", test.name, validate), func(t *testing.T) {
				options := []config.CreateOption{config.CreateFromConfig(h.topology("topo", test.value))}
				if validate {
					options = append(options, config.CreateWithValidation())
				}

				_, err := config.Create(options...)

				stored, getErr := config.Get("topology/topo", false)
				if err == nil && getErr != nil {
					t.Fatalf("getting the created topology returned error: %v", getErr)
				}

				check(t, "create", stored, err)

				if !validate && test.refused && !errors.Is(err, ErrInvalid) {
					t.Fatalf("create error = %v, want the hook's refusal", err)
				}

				_ = store.Delete(h.topology("topo", ""))
			})
		}

		t.Run(test.name+"/update", func(t *testing.T) {
			if err := h.writeReference("topo", own.Reference()); err != nil {
				t.Fatalf("storing the topology returned error: %v", err)
			}

			defer func() { _ = store.Delete(h.topology("topo", "")) }()

			err := config.Update("topology/topo", h.topology("topo", test.value))

			stored, getErr := config.Get("topology/topo", false)
			if getErr != nil {
				t.Fatalf("getting the updated topology returned error: %v", getErr)
			}

			check(t, "update", stored, err)

			if want := encode(own.Reference()); test.refused && stored.Metadata.Annotations[DocumentAnnotation] != want {
				t.Fatalf("a refused update stored the reference %q, want %q kept",
					stored.Metadata.Annotations[DocumentAnnotation], want)
			}
		})
	}

	h.assertReadable(own, other)
}

// TestRenamingTopologyKeepsItsFile renames topologies whose references name
// a Builder file. The path is not bound to the topology's name and stays;
// the ID, which is, goes, and the digest stays as the file's pin.
func TestRenamingTopologyKeepsItsFile(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	const file = "/phenix/topologies/site/builder.yaml"

	doc := h.publish("pinned", "pinned-v1", time.Minute)

	for name, test := range map[string]struct{ ref, want DocumentReference }{
		"floating": {
			ref:  DocumentReference{Path: file},
			want: DocumentReference{Path: file},
		},
		"pinned": {
			ref:  DocumentReference{Digest: doc.Digest, ID: doc.ID, Path: file},
			want: DocumentReference{Digest: doc.Digest, Path: file},
		},
	} {
		if err := h.writeReference(name, test.ref); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}

		c, err := config.Get("topology/"+name, false)
		if err != nil {
			t.Fatalf("getting topology %s returned error: %v", name, err)
		}

		c.Metadata.Name = name + "-renamed"

		if err := config.Update("topology/"+name, c); err != nil {
			t.Fatalf("renaming topology %s returned error: %v", name, err)
		}

		renamed, err := config.Get("topology/"+name+"-renamed", false)
		if err != nil {
			t.Fatalf("getting the renamed topology returned error: %v", err)
		}

		if got, want := renamed.Metadata.Annotations[DocumentAnnotation], encodeTestReference(t, test.want); got != want {
			t.Errorf("renamed topology %s has the reference %q, want %q", name, got, want)
		}
	}

	// The document was published to the old name, which no topology has now.
	h.assertGone(doc)
}

// TestDeletingTopologyNeverTouchesItsFile deletes topologies that name
// their documents by digest alone, and by a file as well: the stored
// documents go, those stored in the second the topology was written in
// included, which only the document the topology named tells apart, and the
// file is neither read nor removed.
func TestDeletingTopologyNeverTouchesItsFile(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	file := filepath.Join(t.TempDir(), "builder.yaml")
	content := []byte("not a builder document\n")

	//nolint:gosec // a file the test reads back
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatalf("writing the builder file: %v", err)
	}

	byDigest := h.publishAt("by-digest", "by-digest-v1", time.Now())
	if err := h.writeReference("by-digest", DocumentReference{Digest: byDigest.Digest}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	withFile := h.publishAt("with-file", "with-file-v1", time.Now())
	if err := h.writeReference("with-file", DocumentReference{Digest: withFile.Digest, Path: file}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	if err := h.writeReference("file-only", DocumentReference{Path: file}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	for _, name := range []string{"by-digest", "with-file", "file-only"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("deleting topology %s returned error: %v", name, err)
		}
	}

	h.assertGone(byDigest, withFile)

	if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("the builder file after the deletes = %q, %v; want it untouched", got, err)
	}
}

// TestLeaveTopologyDocuments asserts the hook leaves the documents of a
// topology to a caller that asked for them, for as long as it asked.
func TestLeaveTopologyDocuments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	docs := map[string]*PublishedDocument{}

	for _, name := range []string{"left", "other", "later"} {
		docs[name] = h.publish(name, name+"-v1", time.Minute)
		if err := h.writeTopology(name, docs[name]); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}
	}

	restore := LeaveTopologyDocuments("left")
	again := LeaveTopologyDocuments("left")

	again()
	again()

	for _, name := range []string{"left", "other"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
	}

	h.assertReadable(docs["left"])
	h.assertGone(docs["other"])

	restore()

	if err := h.writeTopology("left", docs["left"]); err != nil {
		t.Fatalf("storing the topology again returned error: %v", err)
	}

	for _, name := range []string{"left", "later"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
	}

	h.assertGone(docs["left"], docs["later"])
}

// TestDeletingTopologyWhenDocumentsCannotBeDeleted asserts a failure to
// remove the documents does not fail deleting the topology.
func TestDeletingTopologyWhenDocumentsCannotBeDeleted(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	h := newHookTest(t)

	doc := h.publish("topo", "topo-v1", time.Minute)
	if err := h.writeTopology("topo", doc); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	h.store.FailDelete = func(string, string) error { return errors.New("injected delete failure") }

	if err := config.Delete("topology/topo"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if _, err := config.Get("topology/topo", false); !errors.Is(err, store.ErrNotExist) {
		t.Fatalf("topology error = %v, want it deleted", err)
	}

	h.assertReadable(doc)
}

// TestDeleteConfigDocuments removes the documents of one config stored before
// it was last written or past the grace period, and nothing else.
func TestDeleteConfigDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	old := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v1", 1200))

	h.passOrphanGracePeriod()

	superseded := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 1200))
	current := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v3", 1200))
	written := memrecord.Time(h.now.Add(1))
	inFlight := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v4", 1200))
	other := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1200))

	experiment, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Experiment", Actor: testActor,
		Document: testRandomDocument(t, "experiment", 1200),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %s", fmtErr(err))
	}

	deleted := DeletedConfig{Kind: configKindTopology, Name: "topo", DocumentID: current.ID, Updated: written}

	removed, err := h.service.DeleteConfigDocuments(ctx, DeletedConfig{
		Kind: "topology", Name: "topo", DocumentID: current.ID, Updated: time.Time{},
	})
	if err != nil || removed != 1 {
		t.Fatalf("DeleteConfigDocuments without a written time = %d, %s; want the old document removed",
			removed, fmtErr(err))
	}

	removed, err = h.service.DeleteConfigDocuments(ctx, deleted)
	if err != nil || removed != 2 {
		t.Fatalf("DeleteConfigDocuments = %d, %s; want 2 removed", removed, fmtErr(err))
	}

	for _, doc := range []*PublishedDocument{old, superseded, current} {
		if _, err := h.service.GetPublishedDocument(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("document %s error = %s, want ErrNotFound", doc.ID, fmtErr(err))
		}
	}

	for _, doc := range []*PublishedDocument{inFlight, other, experiment} {
		if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
			t.Fatalf("document %s must survive: %s", doc.ID, fmtErr(err))
		}
	}

	h.passOrphanGracePeriod()

	if removed, err := h.service.DeleteConfigDocuments(ctx, deleted); err != nil || removed != 1 {
		t.Fatalf("DeleteConfigDocuments past the grace period = %d, %s; want 1 removed", removed, fmtErr(err))
	}

	for _, invalid := range []DeletedConfig{
		{Kind: "Topology", Name: "", DocumentID: "", Updated: written},
		{Kind: "Unknown", Name: "topo", DocumentID: "", Updated: written},
		{Kind: "Topology", Name: "topo", DocumentID: "not an id", Updated: written},
	} {
		if _, err := h.service.DeleteConfigDocuments(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("DeleteConfigDocuments(%+v) error = %s, want ErrInvalid", invalid, fmtErr(err))
		}
	}
}

// TestDeleteConfigDocumentsInTheSecondOfTheLastWrite tells the documents
// stored in the second a config was last written in apart: the one the config
// named and those stored no later than it were stored before that write, and
// one stored after it may belong to a publication in flight.
func TestDeleteConfigDocumentsInTheSecondOfTheLastWrite(t *testing.T) {
	const topo = "topo"

	type stored struct {
		content string
		// at is when the document is stored, from the start of the second
		// the config was last written in.
		at time.Duration
		// again, when not zero, is when the same content is published again.
		again time.Duration
		// named is the document the config named.
		named bool
		kept  bool
	}

	for _, test := range []struct {
		name string
		docs []stored
	}{
		{
			name: "the config's document and those before it go",
			docs: []stored{
				{content: "earlier", at: -time.Second},
				{content: "superseded", at: 100 * time.Millisecond},
				{content: "current", at: 200 * time.Millisecond, named: true},
				{content: "in flight", at: 300 * time.Millisecond, kept: true},
				{content: "in flight later", at: time.Minute, kept: true},
			},
		},
		{
			name: "the config's document stored the second before",
			docs: []stored{
				{content: "superseded", at: -300 * time.Millisecond},
				{content: "current", at: -200 * time.Millisecond, named: true},
				{content: "in flight", at: 0, kept: true},
			},
		},
		{
			name: "the config's document published again in a later second",
			docs: []stored{
				{content: "earlier", at: -time.Second},
				{content: "superseded", at: 100 * time.Millisecond, kept: true},
				{content: "current", at: 200 * time.Millisecond, again: time.Second, named: true, kept: true},
			},
		},
		{
			name: "a config that named no document",
			docs: []stored{
				{content: "earlier", at: -time.Nanosecond},
				{content: "in the second", at: 0, kept: true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			// Later than the service's clock gets, which dates a document too.
			updated := memrecord.Time(h.now.Load()).Add(OrphanGracePeriod / 2)
			deleted := DeletedConfig{Kind: configKindTopology, Name: topo, DocumentID: "", Updated: updated}
			docs := make([]*PublishedDocument, 0, len(test.docs))
			contents := make([][]byte, 0, len(test.docs))

			publish := func(content []byte, at time.Duration) *PublishedDocument {
				h.store.Stamp = func() time.Time { return updated.Add(at) }

				return publishTestDocument(t, h, topo, content)
			}

			for _, doc := range test.docs {
				contents = append(contents, testRandomDocument(t, doc.content, 600))
				docs = append(docs, publish(contents[len(contents)-1], doc.at))

				if doc.named {
					deleted.DocumentID = docs[len(docs)-1].ID
				}
			}

			for i, doc := range test.docs {
				if doc.again != 0 {
					docs[i] = publish(contents[i], doc.again)
				}
			}

			want := 0

			for _, doc := range test.docs {
				if !doc.kept {
					want++
				}
			}

			if removed, err := h.service.DeleteConfigDocuments(ctx, deleted); err != nil || removed != want {
				t.Fatalf("DeleteConfigDocuments = %d, %s; want %d removed", removed, fmtErr(err), want)
			}

			for i, doc := range test.docs {
				_, _, err := h.service.GetPublishedDocumentData(ctx, docs[i].ID)
				if doc.kept && err != nil {
					t.Errorf("document %q: %s, want it kept", doc.content, fmtErr(err))
				}

				if !doc.kept && !errors.Is(err, ErrNotFound) {
					t.Errorf("document %q: error = %s, want it removed", doc.content, fmtErr(err))
				}
			}
		})
	}
}

func TestConfigUpdated(t *testing.T) {
	t.Parallel()

	want := time.Date(2026, time.September, 30, 12, 0, 5, 0, time.UTC)

	for _, updated := range []string{"2026-09-30T12:00:05Z", "2026-09-30T14:00:05+02:00", "2026-09-30T12:00:05.25Z"} {
		got := configUpdated(&store.Config{Metadata: store.ConfigMetadata{Updated: updated}})
		if !got.Equal(want) {
			t.Errorf("configUpdated(%q) = %s, want %s", updated, got, want)
		}
	}

	if got := configUpdated(&store.Config{}); !got.IsZero() {
		t.Errorf("configUpdated without an updated time = %s, want zero", got)
	}
}
