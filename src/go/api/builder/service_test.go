package builder

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types/builder"
)

// draftDiff returns how two drafts differ, as cmp.Diff does, in all but the
// document stamp, which only the call that stored a snapshot returns.
func draftDiff(want, got *DraftMetadata) string {
	return cmp.Diff(want, got, cmpopts.IgnoreFields(DraftMetadata{}, "Stamp"))
}

// draftState is where the history of a draft stands, and who created and last
// changed the draft.
type draftState struct {
	Snapshots      int
	Cursor         int
	CreatedBy      string
	LastModifiedBy string
}

func stateOf(meta *DraftMetadata) draftState {
	return draftState{
		Snapshots: len(meta.History), Cursor: meta.Cursor,
		CreatedBy: meta.CreatedBy, LastModifiedBy: meta.LastModifiedBy,
	}
}

// snapshotIDs returns the ID of each snapshot, in order.
func snapshotIDs(history []SnapshotManifest) []string {
	ids := make([]string, len(history))
	for i := range history {
		ids[i] = history[i].ID
	}

	return ids
}

// draftIDs returns the ID of each draft, in order.
func draftIDs(drafts []DraftMetadata) []string {
	ids := make([]string, len(drafts))
	for i := range drafts {
		ids[i] = drafts[i].ID
	}

	return ids
}

func TestCreateDraftAndReadCurrentDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	data := testDocument(t, "topo-a", 0)

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "Topo A",
		SourceToken: "Topology/topo-a", Document: data, Summary: "initial",
	})
	if err != nil {
		t.Fatalf("CreateDraft(topo-a) returned error: %v", err)
	}

	if meta.Revision <= 0 {
		t.Errorf("CreateDraft(topo-a) revision = %d, want more than 0", meta.Revision)
	}

	want := draftState{Snapshots: 1, Cursor: 0, CreatedBy: testActor, LastModifiedBy: testActor}
	if diff := cmp.Diff(want, stateOf(meta)); diff != "" {
		t.Errorf("CreateDraft(topo-a) mismatch (-want +got):\n%s", diff)
	}

	if got, want := meta.ETag(), `"`+strconv.FormatInt(meta.Revision, 10)+`"`; got != want {
		t.Errorf("CreateDraft(topo-a) ETag() = %s, want the revision based %s", got, want)
	}

	if !meta.Dirty() {
		t.Error("CreateDraft(topo-a) Dirty() = false, want true for a never published draft")
	}

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(%s) returned error: %v", meta.ID, err)
	}

	if diff := contentDiff(t, data, snapshot.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch (-want +got):\n%s", diff)
	}

	doc, err := snapshot.Decode()
	if err != nil {
		t.Fatalf("Snapshot.Decode returned error: %v", err)
	}

	if got, want := doc.Metadata.ID, builder.DocumentID("topo-a"); got != want {
		t.Errorf("GetCurrentDocument(%s) document ID = %q, want %q", meta.ID, got, want)
	}

	fetched, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) returned error: %v", meta.ID, err)
	}

	if diff := draftDiff(meta, fetched); diff != "" {
		t.Errorf("GetDraft(%s) mismatch with the created draft (-want +got):\n%s", meta.ID, diff)
	}

	if fetched.SourceToken != "Topology/topo-a" {
		t.Errorf("GetDraft(%s) source = %q, want %q", meta.ID, fetched.SourceToken, "Topology/topo-a")
	}
}

func TestGetDraftNotFound(t *testing.T) {
	h := newHarness(t)

	_, err := h.service.GetDraft(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDraft(missing) error = %s, want ErrNotFound", fmtErr(err))
	}

	var notFound *NotFoundError
	if !errors.As(err, &notFound) || notFound.Kind != "draft" {
		t.Errorf("GetDraft(missing) error = %s, want *NotFoundError for a draft", fmtErr(err))
	}
}

func TestCorruptDraftIsSkippedAndCanBeDeleted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	good := createTestDraft(t, h, "good")
	bad := createTestDraft(t, h, "bad")
	content := contentKeysOf(h, draftContentPrefix(bad.ID))

	// A field this version does not know fails the strict decoder, as a record
	// written by a newer phenix does after a rollback.
	record, err := h.store.GetRecord(NamespaceDrafts, bad.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	h.store.SetValue(NamespaceDrafts, bad.ID, append(bytes.TrimSuffix(record.Value, []byte("}")), `,"future":true}`...))

	if _, err := h.service.GetDraft(ctx, bad.ID); !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetDraft(%s) error = %s, want ErrCorrupt", bad.ID, fmtErr(err))
	}

	all, _, err := h.service.ListDraftsWithDamaged(ctx)
	if err != nil {
		t.Fatalf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
	}

	if diff := cmp.Diff([]string{good.ID}, draftIDs(all)); diff != "" {
		t.Errorf("ListDraftsWithDamaged IDs mismatch (-want +got):\n%s", diff)
	}

	// Orphan cleanup must not mistake the unreadable draft's content for
	// orphans, nor stop cleaning up everything else.
	storePayload(t, h, snapshotKey("vanished-draft", "vanished-snapshot"), []byte(randomText(9, 2000)))

	h.passOrphanGracePeriod()

	if removed, err := h.service.CleanupOrphanedContent(ctx, false); err != nil {
		t.Errorf("CleanupOrphanedContent returned error: %s", fmtErr(err))
	} else if removed != 1 {
		t.Errorf("CleanupOrphanedContent = %d, want the orphaned record removed", removed)
	}

	if diff := cmp.Diff(content, contentKeysOf(h, draftContentPrefix(bad.ID))); diff != "" {
		t.Errorf("content of the unreadable draft after CleanupOrphanedContent mismatch (-want +got):\n%s", diff)
	}

	// Its revision can still be read, so the web layer can delete it.
	stored, err := h.service.GetDamagedDraft(ctx, bad.ID)
	if err != nil {
		t.Fatalf("GetDamagedDraft(%s) returned error: %v", bad.ID, err)
	}

	want := DamagedDraft{ID: bad.ID, CreatedBy: testActor, Revision: bad.Revision}
	got := DamagedDraft{ID: stored.ID, CreatedBy: stored.CreatedBy, Revision: stored.Revision}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetDamagedDraft(%s) mismatch (-want +got):\n%s", bad.ID, diff)
	}

	if err := h.service.DeleteDraft(ctx, bad.ID, testActor, stored.Revision); err != nil {
		t.Fatalf("DeleteDraft(%s) returned error: %v", bad.ID, err)
	}

	if got := contentKeysOf(h, draftContentPrefix(bad.ID)); len(got) != 0 {
		t.Errorf("content after DeleteDraft(%s) = %v, want none", bad.ID, got)
	}

	// A record that is not even JSON can still be deleted.
	h.store.SetValue(NamespaceDrafts, good.ID, []byte("{not json"))

	if stored, err := h.service.GetDamagedDraft(ctx, good.ID); err != nil {
		t.Errorf("GetDamagedDraft(%s) of unparsable metadata returned error: %s", good.ID, fmtErr(err))
	} else {
		if stored.CreatedBy != "" {
			t.Errorf("GetDamagedDraft(%s) of unparsable metadata creator = %q, want none", good.ID, stored.CreatedBy)
		}

		if stored.Revision <= 0 {
			t.Errorf("GetDamagedDraft(%s) of unparsable metadata revision = %d, want more than 0", good.ID, stored.Revision)
		}
	}
}

// TestDamagedDraftsAreListedWithWhatCanBeRead lists a draft that does not decode
// apart, with each field that still reads and the revision that deletes it.
func TestDamagedDraftsAreListedWithWhatCanBeRead(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	good := createTestDraft(t, h, "good")
	bad := createTestDraft(t, h, "bad")

	record, err := h.store.GetRecord(NamespaceDrafts, bad.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	h.store.SetValue(NamespaceDrafts, bad.ID, append(bytes.TrimSuffix(record.Value, []byte("}")), `,"future":true}`...))

	readable, damaged, err := h.service.ListDraftsWithDamaged(ctx)
	if err != nil {
		t.Fatalf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
	}

	if diff := cmp.Diff([]string{good.ID}, draftIDs(readable)); diff != "" {
		t.Errorf("ListDraftsWithDamaged readable IDs mismatch (-want +got):\n%s", diff)
	}

	// The revision is the one whose ETag deletes the draft.
	want := []DamagedDraft{{ID: bad.ID, CreatedBy: testActor, Title: bad.Title, Updated: bad.Updated, Revision: bad.Revision}}
	if diff := cmp.Diff(want, damaged); diff != "" {
		t.Errorf("ListDraftsWithDamaged damaged mismatch (-want +got):\n%s", diff)
	}

	for _, value := range []struct {
		name, json, creator, title string
	}{
		// One field of the wrong type leaves only itself out.
		{name: "wrong types", json: `{"createdBy":"alice","title":42,"updated":"yesterday"}`, creator: testActor},
		// So does one that would not pass validation.
		{name: "unusable creator", json: `{"createdBy":"al\u0000ice","title":"ok"}`, title: "ok"},
		// A record that is not JSON leaves only its ID.
		{name: "not JSON", json: "{not json"},
	} {
		t.Run(value.name, func(t *testing.T) {
			h.store.SetValue(NamespaceDrafts, bad.ID, []byte(value.json))

			_, damaged, err := h.service.ListDraftsWithDamaged(ctx)
			if err != nil {
				t.Errorf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
			} else {
				// Nothing reads a date, so none is listed.
				want := []DamagedDraft{{ID: bad.ID, CreatedBy: value.creator, Title: value.title}}
				if diff := cmp.Diff(want, damaged, cmpopts.IgnoreFields(DamagedDraft{}, "Revision")); diff != "" {
					t.Errorf("ListDraftsWithDamaged damaged mismatch (-want +got):\n%s", diff)
				}
			}

			if stored, err := h.service.GetDamagedDraft(ctx, bad.ID); err != nil {
				t.Errorf("GetDamagedDraft(%s) returned error: %s", bad.ID, fmtErr(err))
			} else if stored.CreatedBy != value.creator {
				t.Errorf("GetDamagedDraft(%s) creator = %q, want %q", bad.ID, stored.CreatedBy, value.creator)
			}
		})
	}
}

func TestAppendSnapshotAdvancesHistoryAndRecordsActor(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	updated := appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	// The cross-user edit records its actor and leaves the creator unchanged.
	want := draftState{Snapshots: 2, Cursor: 1, CreatedBy: testActor, LastModifiedBy: testPeer}
	if diff := cmp.Diff(want, stateOf(updated)); diff != "" {
		t.Errorf("AppendSnapshot(%s) mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if len(updated.History) != 2 {
		t.Fatalf("AppendSnapshot(%s) history = %d snapshots, want 2", meta.ID, len(updated.History))
	}

	if updated.Revision <= meta.Revision {
		t.Errorf("AppendSnapshot(%s) revision = %d, want more than %d", meta.ID, updated.Revision, meta.Revision)
	}

	creators := []string{updated.History[0].CreatedBy, updated.History[1].CreatedBy}
	if diff := cmp.Diff([]string{testActor, testPeer}, creators); diff != "" {
		t.Errorf("AppendSnapshot(%s) snapshot creators mismatch (-want +got):\n%s", meta.ID, diff)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(%s) returned error: %v", meta.ID, err)
	}

	if diff := contentDiff(t, testDocument(t, "topo-v2", 0), current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) content mismatch with the appended snapshot (-want +got):\n%s", diff)
	}

	first, err := h.service.GetSnapshot(ctx, meta.ID, updated.History[0].ID)
	if err != nil {
		t.Fatalf("GetSnapshot(%s) returned error: %v", updated.History[0].ID, err)
	}

	if diff := contentDiff(t, testDocument(t, "topo", 0), first.Data); diff != "" {
		t.Errorf("GetSnapshot(first snapshot) content mismatch (-want +got):\n%s", diff)
	}

	if _, err := h.service.GetSnapshot(ctx, meta.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSnapshot(nope) error = %s, want ErrNotFound", fmtErr(err))
	}

	// The client's id for a save is kept with its snapshot. One that is not an
	// id is refused.
	request := AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: updated.Revision,
		Document: testDocument(t, "topo-v3", 0), Summary: "v3", OpID: "not an id",
	}

	if _, err := h.service.AppendSnapshot(ctx, request); !errors.Is(err, ErrInvalid) {
		t.Errorf("AppendSnapshot with opId %q error = %s, want ErrInvalid", request.OpID, fmtErr(err))
	}

	request.OpID = "4f1c2a90-8d3e-4b7a-9c61-2e5f7a8b9c0d"

	third, err := h.service.AppendSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("AppendSnapshot with opId %q returned error: %v", request.OpID, err)
	}

	if got := third.Current().OpID; got != request.OpID {
		t.Errorf("AppendSnapshot with opId %q snapshot opId = %q, want %q", request.OpID, got, request.OpID)
	}

	if got := third.History[1].OpID; got != "" {
		t.Errorf("opId of a snapshot saved without one = %q, want none", got)
	}
}

func TestAppendSnapshotConflictLeavesNoContentBehind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	scope := draftContentPrefix(meta.ID)
	before := contentKeysOf(h, scope)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 5,
		Document: testDocument(t, "topo-v2", 0), Summary: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("AppendSnapshot(%s) at a stale revision error = %s, want ErrConflict", meta.ID, fmtErr(err))
	}

	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("AppendSnapshot(%s) at a stale revision error = %s, want *ConflictError", meta.ID, fmtErr(err))
	}

	want := ConflictError{Expected: meta.Revision + 5, Actual: meta.Revision}
	if diff := cmp.Diff(want, *conflict, cmpopts.IgnoreFields(ConflictError{}, "Kind", "ID", "Reason")); diff != "" {
		t.Errorf("AppendSnapshot(%s) conflict mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if diff := cmp.Diff(before, contentKeysOf(h, scope)); diff != "" {
		t.Errorf("content after a conflicting AppendSnapshot(%s) mismatch (-want +got):\n%s", meta.ID, diff)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) returned error: %v", meta.ID, err)
	}

	if diff := draftDiff(meta, stored); diff != "" {
		t.Errorf("GetDraft(%s) after a conflicting append mismatch (-want +got):\n%s", meta.ID, diff)
	}
}

// TestFailedWritesStoreNoContent asserts that a draft write the store
// refused leaves no content of the attempt behind.
func TestFailedWritesStoreNoContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	before := contentKeysOf(h, draftContentPrefix(meta.ID))
	refused := errors.New("disk full")

	h.store.BeforeUpdate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return refused
	}

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "topo-v2", 0), Summary: "",
	})
	if !errors.Is(err, refused) {
		t.Errorf("AppendSnapshot(%s) error = %s, want the store error", meta.ID, fmtErr(err))
	}

	h.store.BeforeCreate = func(namespace, _ string) error {
		if namespace != NamespaceDrafts {
			return nil
		}

		return refused
	}

	h.service.newID = func() (string, error) { return "other", nil }

	_, err = h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, "other", 0), Summary: "",
	})
	if !errors.Is(err, refused) {
		t.Errorf("CreateDraft(other) error = %s, want the store error", fmtErr(err))
	}

	// A refused append stores no content and keeps the draft's.
	if diff := cmp.Diff(before, contentKeysOf(h, draftContentPrefix(meta.ID))); diff != "" {
		t.Errorf("content after a refused AppendSnapshot(%s) mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if got := contentKeysOf(h, draftContentPrefix("other")); len(got) != 0 {
		t.Errorf("content after a refused CreateDraft(other) = %v, want none", got)
	}

	if _, err := h.service.GetCurrentDocument(ctx, meta.ID); err != nil {
		t.Errorf("GetCurrentDocument(%s) returned error: %v", meta.ID, err)
	}
}

func TestMoveCursorAndRedoBranchTruncation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v1")
	meta = appendTestSnapshot(t, h, meta, "v2", testActor)
	meta = appendTestSnapshot(t, h, meta, "v3", testActor)

	if !meta.CanUndo() {
		t.Errorf("CanUndo() at cursor %d of %d = false, want true", meta.Cursor, len(meta.History))
	}

	if meta.CanRedo() {
		t.Errorf("CanRedo() at cursor %d of %d = true, want false", meta.Cursor, len(meta.History))
	}

	droppedID := meta.History[2].ID

	moved, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		Index: 1, SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("MoveCursor(%s, 1) returned error: %v", meta.ID, err)
	}

	// The redo branch is retained.
	want := draftState{Snapshots: 3, Cursor: 1, CreatedBy: testActor, LastModifiedBy: testPeer}
	if diff := cmp.Diff(want, stateOf(moved)); diff != "" {
		t.Errorf("MoveCursor(%s, 1) mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if !moved.CanRedo() {
		t.Errorf("MoveCursor(%s, 1) CanRedo() = false, want true", meta.ID)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(%s) returned error: %v", meta.ID, err)
	}

	if diff := contentDiff(t, testDocument(t, "v2", 0), current.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) after the cursor move content mismatch (-want +got):\n%s", diff)
	}

	// Appending from a moved cursor discards the redo branch and its content.
	branched := appendTestSnapshot(t, h, moved, "v4", testActor)

	want = draftState{Snapshots: 3, Cursor: 2, CreatedBy: testActor, LastModifiedBy: testActor}
	if diff := cmp.Diff(want, stateOf(branched)); diff != "" {
		t.Errorf("AppendSnapshot(%s) after the cursor move mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if n := len(branched.History); n > 0 && branched.History[n-1].Summary != "v4" {
		t.Errorf("AppendSnapshot(%s) last snapshot = %q, want the newly appended v4", meta.ID, branched.History[n-1].Summary)
	}

	if key := snapshotKey(meta.ID, droppedID); len(contentKeysOf(h, key)) != 0 {
		t.Errorf("content %s of the discarded redo branch was not removed", key)
	}
}

func TestMoveCursorValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v1")

	_, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Index: 7, SnapshotID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("MoveCursor(%s, 7) out of range error = %s, want ErrInvalid", meta.ID, fmtErr(err))
	}

	_, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Index: 0, SnapshotID: "unknown",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("MoveCursor(%s, unknown) error = %s, want ErrNotFound", meta.ID, fmtErr(err))
	}

	_, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 3,
		Index: 0, SnapshotID: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MoveCursor(%s, 0) with a stale revision error = %s, want ErrConflict", meta.ID, fmtErr(err))
	}
}

func TestHistoryPrunedToMaxSnapshots(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v0")

	for i := 1; i <= MaxSnapshots+5; i++ {
		meta = appendTestSnapshot(t, h, meta, "v"+strconv.Itoa(i), testActor)
	}

	want := draftState{Snapshots: MaxSnapshots, Cursor: MaxSnapshots - 1, CreatedBy: testActor, LastModifiedBy: testActor}
	if diff := cmp.Diff(want, stateOf(meta)); diff != "" {
		t.Errorf("AppendSnapshot past the limit mismatch (-want +got):\n%s", diff)
	}

	// The oldest snapshots are pruned.
	oldest := "v" + strconv.Itoa(MaxSnapshots+5-MaxSnapshots+1)
	if len(meta.History) > 0 && meta.History[0].Summary != oldest {
		t.Errorf("oldest retained snapshot = %q, want %q", meta.History[0].Summary, oldest)
	}

	// Every retained snapshot must still be readable, and pruned content must be
	// gone.
	for i := range meta.History {
		if _, err := h.service.GetSnapshot(ctx, meta.ID, meta.History[i].ID); err != nil {
			t.Errorf("GetSnapshot(%s) returned error: %v", meta.History[i].ID, err)
		}
	}

	if content := contentKeysOf(h, draftContentPrefix(meta.ID)); len(content) != len(meta.History) {
		t.Errorf("retained content records = %d, want %d (pruned snapshot content must be removed)", len(content), len(meta.History))
	}
}

func TestPruneHistoryEnforcesTheByteLimit(t *testing.T) {
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

	// 12 x 5 MiB is past the 50 MiB byte limit, so the two oldest snapshots
	// are pruned, well before the snapshot count limit is reached.
	if diff := cmp.Diff([]string{"snap-0", "snap-1"}, snapshotIDs(dropped)); diff != "" {
		t.Errorf("pruneHistory dropped mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(draftState{Snapshots: 10, Cursor: 9}, stateOf(meta)); diff != "" {
		t.Errorf("pruneHistory history mismatch (-want +got):\n%s", diff)
	}

	if got := meta.HistoryBytes(); got > MaxDraftHistoryBytes {
		t.Errorf("pruneHistory HistoryBytes() = %d, want at most %d", got, int64(MaxDraftHistoryBytes))
	}

	// A snapshot larger than the whole budget is kept, as the cursor snapshot
	// always is: AppendSnapshot rejects the edit that would store it.
	meta.History = append(meta.History, SnapshotManifest{ID: "huge", Size: MaxDraftHistoryBytes + 1})
	meta.Cursor = len(meta.History) - 1

	pruneHistory(meta)

	if diff := cmp.Diff([]string{"huge"}, snapshotIDs(meta.History)); diff != "" {
		t.Errorf("pruneHistory with a huge cursor snapshot history mismatch (-want +got):\n%s", diff)
	}
}

func TestPruneHistoryNeverDropsTheCursorSnapshot(t *testing.T) {
	meta := &DraftMetadata{
		ID:      "draft",
		History: make([]SnapshotManifest, 0, MaxSnapshots+10),
	}

	// Past both the snapshot count and the byte limit.
	for i := range MaxSnapshots + 10 {
		meta.History = append(meta.History, SnapshotManifest{ID: "snap-" + strconv.Itoa(i), Size: 5 << 20})
	}

	meta.Cursor = 0

	dropped := pruneHistory(meta)

	if len(dropped) != 0 {
		t.Errorf("pruneHistory dropped %d snapshots, want none when the cursor is at the oldest snapshot", len(dropped))
	}

	if got := meta.Current(); got == nil || got.ID != "snap-0" {
		t.Errorf("pruneHistory cursor snapshot = %+v, want snap-0", got)
	}
}

func TestDocumentSizeLimitRejectedBeforeDurableWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	drafts := h.store.Count(NamespaceDrafts)
	content := h.store.Count(NamespaceContent)

	oversized := bytes.Repeat([]byte("a"), MaxDocumentBytes+1)

	_, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: oversized, Summary: "",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("AppendSnapshot(%s) of an oversized document error = %s, want ErrTooLarge", meta.ID, fmtErr(err))
	}

	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("AppendSnapshot(%s) of an oversized document error = %s, want *TooLargeError", meta.ID, fmtErr(err))
	}

	if tooLarge.Limit != MaxDocumentBytes {
		t.Errorf("AppendSnapshot(%s) of an oversized document limit = %d, want %d", meta.ID, tooLarge.Limit, MaxDocumentBytes)
	}

	// The document is rejected before anything is written.
	if got := h.store.Count(NamespaceContent); got != content {
		t.Errorf("content after an oversized AppendSnapshot(%s) = %d, want %d", meta.ID, got, content)
	}

	if got := h.store.Count(NamespaceDrafts); got != drafts {
		t.Errorf("drafts after an oversized AppendSnapshot(%s) = %d, want %d", meta.ID, got, drafts)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) returned error: %v", meta.ID, err)
	}

	if diff := draftDiff(meta, stored); diff != "" {
		t.Errorf("GetDraft(%s) after an oversized append mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if _, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "big",
		SourceToken: "", Document: oversized, Summary: "",
	}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("CreateDraft(big) of an oversized document error = %s, want ErrTooLarge", fmtErr(err))
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
		t.Errorf("EncodeDocument(huge) error = %s, want ErrTooLarge", fmtErr(err))
	}

	if _, err := EncodeDocument(nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("EncodeDocument(nil) error = %s, want ErrInvalid", fmtErr(err))
	}

	// ParseDocument, for a document used without being stored, has the
	// limits saving has.
	if _, err := ParseDocument([]byte(strings.Repeat(" ", MaxDocumentBytes+1))); !errors.Is(err, ErrTooLarge) {
		t.Errorf("ParseDocument(oversized) error = %s, want ErrTooLarge", fmtErr(err))
	}

	if _, err := ParseDocument([]byte(`{"apiVersion":"builder/v1","kind":"nope"}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("ParseDocument(kind nope) error = %s, want ErrInvalid", fmtErr(err))
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
		t.Errorf("MarkPublished(%s) with a non-current snapshot error = %s, want ErrConflict", meta.ID, fmtErr(err))
	}

	_, err = h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision + 9,
		SnapshotID: meta.History[1].ID, Mode: PublishModeTopology, TopologyTarget: "topo",
		TopologyAction: TopologyActionCreate, ExperimentTarget: "", ScenarioTarget: "", DocumentID: "",
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("MarkPublished(%s) with a stale revision error = %s, want ErrConflict", meta.ID, fmtErr(err))
	}

	_, err = h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[1].ID, Mode: "sideways", TopologyTarget: "topo",
		TopologyAction: TopologyActionCreate, ExperimentTarget: "", ScenarioTarget: "", DocumentID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("MarkPublished(%s) with an unknown mode error = %s, want ErrInvalid", meta.ID, fmtErr(err))
	}

	published, err := h.service.MarkPublished(ctx, MarkPublishedRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		SnapshotID: meta.History[1].ID, Mode: PublishModeTopologyExperiment,
		TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", ScenarioTarget: "scen-1", DocumentID: "doc-1",
	})
	if err != nil {
		t.Fatalf("MarkPublished(%s) returned error: %v", meta.ID, err)
	}

	if published.Publication == nil {
		t.Fatalf("MarkPublished(%s) publication = nil, want the publication recorded", meta.ID)
	}

	if published.Dirty() {
		t.Errorf("MarkPublished(%s) Dirty() = true, want false at the published snapshot", meta.ID)
	}

	// The cross-user actor, the observed revision and the requested operation
	// are recorded.
	want := &PublicationState{
		Mode: PublishModeTopologyExperiment, TopologyTarget: "topo", TopologyAction: TopologyActionUpdate,
		ExperimentTarget: "exp-1", ScenarioTarget: "scen-1", SnapshotID: meta.History[1].ID,
		Digest: meta.History[1].Digest, Revision: meta.Revision, DocumentID: "doc-1", PublishedBy: testPeer,
	}

	if diff := publicationDiff(want, published.Publication); diff != "" {
		t.Errorf("MarkPublished(%s) publication mismatch (-want +got):\n%s", meta.ID, diff)
	}

	if published.LastModifiedBy != testPeer {
		t.Errorf("MarkPublished(%s) lastModifiedBy = %q, want %q", meta.ID, published.LastModifiedBy, testPeer)
	}

	// Editing after publishing makes the draft dirty again.
	edited := appendTestSnapshot(t, h, published, "topo-v3", testActor)
	if !edited.Dirty() {
		t.Errorf("AppendSnapshot(%s) after publishing Dirty() = false, want true", meta.ID)
	}

	// Moving back to the published snapshot makes it clean again.
	restored, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: edited.Revision,
		Index: 0, SnapshotID: published.Publication.SnapshotID,
	})
	if err != nil {
		t.Fatalf("MoveCursor(%s, %s) returned error: %v", meta.ID, published.Publication.SnapshotID, err)
	}

	if restored.Dirty() {
		t.Errorf("MoveCursor(%s) to the published snapshot Dirty() = true, want false", meta.ID)
	}
}

func TestDeleteDraftCASAndContent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, meta.Revision+4); !errors.Is(err, ErrConflict) {
		t.Errorf("DeleteDraft(%s) with a stale revision error = %s, want ErrConflict", meta.ID, fmtErr(err))
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) after a conflicting delete returned error: %v", meta.ID, err)
	}

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, meta.Revision); err != nil {
		t.Fatalf("DeleteDraft(%s) returned error: %v", meta.ID, err)
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDraft(%s) after delete error = %s, want ErrNotFound", meta.ID, fmtErr(err))
	}

	if content := contentKeysOf(h, draftContentPrefix(meta.ID)); len(content) != 0 {
		t.Errorf("content after DeleteDraft(%s) = %v, want none", meta.ID, content)
	}

	if err := h.service.DeleteDraft(ctx, meta.ID, testActor, store.AnyRevision); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteDraft(%s) of a missing draft error = %s, want ErrNotFound", meta.ID, fmtErr(err))
	}
}

func TestRequestValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tests := []struct {
		name string
		op   string
		call func() error
	}{
		{
			name: "append without actor",
			op:   "AppendSnapshot",
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
			op:   "DeleteDraft",
			call: func() error {
				return h.service.DeleteDraft(ctx, "draft", "", 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s(draft) without an actor error = %s, want ErrInvalid", tt.op, fmtErr(err))
			}
		})
	}
}

func TestDraftsSurviveServiceRestartOnBoltDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "phenix.bdb")

	first := new(store.BoltDB)
	if err := first.Init(store.Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	service, err := New(WithStore(first))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	meta, err := service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "Topo", SourceToken: "Topology/topo",
		Document: testRandomDocument(t, "topo", 40000), Summary: "initial",
	})
	if err != nil {
		t.Fatalf("CreateDraft(topo) returned error: %v", err)
	}

	meta, err = service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		Document: testRandomDocument(t, "topo-v2", 40000), Summary: "second",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot(%s) returned error: %v", meta.ID, err)
	}

	// A brand new service over the same database file must recover everything.
	second := new(store.BoltDB)
	if err := second.Init(store.Endpoint("bolt://" + path)); err != nil {
		t.Fatalf("reopening BoltDB returned error: %v", err)
	}

	reloaded, err := New(WithStore(second))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	restored, err := reloaded.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft(%s) after restart returned error: %v", meta.ID, err)
	}

	if restored.Revision != meta.Revision {
		t.Errorf("GetDraft(%s) after restart revision = %d, want %d", meta.ID, restored.Revision, meta.Revision)
	}

	want := draftState{Snapshots: 2, Cursor: 1, CreatedBy: testActor, LastModifiedBy: testPeer}
	if diff := cmp.Diff(want, stateOf(restored)); diff != "" {
		t.Errorf("GetDraft(%s) after restart mismatch (-want +got):\n%s", meta.ID, diff)
	}

	snapshot, err := reloaded.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument(%s) after restart returned error: %v", meta.ID, err)
	}

	if diff := contentDiff(t, testRandomDocument(t, "topo-v2", 40000), snapshot.Data); diff != "" {
		t.Errorf("GetCurrentDocument(draft) after restart content mismatch (-want +got):\n%s", diff)
	}

	if got := documentProvenance(t, snapshot.Data).UpdatedBy; got != testPeer {
		t.Errorf("GetCurrentDocument(draft) after restart UpdatedBy = %q, want %q", got, testPeer)
	}

	// Continuing to edit after the restart still works against the recovered
	// revision.
	if _, err := reloaded.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: restored.Revision,
		Document: testDocument(t, "topo-v3", 100), Summary: "third",
	}); err != nil {
		t.Errorf("AppendSnapshot(%s) after restart returned error: %v", meta.ID, err)
	}
}

func TestCreateDraftRefusesPastMaxDrafts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// A damaged draft counts, as it holds storage until it is deleted.
	for i := range MaxDrafts - 1 {
		if _, err := h.store.CreateRecord(NamespaceDrafts, "full-"+strconv.Itoa(i), []byte("{}")); err != nil {
			t.Fatalf("CreateRecord(full-%d) returned error: %v", i, err)
		}
	}

	last := createTestDraft(t, h, "last")
	content := h.store.Count(NamespaceContent)

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "one more", Document: testDocument(t, "one-more", 0),
	})

	var full *DraftLimitError
	if !errors.As(err, &full) || full.Limit != MaxDrafts || !errors.Is(err, ErrTooLarge) {
		t.Errorf("CreateDraft past %d drafts error = %s, want a DraftLimitError that matches ErrTooLarge", MaxDrafts, fmtErr(err))
	}

	if got := h.store.Count(NamespaceDrafts); got != MaxDrafts {
		t.Errorf("drafts after a refused CreateDraft = %d, want %d", got, MaxDrafts)
	}

	if got := h.store.Count(NamespaceContent); got != content {
		t.Errorf("content after a refused CreateDraft = %d, want %d", got, content)
	}

	// Deleting one makes room for one.
	if err := h.service.DeleteDraft(ctx, last.ID, testActor, last.Revision); err != nil {
		t.Fatalf("DeleteDraft(%s) returned error: %v", last.ID, err)
	}

	createTestDraft(t, h, "again")
}
