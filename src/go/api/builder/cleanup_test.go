package builder

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
)

// cleanupFaultStore is a record store whose listings and reads fail when the
// hook set for them returns an error. failTxGet is failGet for the reads of
// a transaction.
type cleanupFaultStore struct {
	*memrecord.Store

	failList  func(namespace string) error
	failKeys  func(namespace string) error
	failGet   func(namespace, key string) error
	failTxGet func(namespace, key string) error
}

// cleanupFaultTx is the transaction of a cleanupFaultStore.
type cleanupFaultTx struct {
	store.RecordTx

	failGet func(namespace, key string) error
}

func (tx *cleanupFaultTx) GetRecord(namespace, key string) (store.Record, error) {
	if tx.failGet != nil {
		if err := tx.failGet(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	return tx.RecordTx.GetRecord(namespace, key) //nolint:wrapcheck // the wrapped transaction's answer
}

func (s *cleanupFaultStore) RecordTxn(fn func(store.RecordTx) error) error {
	return s.Store.RecordTxn(func(tx store.RecordTx) error { //nolint:wrapcheck // the wrapped store's answer
		return fn(&cleanupFaultTx{RecordTx: tx, failGet: s.failTxGet})
	})
}

func (s *cleanupFaultStore) ListRecords(namespace, prefix string) (store.Records, error) {
	if s.failList != nil {
		if err := s.failList(namespace); err != nil {
			return nil, err
		}
	}

	return s.Store.ListRecords(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

func (s *cleanupFaultStore) ListRecordKeys(namespace, prefix string) ([]string, error) {
	if s.failKeys != nil {
		if err := s.failKeys(namespace); err != nil {
			return nil, err
		}
	}

	return s.Store.ListRecordKeys(namespace, prefix) //nolint:wrapcheck // the wrapped store's answer
}

func (s *cleanupFaultStore) GetRecord(namespace, key string) (store.Record, error) {
	if s.failGet != nil {
		if err := s.failGet(namespace, key); err != nil {
			return store.Record{}, err
		}
	}

	return s.Store.GetRecord(namespace, key) //nolint:wrapcheck // the wrapped store's answer
}

// newCleanupFaultService returns a service of the harness's store, clock and
// IDs, which reads the store through a cleanupFaultStore with no hooks set.
func newCleanupFaultService(t *testing.T, h *testHarness) (*Service, *cleanupFaultStore) {
	t.Helper()

	faults := &cleanupFaultStore{Store: h.store, failList: nil, failKeys: nil, failGet: nil, failTxGet: nil}

	service, err := New(WithStore(faults), WithClock(h.service.clock), WithIDSource(h.service.newID))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	return service, faults
}

// publishTestDraft records a publication of the current snapshot of a draft.
func publishTestDraft(t *testing.T, h *testHarness, meta *DraftMetadata) *DraftMetadata {
	t.Helper()

	published, err := h.service.MarkPublished(context.Background(), markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	return published
}

// testLive returns the document that each publication of publishTestDraft
// stores, as its topology names it.
func testLive() []LiveDocument {
	return []LiveDocument{{Target: "topo", ID: "doc-1"}}
}

func TestPublishedUnchanged(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	unpublished := createTestDraft(t, h, "unpublished")
	clean := publishTestDraft(t, h, createTestDraft(t, h, "clean"))
	edited := appendTestSnapshot(t, h, publishTestDraft(t, h, createTestDraft(t, h, "edited")), "edited-v2", testActor)

	undone, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: edited.ID, Actor: testActor, ExpectedRevision: edited.Revision, Index: 0,
	})
	if err != nil {
		t.Fatalf("MoveCursor(%s, 0) returned error: %v", edited.ID, err)
	}

	for _, tt := range []struct {
		name  string
		draft *DraftMetadata
		want  bool
	}{
		{name: "never published", draft: unpublished, want: false},
		{name: "published, unchanged", draft: clean, want: true},
		{name: "edited after the publication", draft: edited, want: false},
		{name: "published snapshot with a newer one after it", draft: undone, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.draft.PublishedUnchanged(); got != tt.want {
				t.Errorf("PublishedUnchanged() of a draft %s = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

func TestDeletePublishedDrafts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	kept := createTestDraft(t, h, "kept")
	first := publishTestDraft(t, h, createTestDraft(t, h, "first"))
	second := publishTestDraft(t, h, appendTestSnapshot(t, h, createTestDraft(t, h, "second"), "second-v2", testActor))

	drafts, content := h.store.Keys(NamespaceDrafts), h.store.Keys(NamespaceContent)

	counted, err := h.service.DeletePublishedDrafts(ctx, testPeer, testLive(), true)
	if err != nil {
		t.Fatalf("DeletePublishedDrafts(dry run) returned error: %s", fmtErr(err))
	}

	want := []string{first.ID, second.ID}
	if diff := cmp.Diff(want, counted); diff != "" {
		t.Errorf("DeletePublishedDrafts(dry run) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(drafts, h.store.Keys(NamespaceDrafts)); diff != "" {
		t.Errorf("drafts after a dry run mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(content, h.store.Keys(NamespaceContent)); diff != "" {
		t.Errorf("content after a dry run mismatch (-want +got):\n%s", diff)
	}

	removed, err := h.service.DeletePublishedDrafts(ctx, testPeer, testLive(), false)
	if err != nil {
		t.Fatalf("DeletePublishedDrafts returned error: %s", fmtErr(err))
	}

	if diff := cmp.Diff(want, removed); diff != "" {
		t.Errorf("DeletePublishedDrafts mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{kept.ID}, h.store.Keys(NamespaceDrafts)); diff != "" {
		t.Errorf("drafts after DeletePublishedDrafts mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(contentKeysOf(h, draftContentPrefix(kept.ID)), h.store.Keys(NamespaceContent)); diff != "" {
		t.Errorf("content after DeletePublishedDrafts mismatch (-want +got):\n%s", diff)
	}

	if _, err := h.service.DeletePublishedDrafts(ctx, "", testLive(), true); !errors.Is(err, ErrInvalid) {
		t.Errorf("DeletePublishedDrafts without an actor error = %s, want ErrInvalid", fmtErr(err))
	}
}

// TestDeletePublishedDraftsSkipsADraftChangedSinceTheListing changes a
// listed draft before its delete: the delete names the revision it listed,
// so the changed draft stays.
func TestDeletePublishedDraftsSkipsADraftChangedSinceTheListing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := publishTestDraft(t, h, createTestDraft(t, h, "topo"))

	var edited bool

	h.store.FailDelete = func(namespace, _ string) error {
		if namespace == NamespaceDrafts && !edited {
			edited = true
			appendTestSnapshot(t, h, meta, "topo-v2", testPeer)
		}

		return nil
	}

	removed, err := h.service.DeletePublishedDrafts(ctx, testActor, testLive(), false)

	switch {
	case !edited:
		t.Error("the draft was not changed during DeletePublishedDrafts")
	case err != nil:
		t.Errorf("DeletePublishedDrafts returned error: %s", fmtErr(err))
	case len(removed) != 0:
		t.Errorf("DeletePublishedDrafts = %q, want the changed draft kept", removed)
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) after DeletePublishedDrafts returned error: %s", meta.ID, fmtErr(err))
	}
}

// TestDeletePublishedDraftsReportsAFailedDelete keeps going past a draft
// it cannot delete, and reports it as a cleanup failure.
func TestDeletePublishedDraftsReportsAFailedDelete(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	refused := publishTestDraft(t, h, createTestDraft(t, h, "refused"))
	deleted := publishTestDraft(t, h, createTestDraft(t, h, "deleted"))
	failure := errors.New("the store refuses the delete")

	h.store.FailDelete = func(namespace, key string) error {
		if namespace == NamespaceDrafts && key == refused.ID {
			return failure
		}

		return nil
	}

	removed, err := h.service.DeletePublishedDrafts(ctx, testActor, testLive(), false)
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, failure) {
		t.Errorf("DeletePublishedDrafts error = %s, want ErrCleanup with the refused delete", fmtErr(err))
	}

	if diff := cmp.Diff([]string{deleted.ID}, removed); diff != "" {
		t.Errorf("DeletePublishedDrafts mismatch (-want +got):\n%s", diff)
	}
}

// TestDeletePublishedDraftsKeepsADraftWhosePublicationIsGone keeps a
// published, unchanged draft when its topology no longer names what it
// published: the topology was deleted, or names another document.
func TestDeletePublishedDraftsKeepsADraftWhosePublicationIsGone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := publishTestDraft(t, h, createTestDraft(t, h, "topo"))

	for _, tt := range []struct {
		name string
		live []LiveDocument
	}{
		{name: "no topology", live: nil},
		{name: "another document", live: []LiveDocument{{Target: "topo", ID: "doc-2"}}},
		{name: "another topology", live: []LiveDocument{{Target: "other", ID: "doc-1"}}},
	} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dry run %t", tt.name, dryRun), func(t *testing.T) {
				removed, err := h.service.DeletePublishedDrafts(ctx, testActor, tt.live, dryRun)
				if err != nil {
					t.Errorf("DeletePublishedDrafts(%s, dry run %t) returned error: %s", tt.name, dryRun, fmtErr(err))
				} else if len(removed) != 0 {
					t.Errorf("DeletePublishedDrafts(%s, dry run %t) = %q, want the draft kept", tt.name, dryRun, removed)
				}
			})
		}
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) after DeletePublishedDrafts returned error: %s", meta.ID, fmtErr(err))
	}
}

// TestCleanupOrphanedDocumentsDryRun counts the readable orphans past the
// grace period, removes nothing, and reports the record it cannot read.
func TestCleanupOrphanedDocumentsDryRun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	keep := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo", 1000))
	publishTestDocument(t, h, "gone", testRandomDocument(t, "gone", 1000))

	if _, err := h.store.CreateRecord(NamespacePublished, "damaged-document", []byte("not metadata")); err != nil {
		t.Fatalf("CreateRecord(damaged-document) returned error: %v", err)
	}

	live := []LiveDocument{{Target: "topo", ID: keep.Reference().StoredID("topo")}}

	if counted, err := h.service.CleanupOrphanedDocuments(ctx, live, true); counted != 0 {
		t.Errorf("CleanupOrphanedDocuments(dry run) of recent documents = %d, %s, want 0", counted, fmtErr(err))
	}

	h.passOrphanGracePeriod()

	published, content := h.store.Keys(NamespacePublished), h.store.Keys(NamespaceContent)

	counted, err := h.service.CleanupOrphanedDocuments(ctx, live, true)
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrCorrupt) {
		t.Errorf("CleanupOrphanedDocuments(dry run) error = %s, want ErrCleanup with the damaged record", fmtErr(err))
	}

	if counted != 1 {
		t.Errorf("CleanupOrphanedDocuments(dry run) = %d, want 1", counted)
	}

	if diff := cmp.Diff(published, h.store.Keys(NamespacePublished)); diff != "" {
		t.Errorf("published records after a dry run mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(content, h.store.Keys(NamespaceContent)); diff != "" {
		t.Errorf("content after a dry run mismatch (-want +got):\n%s", diff)
	}
}

// TestCleanupListingFailures fails each listing a cleanup reads before it
// removes anything: the cleanup returns that error and removes nothing.
func TestCleanupListingFailures(t *testing.T) {
	ctx := context.Background()
	injected := errors.New("injected listing failure")

	failOn := func(failing string) func(string) error {
		return func(namespace string) error {
			if namespace == failing {
				return injected
			}

			return nil
		}
	}

	for _, test := range []struct {
		name string
		// fail sets the hooks of the store, and run runs the cleanup.
		fail func(*cleanupFaultStore)
		run  func(*Service) (int, error)
	}{
		{
			name: "documents, the published listing",
			fail: func(s *cleanupFaultStore) { s.failList = failOn(NamespacePublished) },
			run:  func(s *Service) (int, error) { return s.CleanupOrphanedDocuments(ctx, nil, false) },
		},
		{
			name: "content, the draft listing",
			fail: func(s *cleanupFaultStore) { s.failList = failOn(NamespaceDrafts) },
			run:  func(s *Service) (int, error) { return s.CleanupOrphanedContent(ctx, false) },
		},
		{
			name: "content, the published listing",
			fail: func(s *cleanupFaultStore) { s.failList = failOn(NamespacePublished) },
			run:  func(s *Service) (int, error) { return s.CleanupOrphanedContent(ctx, false) },
		},
		{
			name: "content, the content keys",
			fail: func(s *cleanupFaultStore) { s.failKeys = failOn(NamespaceContent) },
			run:  func(s *Service) (int, error) { return s.CleanupOrphanedContent(ctx, false) },
		},
		{
			name: "published drafts, the draft listing",
			fail: func(s *cleanupFaultStore) { s.failList = failOn(NamespaceDrafts) },
			run: func(s *Service) (int, error) {
				removed, err := s.DeletePublishedDrafts(ctx, testActor, testLive(), false)

				return len(removed), err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			service, faults := newCleanupFaultService(t, h)

			publishTestDraft(t, h, createTestDraft(t, h, "topo"))
			publishTestDocument(t, h, "gone", testRandomDocument(t, "gone", 1000))
			storePayload(t, h, snapshotKey("vanished-draft", "vanished-snapshot"), []byte(randomText(9, 500)))
			h.passOrphanGracePeriod()

			drafts, published, content := h.store.Keys(NamespaceDrafts), h.store.Keys(NamespacePublished),
				h.store.Keys(NamespaceContent)

			test.fail(faults)

			removed, err := test.run(service)
			if !errors.Is(err, injected) {
				t.Errorf("cleanup error = %s, want the injected listing failure", fmtErr(err))
			}

			if removed != 0 {
				t.Errorf("cleanup removed %d, want 0", removed)
			}

			for namespace, want := range map[string][]string{
				NamespaceDrafts: drafts, NamespacePublished: published, NamespaceContent: content,
			} {
				if diff := cmp.Diff(want, h.store.Keys(namespace)); diff != "" {
					t.Errorf("records of %s after the failed cleanup mismatch (-want +got):\n%s", namespace, diff)
				}
			}
		})
	}
}

// TestCleanupOrphanedContentReportsEachKey removes the orphans it can, and
// reports each key it cannot read or remove. It skips a record that is gone
// or written again before its delete.
func TestCleanupOrphanedContentReportsEachKey(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	service, faults := newCleanupFaultService(t, h)

	key := func(snapshot string) string { return snapshotKey("vanished-draft", snapshot) }

	for i, snapshot := range []string{"removed", "unreadable", "vanished", "undeletable", "rewritten"} {
		storePayload(t, h, key(snapshot), []byte(randomText(int64(20+i), 500)))
	}

	const badKey = "drafts/vanished-draft"

	if _, err := h.store.CreateRecord(NamespaceContent, badKey, []byte("not content")); err != nil {
		t.Fatalf("CreateRecord(%s) returned error: %v", badKey, err)
	}

	h.passOrphanGracePeriod()

	readFailure, deleteFailure := errors.New("injected read failure"), errors.New("injected delete failure")

	faults.failGet = func(namespace, got string) error {
		switch {
		case namespace != NamespaceContent:
		case got == key("unreadable"):
			return readFailure
		case got == key("vanished"):
			h.store.Drop(namespace, got)
		}

		return nil
	}

	h.store.FailDelete = func(namespace, got string) error {
		switch {
		case namespace != NamespaceContent:
		case got == key("undeletable"):
			return deleteFailure
		case got == key("rewritten"):
			if _, err := h.store.UpdateRecord(namespace, got, []byte("new content"), store.AnyRevision); err != nil {
				t.Errorf("UpdateRecord(%s) returned error: %v", got, err)
			}
		}

		return nil
	}

	removed, err := service.CleanupOrphanedContent(ctx, false)
	if removed != 1 {
		t.Errorf("CleanupOrphanedContent = %d, want only %s removed", removed, key("removed"))
	}

	var cleanup *CleanupError

	switch {
	case !errors.As(err, &cleanup):
		t.Errorf("CleanupOrphanedContent error = %s, want a *CleanupError", fmtErr(err))
	case len(cleanup.Errors) != 3, !errors.Is(err, readFailure), !errors.Is(err, deleteFailure), !errors.Is(err, ErrCorrupt):
		t.Errorf("CleanupOrphanedContent error = %s, want the failed read, the failed delete and the bad key", fmtErr(err))
	}

	want := []string{badKey, key("rewritten"), key("undeletable"), key("unreadable")}
	if diff := cmp.Diff(want, h.store.Keys(NamespaceContent)); diff != "" {
		t.Errorf("content after CleanupOrphanedContent mismatch (-want +got):\n%s", diff)
	}
}
