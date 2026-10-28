package builder

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestConcurrentIdenticalAppendKeepsWinnerReadable lets a writer commit the same
// document first. The loser's transaction must store nothing.
func TestConcurrentIdenticalAppendKeepsWinnerReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	document := testDocument(t, "topo-v2", 0)

	var (
		winner    *DraftMetadata
		winnerErr error
		raced     bool
	)

	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts || raced {
			return nil
		}

		raced = true

		// The other writer commits the identical document first.
		winner, winnerErr = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
			DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
			Document: document, Summary: "winner",
		})

		return nil
	}

	loser, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: document, Summary: "loser",
	})

	if winnerErr != nil {
		t.Fatalf("winning AppendSnapshot returned error: %v", winnerErr)
	}

	if !raced {
		t.Fatal("the test did not interleave the two appends")
	}

	if !errors.Is(err, ErrConflict) {
		t.Errorf("losing AppendSnapshot error = %s, want ErrConflict", fmtErr(err))
	}

	if loser != nil {
		t.Errorf("losing AppendSnapshot = %+v, want no metadata", loser)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(draft) of the winner returned error: %v", err)
	}

	if diff := contentDiff(t, document, current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch with the winner (-want +got):\n%s", diff)
	}

	if got := documentProvenance(t, current.Data).UpdatedBy; got != testPeer {
		t.Errorf("GetCurrentDocument(draft) UpdatedBy = %q, want the winner %q", got, testPeer)
	}

	if want := winner.History[winner.Cursor].ID; current.Manifest.ID != want {
		t.Errorf("GetCurrentDocument(draft) snapshot = %q, want the winner's snapshot %q", current.Manifest.ID, want)
	}

	// The draft holds the content of the winner's snapshots and nothing of
	// the loser's.
	if keys := contentKeysOf(h, draftContentPrefix(meta.ID)); len(keys) != len(winner.History) {
		t.Errorf("draft holds %d content records, want %d: the losing attempt stored content", len(keys), len(winner.History))
	}
}

func TestConcurrentAppendsElectExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	document := testDocument(t, "topo-v2", 0)

	const writers = 4

	var (
		wg      sync.WaitGroup
		results = make([]*DraftMetadata, writers)
		errs    = make([]error, writers)
	)

	wg.Add(writers)

	for i := range writers {
		go func() {
			defer wg.Done()

			results[i], errs[i] = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: document, Summary: "concurrent",
			})
		}()
	}

	wg.Wait()

	var winners int

	for i := range writers {
		switch {
		case errs[i] == nil:
			winners++
		case errors.Is(errs[i], ErrConflict):
		default:
			t.Errorf("AppendSnapshot of writer %d error = %s, want nil or ErrConflict", i, fmtErr(errs[i]))
		}
	}

	if winners != 1 {
		t.Errorf("%d concurrent AppendSnapshot calls succeeded, want exactly 1", winners)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if len(stored.History) != 2 {
		t.Errorf("GetDraft(draft) history length = %d, want 2 with the single winning append", len(stored.History))
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(draft) of the winner returned error: %v", err)
	}

	if diff := contentDiff(t, document, current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch with the winner (-want +got):\n%s", diff)
	}

	if keys := contentKeysOf(h, draftContentPrefix(meta.ID)); len(keys) != len(stored.History) {
		t.Errorf("draft holds %d content records, want %d: losing attempts stored content", len(keys), len(stored.History))
	}
}

// TestConcurrentPublishKeepsWinnerReadable lets a winner store the same
// document while the loser's transaction runs. The loser stores nothing, and
// the winner's document stays whole.
func TestConcurrentPublishKeepsWinnerReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	req := PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: testRandomDocument(t, "topo", 3000), DraftID: "", SnapshotID: "",
	}

	var (
		winner    *PublishedDocument
		winnerErr error
		raced     bool
	)

	h.store.BeforeCreate = func(namespace, _ string) error {
		if namespace != NamespacePublished || raced {
			return nil
		}

		raced = true
		winner, winnerErr = h.service.PutPublishedDocument(ctx, req)

		return nil
	}

	_, err := h.service.PutPublishedDocument(ctx, req)

	if winnerErr != nil {
		t.Fatalf("winning PutPublishedDocument returned error: %v", winnerErr)
	}

	if !raced {
		t.Fatal("the test did not interleave the two publications")
	}

	if !errors.Is(err, ErrConflict) {
		t.Errorf("losing PutPublishedDocument error = %s, want ErrConflict", fmtErr(err))
	}

	if keys := contentKeysOf(h, publishedKey(winner.ID)); len(keys) != 1 {
		t.Errorf("published document content records = %q, want the winner's one", keys)
	}

	_, data, err := h.service.GetPublishedDocumentData(ctx, winner.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData(winner) after the loser failed returned error: %v", err)
	}

	if diff := cmp.Diff(string(req.Document), string(data)); diff != "" {
		t.Errorf("GetPublishedDocumentData(winner) mismatch (-want +got):\n%s", diff)
	}

	// The same publication again finds the winner's document.
	again, err := h.service.PutPublishedDocument(ctx, req)
	if err != nil {
		t.Fatalf("PutPublishedDocument(topo) again returned error: %v", err)
	}

	ignored := []cmp.Option{cmpopts.IgnoreUnexported(PublishedDocument{}), cmpopts.IgnoreFields(PublishedDocument{}, "Revision")}
	if diff := cmp.Diff(winner, again, ignored...); diff != "" {
		t.Errorf("PutPublishedDocument(topo) again mismatch with the winner (-want +got):\n%s", diff)
	}
}

// TestSaveWritesNothingWhenOneWriteFails fails one write of each save in turn,
// and checks that the save leaves the store as it was: no content without
// metadata, and no metadata without content.
func TestSaveWritesNothingWhenOneWriteFails(t *testing.T) {
	refused := errors.New("the store refuses the write")

	tests := []struct {
		name string
		// fail sets the hook that refuses one write of the save.
		fail func(h *testHarness)
		save func(h *testHarness, meta *DraftMetadata) error
	}{
		{
			name: "create draft, metadata refused",
			fail: func(h *testHarness) {
				h.store.BeforeCreate = func(namespace, _ string) error {
					if namespace == NamespaceDrafts {
						return refused
					}

					return nil
				}
			},
			save: func(h *testHarness, _ *DraftMetadata) error {
				_, err := h.service.CreateDraft(context.Background(), CreateDraftRequest{
					Actor: testActor, Title: "other", Document: testDocument(t, "other", 0),
				})

				return err
			},
		},
		{
			name: "append snapshot, metadata refused",
			fail: func(h *testHarness) {
				h.store.BeforeUpdate = func(string, string) error { return refused }
			},
			save: func(h *testHarness, meta *DraftMetadata) error {
				_, err := h.service.AppendSnapshot(context.Background(), AppendSnapshotRequest{
					DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
					Document: testDocument(t, "topo-v3", 0),
				})

				return err
			},
		},
		{
			name: "append snapshot over the redo branch, content delete refused",
			fail: func(h *testHarness) {
				h.store.FailDelete = func(namespace, _ string) error {
					if namespace == NamespaceContent {
						return refused
					}

					return nil
				}
			},
			save: func(h *testHarness, meta *DraftMetadata) error {
				_, err := h.service.AppendSnapshot(context.Background(), AppendSnapshotRequest{
					DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
					Document: testDocument(t, "topo-v3", 0),
				})

				return err
			},
		},
		{
			name: "delete snapshot, content delete refused",
			fail: func(h *testHarness) {
				h.store.FailDelete = func(string, string) error { return refused }
			},
			save: func(h *testHarness, meta *DraftMetadata) error {
				_, err := h.service.DeleteSnapshot(context.Background(), DeleteSnapshotRequest{
					DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
					SnapshotID: meta.History[1].ID,
				})

				return err
			},
		},
		{
			name: "delete draft, content delete refused",
			fail: func(h *testHarness) {
				h.store.FailPrefixDelete = func(string, string) error { return refused }
			},
			save: func(h *testHarness, meta *DraftMetadata) error {
				return h.service.DeleteDraft(context.Background(), meta.ID, testActor, meta.Revision)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			// Three snapshots with the cursor on the first: the next save
			// discards the other two.
			meta := createTestDraft(t, h, "topo")
			meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)
			meta = appendTestSnapshot(t, h, meta, "topo-v2b", testActor)

			meta, err := h.service.MoveCursor(ctx, MoveCursorRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 0,
			})
			if err != nil {
				t.Fatalf("MoveCursor returned error: %v", err)
			}

			drafts, content := h.store.Keys(NamespaceDrafts), h.store.Keys(NamespaceContent)

			tt.fail(h)

			if err := tt.save(h, meta); !errors.Is(err, refused) {
				t.Errorf("%s error = %s, want the refused write", tt.name, fmtErr(err))
			}

			if diff := cmp.Diff(drafts, h.store.Keys(NamespaceDrafts)); diff != "" {
				t.Errorf("drafts after %s mismatch (-want +got):\n%s", tt.name, diff)
			}

			if diff := cmp.Diff(content, h.store.Keys(NamespaceContent)); diff != "" {
				t.Errorf("content after %s mismatch (-want +got):\n%s", tt.name, diff)
			}

			stored, err := h.service.GetDraft(ctx, meta.ID)
			if err != nil {
				t.Fatalf("GetDraft after %s returned error: %v", tt.name, err)
			}

			if stored.Revision != meta.Revision {
				t.Errorf("GetDraft after %s revision = %d, want the %d it had", tt.name, stored.Revision, meta.Revision)
			}
		})
	}
}

// TestSaveIsOneTransaction checks that each save commits once, however many
// records it writes.
func TestSaveIsOneTransaction(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	commits := h.store.Commits()
	meta := createTestDraft(t, h, "topo")

	for range MaxSnapshots + 1 {
		meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)
	}

	if _, err := h.service.DeleteSnapshot(ctx, DeleteSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, SnapshotID: meta.History[0].ID,
	}); err != nil {
		t.Fatalf("DeleteSnapshot returned error: %v", err)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, stored.Revision); err != nil {
		t.Fatalf("DeleteDraft returned error: %v", err)
	}

	// One create, MaxSnapshots+1 appends (the last ones also prune), one
	// snapshot delete and one draft delete.
	if got, want := h.store.Commits()-commits, MaxSnapshots+4; got != want {
		t.Errorf("transactions of the saves = %d, want %d: one per save", got, want)
	}

	if keys := h.store.Keys(NamespaceContent); len(keys) != 0 {
		t.Errorf("content after DeleteDraft = %q, want none", keys)
	}
}
