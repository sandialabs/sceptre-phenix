package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
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

	c, err := store.NewConfig("Topology/" + name)
	if err != nil {
		h.t.Fatalf("NewConfig returned error: %v", err)
	}

	reference, err := doc.Reference().EncodeReference()
	if err != nil {
		h.t.Fatalf("EncodeReference returned error: %v", err)
	}

	c.Metadata.Annotations = store.Annotations{DocumentAnnotation: reference}
	c.Spec = map[string]any{"nodes": []any{}}

	return store.Create(c) //nolint:wrapcheck // the store's own error
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
		if _, err := h.service.VerifyPublishedDocument(context.Background(), doc.Reference()); err != nil {
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

	reference, err := doc.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	for name, named := range map[string]bool{"topo": true, "copy": false} {
		c, err := store.NewConfig("Topology/" + name)
		if err != nil {
			t.Fatalf("NewConfig returned error: %v", err)
		}

		c.Metadata.Annotations = store.Annotations{DocumentAnnotation: reference, "kept": "yes"}
		c.Spec = map[string]any{"nodes": []any{}}

		stored, err := config.Create(config.CreateFromConfig(c), config.CreateWithValidation())
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
				_, err := h.service.VerifyPublishedDocument(ctx, docs[i].Reference())
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
