package builder

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDraftTitleIsRejectedNotTruncated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	longName := strings.Repeat("n", MaxTitleLength+1)

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, longName, 0), Summary: "", ID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDraft error = %s, want ErrInvalid for an oversized document name", fmtErr(err))
	}

	switch {
	case h.store.count(NamespaceDrafts) != 0:
		t.Fatal("a rejected title must not create a draft")
	case h.store.count(NamespaceChunks) != 0:
		t.Fatal("a rejected title must not write chunks")
	}

	// A draft whose title comes from the request is bounded the same way.
	_, err = h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: strings.Repeat("t", MaxTitleLength+1),
		SourceToken: "", Document: testDocument(t, "topo", 0), Summary: "", ID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDraft error = %s, want ErrInvalid for an oversized title", fmtErr(err))
	}
}

func TestRenameToAnOversizedNameIsRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	chunks := h.store.count(NamespaceChunks)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, strings.Repeat("n", MaxTitleLength+1), 0), Summary: "rename",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("AppendSnapshot error = %s, want ErrInvalid for an oversized rename", fmtErr(err))
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	switch {
	case stored.Revision != meta.Revision:
		t.Fatal("a rejected rename must not change the draft")
	case stored.Title != meta.Title:
		t.Fatalf("title = %q, want the unchanged %q", stored.Title, meta.Title)
	case len(stored.History) != 1:
		t.Fatalf("history length = %d, want the rejected snapshot dropped", len(stored.History))
	case h.store.count(NamespaceChunks) != chunks:
		t.Fatal("a rejected rename must not write chunks")
	}
}

func TestRenameWithinLimitsUpdatesTheTitle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	renamed, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "renamed-topology", 0), Summary: "rename",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot returned error: %v", err)
	}

	if renamed.Title != "renamed-topology" {
		t.Fatalf("title = %q, want the document's new name", renamed.Title)
	}
}
