package builder

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

func TestCreateDraftAndReadCurrentDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testDocument(t, "topo-a", 0)

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "Topo A",
		SourceToken: "Topology/topo-a", Document: data, Summary: "initial", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	switch {
	case meta.Revision <= 0:
		t.Fatalf("draft revision = %d, want > 0", meta.Revision)
	case len(meta.History) != 1:
		t.Fatalf("history length = %d, want 1", len(meta.History))
	case meta.Cursor != 0:
		t.Fatalf("cursor = %d, want 0", meta.Cursor)
	case meta.ETag() != `"`+strconv.FormatInt(meta.Revision, 10)+`"`:
		t.Fatalf("etag = %s, want revision based etag", meta.ETag())
	case !meta.Dirty():
		t.Fatal("a never published draft must be dirty")
	}

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if !bytes.Equal(snapshot.Data, data) {
		t.Fatal("current document bytes do not match the stored document")
	}

	doc, err := snapshot.Decode()
	if err != nil {
		t.Fatalf("Snapshot.Decode returned error: %v", err)
	}

	if doc.ID != builder.DocumentID("topo-a") {
		t.Fatalf("decoded document ID = %q, want %q", doc.ID, builder.DocumentID("topo-a"))
	}

	fetched, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if fetched.SourceToken != "Topology/topo-a" || fetched.Owner != testOwner || fetched.LastModifiedBy != testActor {
		t.Fatalf("draft metadata = %+v, want owner/actor/source recorded", fetched)
	}
}

func TestGetDraftNotFound(t *testing.T) {
	h := newHarness(t)

	_, err := h.service.GetDraft(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDraft error = %s, want ErrNotFound", fmtErr(err))
	}

	var notFound *NotFoundError
	if !errors.As(err, &notFound) || notFound.Kind != "draft" {
		t.Fatalf("GetDraft error = %s, want *NotFoundError for a draft", fmtErr(err))
	}
}

func TestListDraftsOwnerIsolation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mine := createTestDraft(t, h, "mine")

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testPeer, Actor: testPeer, Title: "Theirs",
		SourceToken: "", Document: testDocument(t, "theirs", 0), Summary: "", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft for peer returned error: %v", err)
	}

	all, err := h.service.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("ListDrafts returned error: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("ListDrafts = %d drafts, want 2", len(all))
	}

	owned, err := h.service.ListDraftsByOwner(ctx, testOwner)
	if err != nil {
		t.Fatalf("ListDraftsByOwner returned error: %v", err)
	}

	if len(owned) != 1 || owned[0].ID != mine.ID {
		t.Fatalf("ListDraftsByOwner = %+v, want only %s", owned, mine.ID)
	}

	if _, err := h.service.ListDraftsByOwner(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ListDraftsByOwner(\"\") error = %s, want ErrInvalid", fmtErr(err))
	}
}

func TestAppendSnapshotAdvancesHistoryAndRecordsActor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	updated := appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	switch {
	case len(updated.History) != 2:
		t.Fatalf("history length = %d, want 2", len(updated.History))
	case updated.Cursor != 1:
		t.Fatalf("cursor = %d, want 1", updated.Cursor)
	case updated.Revision <= meta.Revision:
		t.Fatalf("revision = %d, want > %d", updated.Revision, meta.Revision)
	case updated.LastModifiedBy != testPeer:
		t.Fatalf("lastModifiedBy = %q, want the cross-user actor %q", updated.LastModifiedBy, testPeer)
	case updated.Owner != testOwner:
		t.Fatalf("owner = %q, want it unchanged by a cross-user edit", updated.Owner)
	case updated.History[1].CreatedBy != testPeer:
		t.Fatalf("snapshot createdBy = %q, want %q", updated.History[1].CreatedBy, testPeer)
	case updated.History[0].CreatedBy != testActor:
		t.Fatalf("first snapshot createdBy = %q, want %q", updated.History[0].CreatedBy, testActor)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if !bytes.Equal(current.Data, testDocument(t, "topo-v2", 0)) {
		t.Fatal("current document is not the appended snapshot")
	}

	first, err := h.service.GetSnapshot(ctx, meta.ID, updated.History[0].ID)
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}

	if !bytes.Equal(first.Data, testDocument(t, "topo", 0)) {
		t.Fatal("older snapshot content changed")
	}

	if _, err := h.service.GetSnapshot(ctx, meta.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSnapshot for unknown snapshot = %s, want ErrNotFound", fmtErr(err))
	}
}

func TestAppendSnapshotConflictLeavesNoChunksBehind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	scope := draftScope(meta.ID)
	before := chunkKeysOf(h, scope)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 5,
		Document: testDocument(t, "topo-v2", 0), Summary: "",
	})

	var conflict *ConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrConflict) {
		t.Fatalf("AppendSnapshot error = %s, want *ConflictError", fmtErr(err))
	}

	if conflict.Expected != meta.Revision+5 || conflict.Actual != meta.Revision {
		t.Fatalf("conflict = %+v, want expected %d actual %d", conflict, meta.Revision+5, meta.Revision)
	}

	if got := chunkKeysOf(h, scope); len(got) != len(before) {
		t.Fatalf("chunks after conflict = %v, want unchanged %v", got, before)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if len(stored.History) != 1 || stored.Revision != meta.Revision {
		t.Fatalf("draft changed on a conflicting append: %+v", stored)
	}
}

func TestAppendSnapshotConflictDuringWriteCleansUpAttemptedChunks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	scope := draftScope(meta.ID)
	before := chunkKeysOf(h, scope)

	// The record changes underneath the caller between reading and writing.
	h.store.failUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return store.NewRecordConflictError(namespace, meta.ID, meta.Revision, meta.Revision+1)
	}

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "topo-v2", 0), Summary: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("AppendSnapshot error = %s, want ErrConflict", fmtErr(err))
	}

	if got := chunkKeysOf(h, scope); len(got) != len(before) {
		t.Fatalf("chunks after failed swap = %v, want the attempt's chunks removed (%v)", got, before)
	}
}

func TestAppendSnapshotReportsCleanupFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	h.store.failUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return store.NewRecordConflictError(namespace, meta.ID, meta.Revision, meta.Revision+1)
	}
	h.store.failDelete = func(namespace, _ string) error {
		if namespace != NamespaceChunks {
			return nil
		}

		return errors.New("chunk store is unavailable")
	}

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "topo-v2", 0), Summary: "",
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("AppendSnapshot error = %s, want it to report the conflict", fmtErr(err))
	}

	if !errors.Is(err, ErrCleanup) {
		t.Fatalf("AppendSnapshot error = %s, want it to also report the cleanup failure", fmtErr(err))
	}

	var cleanup *CleanupError
	if !errors.As(err, &cleanup) || len(cleanup.Errors) == 0 {
		t.Fatalf("AppendSnapshot error = %s, want a *CleanupError carrying its causes", fmtErr(err))
	}
}

func TestMoveCursorAndRedoBranchTruncation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v1")
	meta = appendTestSnapshot(t, h, meta, "v2", testActor)
	meta = appendTestSnapshot(t, h, meta, "v3", testActor)

	if !meta.CanUndo() || meta.CanRedo() {
		t.Fatalf("cursor state = %d of %d, want undo available and no redo", meta.Cursor, len(meta.History))
	}

	droppedID := meta.History[2].ID
	droppedChunks := len(meta.History[2].ChunkDigests)

	moved, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		Index: 1, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	switch {
	case moved.Cursor != 1:
		t.Fatalf("cursor = %d, want 1", moved.Cursor)
	case len(moved.History) != 3:
		t.Fatalf("history length = %d, want the redo branch retained", len(moved.History))
	case !moved.CanRedo():
		t.Fatal("redo must be available after moving the cursor back")
	case moved.LastModifiedBy != testPeer:
		t.Fatalf("lastModifiedBy = %q, want %q", moved.LastModifiedBy, testPeer)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if !bytes.Equal(current.Data, testDocument(t, "v2", 0)) {
		t.Fatal("cursor move did not change the current document")
	}

	// Appending from a moved cursor discards the redo branch and its chunks.
	branched := appendTestSnapshot(t, h, moved, "v4", testActor)

	if len(branched.History) != 3 || branched.Cursor != 2 {
		t.Fatalf("history after branching = %d entries, cursor %d, want 3 and 2", len(branched.History), branched.Cursor)
	}

	if branched.History[2].Summary != "v4" {
		t.Fatalf("last snapshot = %q, want the newly appended one", branched.History[2].Summary)
	}

	for i := range droppedChunks {
		key := chunkKey(snapshotScope(meta.ID, droppedID), i)
		if _, err := h.store.GetRecord(NamespaceChunks, key); err == nil {
			t.Fatalf("chunk %s of the discarded redo branch was not removed", key)
		}
	}
}

func TestMoveCursorValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v1")

	_, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Index: 7, SnapshotID: "", UseIndex: true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("MoveCursor out of range error = %s, want ErrInvalid", fmtErr(err))
	}

	_, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Index: 0, SnapshotID: "unknown", UseIndex: false,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MoveCursor to unknown snapshot error = %s, want ErrNotFound", fmtErr(err))
	}

	_, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 3,
		Index: 0, SnapshotID: "", UseIndex: true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("MoveCursor with stale revision error = %s, want ErrConflict", fmtErr(err))
	}
}

func TestHistoryPrunedToMaxSnapshots(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v0")

	for i := 1; i <= MaxSnapshots+5; i++ {
		meta = appendTestSnapshot(t, h, meta, "v"+strconv.Itoa(i), testActor)
	}

	if len(meta.History) != MaxSnapshots {
		t.Fatalf("history length = %d, want %d", len(meta.History), MaxSnapshots)
	}

	if meta.Cursor != MaxSnapshots-1 {
		t.Fatalf("cursor = %d, want %d", meta.Cursor, MaxSnapshots-1)
	}

	if meta.History[0].Summary != "v"+strconv.Itoa(MaxSnapshots+5-MaxSnapshots+1) {
		t.Fatalf("oldest retained snapshot = %q, want the oldest snapshots pruned", meta.History[0].Summary)
	}

	// Every retained snapshot must still be readable, and pruned content must be
	// gone.
	for i := range meta.History {
		if _, err := h.service.GetSnapshot(ctx, meta.ID, meta.History[i].ID); err != nil {
			t.Fatalf("GetSnapshot(%s) returned error: %v", meta.History[i].ID, err)
		}
	}

	chunks := chunkKeysOf(h, draftScope(meta.ID))

	var want int
	for i := range meta.History {
		want += len(meta.History[i].ChunkDigests)
	}

	if len(chunks) != want {
		t.Fatalf("retained chunks = %d, want %d (pruned snapshot chunks must be removed)", len(chunks), want)
	}
}

func TestPruneHistoryNeverEnforcesTheByteLimit(t *testing.T) {
	meta := &DraftMetadata{
		ID:      "draft",
		History: make([]SnapshotManifest, 0, 12),
	}

	const snapshotSize = 5 << 20

	for i := range 12 {
		meta.History = append(meta.History, SnapshotManifest{
			ID:   "snap-" + strconv.Itoa(i),
			Size: snapshotSize,
		})
	}

	meta.Cursor = len(meta.History) - 1

	dropped := pruneHistory(meta)

	// 12 x 5 MiB is past the 50 MiB byte limit, but pruning only ever enforces
	// the snapshot count: exceeding the byte limit rejects the edit instead.
	switch {
	case len(dropped) != 0:
		t.Fatalf("dropped %d snapshots, want none", len(dropped))
	case len(meta.History) != 12:
		t.Fatalf("history length = %d, want 12", len(meta.History))
	case meta.HistoryBytes() <= MaxDraftHistoryBytes:
		t.Fatalf("history bytes = %d, want a history past the limit to be left intact", meta.HistoryBytes())
	}
}

func TestPruneHistoryNeverDropsTheCursorSnapshot(t *testing.T) {
	meta := &DraftMetadata{
		ID:      "draft",
		History: make([]SnapshotManifest, 0, MaxSnapshots+10),
	}

	for i := range MaxSnapshots + 10 {
		meta.History = append(meta.History, SnapshotManifest{ID: "snap-" + strconv.Itoa(i), Size: 1})
	}

	meta.Cursor = 0

	dropped := pruneHistory(meta)

	if len(dropped) != 0 {
		t.Fatalf("dropped %d snapshots, want none when the cursor is at the oldest snapshot", len(dropped))
	}

	if meta.History[meta.Cursor].ID != "snap-0" {
		t.Fatalf("cursor snapshot = %s, want snap-0", meta.History[meta.Cursor].ID)
	}
}

func TestDocumentSizeLimitRejectedBeforeDurableWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	drafts := h.store.count(NamespaceDrafts)
	chunks := h.store.count(NamespaceChunks)

	oversized := bytes.Repeat([]byte("a"), MaxDocumentBytes+1)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: oversized, Summary: "",
	})

	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("AppendSnapshot error = %s, want *TooLargeError", fmtErr(err))
	}

	if tooLarge.Limit != MaxDocumentBytes {
		t.Fatalf("limit = %d, want %d", tooLarge.Limit, MaxDocumentBytes)
	}

	if h.store.count(NamespaceChunks) != chunks || h.store.count(NamespaceDrafts) != drafts {
		t.Fatal("an oversized document must be rejected before anything is written")
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if stored.Revision != meta.Revision || len(stored.History) != 1 {
		t.Fatal("an oversized document must not modify draft metadata")
	}

	if _, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "big",
		SourceToken: "", Document: oversized, Summary: "", ID: "",
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("CreateDraft with oversized document error = %s, want ErrTooLarge", fmtErr(err))
	}
}

func TestEncodeDocumentEnforcesLimit(t *testing.T) {
	doc := builder.NewDocument("huge")

	doc.Nodes = append(doc.Nodes, builder.Node{
		ID:       builder.NoteNodeID("huge"),
		Kind:     builder.NodeKindNote,
		Position: builder.Position{X: 0, Y: 0},
		Note:     &builder.Note{Text: strings.Repeat("y", MaxDocumentBytes+1), Color: ""},
	})

	if _, err := EncodeDocument(doc); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("EncodeDocument error = %s, want ErrTooLarge", fmtErr(err))
	}

	if _, err := EncodeDocument(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("EncodeDocument(nil) error = %s, want ErrInvalid", fmtErr(err))
	}
}

func TestMarkPublishedClearsDirtyAndRequiresCurrentSnapshot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)

	stale := meta.History[0].ID

	_, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: stale, Mode: PublishModeTopology, TopologyTarget: "topo",
		TopologyAction: TopologyActionCreate, ExperimentTarget: "", ScenarioTarget: "", DocumentID: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("MarkPublished with a non-current snapshot error = %s, want ErrConflict", fmtErr(err))
	}

	_, err = h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 9,
		SnapshotID: meta.History[1].ID, Mode: PublishModeTopology, TopologyTarget: "topo",
		TopologyAction: TopologyActionCreate, ExperimentTarget: "", ScenarioTarget: "", DocumentID: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("MarkPublished with a stale revision error = %s, want ErrConflict", fmtErr(err))
	}

	_, err = h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[1].ID, Mode: "sideways", TopologyTarget: "topo",
		TopologyAction: TopologyActionCreate, ExperimentTarget: "", ScenarioTarget: "", DocumentID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("MarkPublished with an unknown mode error = %s, want ErrInvalid", fmtErr(err))
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[1].ID, Mode: PublishModeTopologyExperiment,
		TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", ScenarioTarget: "scen-1", DocumentID: "doc-1",
	})
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	switch {
	case published.Dirty():
		t.Fatal("a draft published at its current snapshot must be clean")
	case published.Publication.SnapshotID != meta.History[1].ID:
		t.Fatalf("publication snapshot = %q, want the current snapshot", published.Publication.SnapshotID)
	case published.Publication.Digest != meta.History[1].Digest:
		t.Fatal("publication digest must match the published snapshot")
	case published.Publication.Revision != meta.Revision:
		t.Fatalf("publication revision = %d, want the observed revision %d", published.Publication.Revision, meta.Revision)
	case published.Publication.PublishedBy != testPeer || published.LastModifiedBy != testPeer:
		t.Fatalf("publication actor = %q, want the cross-user actor recorded", published.Publication.PublishedBy)
	case published.Publication.Mode != PublishModeTopologyExperiment:
		t.Fatalf("publication = %+v, want the requested mode recorded", published.Publication)
	case published.Publication.TopologyTarget != "topo" || published.Publication.TopologyAction != TopologyActionUpdate:
		t.Fatalf("publication = %+v, want the topology target and action recorded", published.Publication)
	case published.Publication.ExperimentTarget != "exp-1" || published.Publication.ScenarioTarget != "scen-1":
		t.Fatalf("publication = %+v, want the experiment and scenario targets recorded", published.Publication)
	case !published.PublishedAs(PublishModeTopologyExperiment, "topo"):
		t.Fatal("draft must be clean for exactly the operation it was published with")
	case published.PublishedAs(PublishModeTopology, "topo"):
		t.Fatal("draft must not be clean for a different publication operation")
	}

	// Editing after publishing makes the draft dirty again.
	edited := appendTestSnapshot(t, h, published, "topo-v3", testActor)
	if !edited.Dirty() {
		t.Fatal("a draft edited after publishing must be dirty")
	}

	// Moving back to the published snapshot makes it clean again.
	restored, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: edited.Revision,
		Index: 0, SnapshotID: published.Publication.SnapshotID, UseIndex: false,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	if restored.Dirty() {
		t.Fatal("moving the cursor back to the published snapshot must make the draft clean")
	}
}

func TestDeleteDraftCASAndChunkCleanup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, meta.Revision+4); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteDraft with a stale revision error = %s, want ErrConflict", fmtErr(err))
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Fatalf("draft must survive a conflicting delete: %v", err)
	}

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, meta.Revision); err != nil {
		t.Fatalf("DeleteDraft returned error: %v", err)
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDraft after delete error = %s, want ErrNotFound", fmtErr(err))
	}

	if chunks := chunkKeysOf(h, draftScope(meta.ID)); len(chunks) != 0 {
		t.Fatalf("chunks after delete = %v, want none", chunks)
	}

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, store.AnyRevision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a missing draft error = %s, want ErrNotFound", fmtErr(err))
	}
}

func TestDeleteDraftReportsChunkCleanupFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	h.store.failDelete = func(namespace, _ string) error {
		if namespace != NamespaceChunks {
			return nil
		}

		return errors.New("chunk store is unavailable")
	}

	err := h.service.DeleteDraft(ctx, meta.ID, testActor, meta.Revision)
	if !errors.Is(err, ErrCleanup) {
		t.Fatalf("DeleteDraft error = %s, want ErrCleanup", fmtErr(err))
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the draft metadata must still be deleted, got %s", fmtErr(err))
	}
}

func TestRequestValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "create without owner",
			call: func() error {
				_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
					Owner: "", Actor: testActor, Title: "", SourceToken: "",
					Document: testDocument(t, "x", 0), Summary: "", ID: "",
				})

				return err
			},
		},
		{
			name: "create without actor",
			call: func() error {
				_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
					Owner: testOwner, Actor: "", Title: "", SourceToken: "",
					Document: testDocument(t, "x", 0), Summary: "", ID: "",
				})

				return err
			},
		},
		{
			name: "create with an unsafe ID",
			call: func() error {
				_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
					Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
					Document: testDocument(t, "x", 0), Summary: "", ID: "../escape",
				})

				return err
			},
		},
		{
			name: "append without actor",
			call: func() error {
				_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
					DraftID: "draft", Actor: "", ExpectedRevision: 1,
					Document: testDocument(t, "x", 0), Summary: "",
				})

				return err
			},
		},
		{
			name: "delete without actor",
			call: func() error {
				return h.service.DeleteDraft(ctx, "draft", "", 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}
}

func TestServiceRejectsBadChunkSize(t *testing.T) {
	if _, err := New(WithStore(newFakeStore()), WithChunkSize(0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("New with a zero chunk size error = %s, want ErrInvalid", fmtErr(err))
	}

	if _, err := New(WithStore(newFakeStore()), WithChunkSize(ChunkBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("New with an oversized chunk size error = %s, want ErrInvalid", fmtErr(err))
	}
}

func TestDraftsSurviveServiceRestartOnBoltDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "phenix.bdb")

	first := store.NewBoltDB()
	if err := first.Init(store.Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	service, err := New(WithStore(first), WithChunkSize(4096))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	meta, err := service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "Topo", SourceToken: "Topology/topo",
		Document: testRandomDocument(t, "topo", 40000), Summary: "initial", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	meta, err = service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		Document: testRandomDocument(t, "topo-v2", 40000), Summary: "second",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot returned error: %v", err)
	}

	// A brand new service over the same database file must recover everything.
	second := store.NewBoltDB()
	if err := second.Init(store.Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("reopening BoltDB returned error: %v", err)
	}

	reloaded, err := New(WithStore(second), WithChunkSize(4096))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	restored, err := reloaded.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft after restart returned error: %v", err)
	}

	switch {
	case restored.Revision != meta.Revision:
		t.Fatalf("revision after restart = %d, want %d", restored.Revision, meta.Revision)
	case len(restored.History) != 2 || restored.Cursor != 1:
		t.Fatalf("history after restart = %d entries, cursor %d", len(restored.History), restored.Cursor)
	case restored.LastModifiedBy != testPeer:
		t.Fatalf("lastModifiedBy after restart = %q, want %q", restored.LastModifiedBy, testPeer)
	}

	snapshot, err := reloaded.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument after restart returned error: %v", err)
	}

	if !bytes.Equal(snapshot.Data, testRandomDocument(t, "topo-v2", 40000)) {
		t.Fatal("document content did not survive the restart")
	}

	if len(snapshot.Manifest.ChunkDigests) < 2 {
		t.Fatalf("chunk count = %d, want a multi-chunk payload in this test", len(snapshot.Manifest.ChunkDigests))
	}

	// Continuing to edit after the restart still works against the recovered
	// revision.
	if _, err := reloaded.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: restored.Revision,
		Document: testDocument(t, "topo-v3", 100), Summary: "third",
	}); err != nil {
		t.Fatalf("AppendSnapshot after restart returned error: %v", err)
	}
}
