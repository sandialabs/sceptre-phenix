package builder

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

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
		if got := tt.draft.PublishedUnchanged(); got != tt.want {
			t.Errorf("PublishedUnchanged() of a draft %s = %t, want %t", tt.name, got, tt.want)
		}
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
			removed, err := h.service.DeletePublishedDrafts(ctx, testActor, tt.live, dryRun)
			if err != nil {
				t.Errorf("DeletePublishedDrafts(%s, dry run %t) returned error: %s", tt.name, dryRun, fmtErr(err))
			} else if len(removed) != 0 {
				t.Errorf("DeletePublishedDrafts(%s, dry run %t) = %q, want the draft kept", tt.name, dryRun, removed)
			}
		}
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) after DeletePublishedDrafts returned error: %s", meta.ID, fmtErr(err))
	}
}
