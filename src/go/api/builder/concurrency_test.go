package builder

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentIdenticalAppendKeepsWinnerReadable lets a writer commit the same
// document first; the loser's cleanup must not remove the winner's chunks.
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

	switch {
	case winnerErr != nil:
		t.Fatalf("the winning append returned error: %v", winnerErr)
	case !raced:
		t.Fatal("the test did not interleave the two appends")
	case !errors.Is(err, ErrConflict):
		t.Fatalf("the losing append error = %s, want ErrConflict", fmtErr(err))
	case loser != nil:
		t.Fatal("a losing append must not return metadata")
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("the winner's document must stay readable: %v", err)
	}

	if !sameContent(t, current.Data, document) || documentProvenance(t, current.Data).UpdatedBy != testPeer {
		t.Fatal("the winner's document content changed")
	}

	if current.Manifest.ID != winner.History[winner.Cursor].ID {
		t.Fatal("the current snapshot is not the winner's snapshot")
	}

	// The winner's chunks survived; the loser's are gone.
	if keys := chunkKeysOf(h, snapshotScope(meta.ID, current.Manifest.ID)); len(keys) == 0 {
		t.Fatal("the winner's chunks were removed by the loser's cleanup")
	}

	want := 0
	for i := range winner.History {
		want += len(winner.History[i].ChunkDigests)
	}

	if keys := chunkKeysOf(h, draftScope(meta.ID)); len(keys) != want {
		t.Fatalf("draft holds %d chunks, want %d: the losing attempt leaked chunks", len(keys), want)
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
			t.Fatalf("writer %d error = %s, want nil or ErrConflict", i, fmtErr(errs[i]))
		}
	}

	if winners != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", winners)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if len(stored.History) != 2 {
		t.Fatalf("history length = %d, want the single winning append", len(stored.History))
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("the winning document must be readable: %v", err)
	}

	if !sameContent(t, current.Data, document) {
		t.Fatal("the winning document content changed")
	}

	want := 0
	for i := range stored.History {
		want += len(stored.History[i].ChunkDigests)
	}

	if keys := chunkKeysOf(h, draftScope(meta.ID)); len(keys) != want {
		t.Fatalf("draft holds %d chunks, want %d: losing attempts leaked chunks", len(keys), want)
	}
}

// TestConcurrentPublishKeepsWinnerReadable lets a winner store the same document
// after the loser wrote its chunks; the loser's cleanup must not touch it.
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
		loserKeys []string
	)

	h.store.BeforeCreate = func(namespace, _ string) error {
		if namespace != NamespacePublished || raced {
			return nil
		}

		raced = true

		// At this point the loser has already checked for absence and written
		// its chunks; record them so the cleanup can be checked below.
		loserKeys = chunkKeysOf(h, publishedScope(PublishedDocumentID(req.Target, digestOf(req.Document))))

		winner, winnerErr = h.service.PutPublishedDocument(ctx, req)

		return nil
	}

	doc, err := h.service.PutPublishedDocument(ctx, req)

	switch {
	case winnerErr != nil:
		t.Fatalf("the winning publish returned error: %v", winnerErr)
	case !raced:
		t.Fatal("the test did not interleave the two publications")
	case err != nil:
		t.Fatalf("the losing publish returned error: %s", fmtErr(err))
	case doc.ID != winner.ID || doc.PayloadID != winner.PayloadID || doc.Revision != winner.Revision:
		t.Fatalf("the losing publish returned %+v, want the winner %+v", doc, winner)
	}

	if len(loserKeys) != len(winner.ChunkDigests) {
		t.Fatalf("the loser wrote %d chunks, want %d", len(loserKeys), len(winner.ChunkDigests))
	}

	// The loser's private scope is gone and the winner's is untouched.
	remaining := chunkKeysOf(h, publishedScope(winner.ID))
	if len(remaining) != len(winner.ChunkDigests) {
		t.Fatalf("document holds %d chunks, want the winner's %d", len(remaining), len(winner.ChunkDigests))
	}

	for _, key := range remaining {
		if !strings.HasPrefix(key, publishedPayloadScope(winner.ID, winner.PayloadID)) {
			t.Fatalf("chunk %q does not belong to the winner's payload scope", key)
		}
	}

	_, data, err := h.service.GetPublishedDocumentData(ctx, winner.ID)
	if err != nil {
		t.Fatalf("the winner's document must stay readable after the loser cleaned up: %v", err)
	}

	if !bytes.Equal(data, req.Document) {
		t.Fatal("the winner's published content changed")
	}
}

func TestWriteChunksCleansUpPartialFailures(t *testing.T) {
	h := newHarness(t)

	scope := snapshotScope("draft-1", "snap-1")

	load, err := buildPayload([]byte(randomText(11, 4000)), h.service.chunkSize)
	if err != nil {
		t.Fatalf("buildPayload returned error: %v", err)
	}

	if len(load.chunks) < 3 {
		t.Fatalf("chunks = %d, want a payload of at least 3 chunks", len(load.chunks))
	}

	failing := chunkKey(scope, 2)

	h.store.BeforeCreate = func(namespace, key string) error {
		if namespace == NamespaceChunks && key == failing {
			return errors.New("chunk store is unavailable")
		}

		return nil
	}

	created, err := h.service.writeChunks(scope, load)

	switch {
	case err == nil:
		t.Fatal("writeChunks must fail when the store rejects a chunk")
	case created != nil:
		t.Fatalf("writeChunks returned %v, want no keys after cleaning up", created)
	}

	if keys := chunkKeysOf(h, scope); len(keys) != 0 {
		t.Fatalf("chunks after a partial failure = %v, want none", keys)
	}

	// A cleanup failure on top of the write failure is reported, not swallowed.
	h.store.FailDelete = func(namespace, _ string) error {
		if namespace != NamespaceChunks {
			return nil
		}

		return errors.New("chunk store is unavailable for deletes too")
	}

	_, err = h.service.writeChunks(scope, load)
	if !errors.Is(err, ErrCleanup) {
		t.Fatalf("writeChunks error = %s, want it to also report the cleanup failure", fmtErr(err))
	}
}
