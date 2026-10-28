package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

// claimedDocument returns the test document of name, claiming the given
// creator, creation time, last editor and last edit time.
func claimedDocument(t *testing.T, name string, claim builder.Provenance) []byte {
	t.Helper()

	return stampedDocument(t, testDocument(t, name, 0), claim)
}

// metadataOf returns the metadata object of a document decoded as a map.
func metadataOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()

	metadata, ok := doc["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("the document has no metadata object: %v", doc)
	}

	return metadata
}

// snapshotProvenance returns what the document of one snapshot of a draft
// holds in its four provenance fields.
func snapshotProvenance(t *testing.T, h *testHarness, draftID, snapshotID string) builder.Provenance {
	t.Helper()

	snapshot, err := h.service.GetSnapshot(context.Background(), draftID, snapshotID)
	if err != nil {
		t.Fatalf("GetSnapshot(%s) returned error: %v", snapshotID, err)
	}

	return documentProvenance(t, snapshot.Data)
}

// checkSavedBy returns an error naming every snapshot whose document does not
// name the user and the time, in whole seconds, that its manifest records.
func checkSavedBy(t *testing.T, h *testHarness, meta *DraftMetadata) error {
	t.Helper()

	var problems []error

	for _, manifest := range meta.History {
		got := snapshotProvenance(t, h, meta.ID, manifest.ID)
		want := [2]string{manifest.CreatedBy, builder.FormatTime(manifest.CreatedAt)}

		if saved := [2]string{got.UpdatedBy, got.UpdatedAt}; saved != want {
			problems = append(problems, fmt.Errorf("snapshot %s updatedBy and updatedAt = %q, want %q as its manifest records",
				manifest.ID, saved, want))
		}
	}

	return errors.Join(problems...)
}

// documentCreation returns the creator and creation time a draft records of
// its document.
func documentCreation(meta *DraftMetadata) [2]string {
	return [2]string{meta.DocumentCreatedBy, meta.DocumentCreatedAt}
}

// TestCreateDraftKeepsTheCreatorADocumentNames stamps a new draft: the document's
// creator and creation time are kept, each on its own, and its last edit never is.
func TestCreateDraftKeepsTheCreatorADocumentNames(t *testing.T) {
	const (
		elsewhere = "2019-05-06T07:08:09Z"
		forged    = "2031-01-01T00:00:00Z"
	)

	for _, test := range []struct {
		name  string
		claim builder.Provenance
		want  builder.Provenance
	}{
		{
			name:  "nothing",
			claim: builder.Provenance{CreatedBy: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
			want:  stampAt(testActor, 1, testActor, 1),
		},
		{
			name:  "all four",
			claim: builder.Provenance{CreatedBy: "carol", CreatedAt: elsewhere, UpdatedBy: "mallory", UpdatedAt: forged},
			want:  builder.Provenance{CreatedBy: "carol", CreatedAt: elsewhere, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z"},
		},
		{
			name:  "a creator and no creation time",
			claim: builder.Provenance{CreatedBy: "carol", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
			want:  stampAt("carol", 1, testActor, 1),
		},
		{
			name:  "a creation time and no creator",
			claim: builder.Provenance{CreatedBy: "", CreatedAt: elsewhere, UpdatedBy: "", UpdatedAt: ""},
			want:  builder.Provenance{CreatedBy: testActor, CreatedAt: elsewhere, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z"},
		},
		{
			name:  "a last edit only",
			claim: builder.Provenance{CreatedBy: "", CreatedAt: "", UpdatedBy: "mallory", UpdatedAt: forged},
			want:  stampAt(testActor, 1, testActor, 1),
		},
		{
			// The longest creator a document may name is one the draft record
			// can hold too, so the draft stays readable.
			name:  "the longest creator",
			claim: builder.Provenance{CreatedBy: strings.Repeat("é", builder.MaxUserBytes/2), CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
			want:  stampAt(strings.Repeat("é", builder.MaxUserBytes/2), 1, testActor, 1),
		},
		{
			name:  "a name no phenix user has",
			claim: builder.Provenance{CreatedBy: "Carol <carol@example.com>", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
			want:  stampAt("Carol <carol@example.com>", 1, testActor, 1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			meta, err := h.service.CreateDraft(context.Background(), CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
				Forked: nil, Origin: nil, Document: claimedDocument(t, "topo", test.claim), Summary: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			if diff := cmp.Diff(&test.want, meta.Stamp); diff != "" {
				t.Errorf("CreateDraft stamp mismatch (-want +got):\n%s", diff)
			}

			if got := h.now.Load(); got != 1 {
				t.Errorf("CreateDraft read the clock %d times, want once", got)
			}

			// The draft, its update and its snapshot share one time.
			times := []time.Time{meta.Created, meta.Updated, meta.History[0].CreatedAt}
			if diff := cmp.Diff([]time.Time{memrecord.Time(1), memrecord.Time(1), memrecord.Time(1)}, times); diff != "" {
				t.Errorf("CreateDraft created, updated and snapshot times mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(test.want, snapshotProvenance(t, h, meta.ID, meta.History[0].ID)); diff != "" {
				t.Errorf("provenance of the stored document mismatch (-want +got):\n%s", diff)
			}

			if got, want := documentCreation(meta), [2]string{test.want.CreatedBy, test.want.CreatedAt}; got != want {
				t.Errorf("CreateDraft document creator and creation time = %q, want %q as the document was stored", got, want)
			}

			if err := checkSavedBy(t, h, meta); err != nil {
				t.Errorf("after CreateDraft:\n%v", err)
			}

			// A read writes nothing, so it returns no stamp.
			stored, err := h.service.GetDraft(context.Background(), meta.ID)
			if err != nil {
				t.Fatalf("GetDraft(%s) returned error: %s", meta.ID, fmtErr(err))
			}

			if stored.Stamp != nil {
				t.Errorf("GetDraft(%s) stamp = %+v, want none", meta.ID, *stored.Stamp)
			}

			if stored.DocumentCreatedBy != test.want.CreatedBy {
				t.Errorf("GetDraft(%s) document creator = %q, want %q", meta.ID, stored.DocumentCreatedBy, test.want.CreatedBy)
			}
		})
	}
}

// TestAppendSnapshotStampsTheDocument covers a save by another user: the creator
// is the draft's and the last edit the saver's, whatever the request says.
func TestAppendSnapshotStampsTheDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	first := *meta.Stamp
	ticks := h.now.Load()

	forged := builder.Provenance{
		CreatedBy: "mallory", CreatedAt: "1999-01-01T00:00:00Z", UpdatedBy: testActor, UpdatedAt: "2031-01-01T00:00:00Z",
	}

	updated, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testPeer, ExpectedRevision: meta.Revision,
		Document: claimedDocument(t, "topo-v2", forged), Summary: "v2", OpID: "",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot returned error: %v", err)
	}

	if got := h.now.Load() - ticks; got != 1 {
		t.Errorf("AppendSnapshot read the clock %d times, want once", got)
	}

	want := stampAt(testActor, 1, testPeer, 2)

	if diff := cmp.Diff(&want, updated.Stamp); diff != "" {
		t.Errorf("AppendSnapshot stamp mismatch (-want +got):\n%s", diff)
	}

	// A save changes neither the creator nor the creation time.
	if got, want := documentCreation(updated), [2]string{testActor, first.CreatedAt}; got != want {
		t.Errorf("AppendSnapshot document creator and creation time = %q, want %q", got, want)
	}

	if !updated.Updated.Equal(updated.Current().CreatedAt) {
		t.Errorf("AppendSnapshot updated the draft at %s and stored its snapshot at %s; want one time",
			updated.Updated, updated.Current().CreatedAt)
	}

	if diff := cmp.Diff(want, snapshotProvenance(t, h, meta.ID, updated.Current().ID)); diff != "" {
		t.Errorf("provenance of the saved document mismatch (-want +got):\n%s", diff)
	}

	// The first snapshot is as it was stored.
	if diff := cmp.Diff(first, snapshotProvenance(t, h, meta.ID, updated.History[0].ID)); diff != "" {
		t.Errorf("provenance of the first document mismatch (-want +got):\n%s", diff)
	}

	// The owner saves again: the creator stays, the last editor changes back.
	updated = appendTestSnapshot(t, h, updated, "topo-v3", testActor)

	want = stampAt(testActor, 1, testActor, 3)
	if diff := cmp.Diff(&want, updated.Stamp); diff != "" {
		t.Errorf("AppendSnapshot(topo-v3) stamp mismatch (-want +got):\n%s", diff)
	}

	if err := checkSavedBy(t, h, updated); err != nil {
		t.Errorf("after AppendSnapshot(topo-v3):\n%v", err)
	}
}

// TestCursorDeleteAndPublishStampNoDocument checks that undo, redo, snapshot
// delete and publication change no digest.
func TestCursorDeleteAndPublishStampNoDocument(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "v1")
	meta = appendTestSnapshot(t, h, meta, "v2", testPeer)
	meta = appendTestSnapshot(t, h, meta, "v3", testActor)

	digests := func(meta *DraftMetadata) map[string]string {
		out := make(map[string]string, len(meta.History))
		for _, manifest := range meta.History {
			out[manifest.ID] = manifest.Digest
		}

		return out
	}

	before := digests(meta)
	second := snapshotProvenance(t, h, meta.ID, meta.History[1].ID)

	// unchanged returns an error naming what a step that stored no document
	// stamped or changed.
	unchanged := func(after *DraftMetadata) error {
		var problems []error

		if after.Stamp != nil {
			problems = append(problems, fmt.Errorf("stamp = %+v, want none: no document was stored", *after.Stamp))
		}

		for id, digest := range digests(after) {
			if before[id] != digest {
				problems = append(problems, fmt.Errorf("digest of snapshot %s = %s, want %s", id, digest, before[id]))
			}
		}

		return errors.Join(problems...)
	}

	ticks := h.now.Load()

	// Undo, as another user, later.
	meta, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 1, SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	if err := unchanged(meta); err != nil {
		t.Errorf("after MoveCursor:\n%v", err)
	}

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	// After an undo the document holds the stamp of the save that made it.
	got := documentProvenance(t, current.Data)
	if diff := cmp.Diff(second, got); diff != "" {
		t.Errorf("provenance of the current document after an undo mismatch (-want +got):\n%s", diff)
	}

	if got.UpdatedBy != testPeer {
		t.Errorf("current document after an undo updatedBy = %q, want %q", got.UpdatedBy, testPeer)
	}

	// The draft itself was touched by the undo, which is another fact.
	if meta.LastModifiedBy != testActor {
		t.Errorf("MoveCursor last modified by = %q, want %q", meta.LastModifiedBy, testActor)
	}

	if !meta.Updated.After(current.Manifest.CreatedAt) {
		t.Errorf("MoveCursor updated = %s, want after the snapshot %s", meta.Updated, current.Manifest.CreatedAt)
	}

	meta, err = h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	if err := unchanged(meta); err != nil {
		t.Errorf("after MarkPublished:\n%v", err)
	}

	if got, want := meta.Publication.Digest, before[meta.Current().ID]; got != want {
		t.Errorf("MarkPublished publication digest = %s, want the published snapshot's %s", got, want)
	}

	meta = deleteTestSnapshot(t, h, meta, meta.History[2].ID, testPeer)

	if err := unchanged(meta); err != nil {
		t.Errorf("after DeleteSnapshot:\n%v", err)
	}

	// Each of the three read the clock once.
	if got := h.now.Load() - ticks; got != 3 {
		t.Errorf("an undo, a publication and a snapshot delete read the clock %d times, want 3", got)
	}

	if diff := cmp.Diff(second, snapshotProvenance(t, h, meta.ID, meta.Current().ID)); diff != "" {
		t.Errorf("provenance of the current document mismatch (-want +got):\n%s", diff)
	}
}

// originOf returns the origin a caller that read data itself gives.
func originOf(data []byte) *DocumentOrigin {
	return &DocumentOrigin{Digest: digestOf(data)}
}

// TestCreateDraftKeepsAnUnchangedCopy stores an unchanged copy of an origin as it
// is, under the origin's digest.
func TestCreateDraftKeepsAnUnchangedCopy(t *testing.T) {
	published := builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2025-03-04T05:06:07Z", UpdatedBy: "dave", UpdatedAt: "2025-06-07T08:09:10Z",
	}

	for _, test := range []struct {
		name   string
		origin builder.Provenance
	}{
		{name: "an origin with all four", origin: published},
		{name: "an origin with no creator", origin: builder.Provenance{CreatedBy: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""}},
		{
			name:   "an origin with a last edit only",
			origin: builder.Provenance{CreatedBy: "", CreatedAt: "", UpdatedBy: "dave", UpdatedAt: "2025-06-07T08:09:10Z"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			origin := claimedDocument(t, "topo", test.origin)

			// The same document in another encoding, as an editor sends it back.
			body := mutateDocument(t, origin, func(map[string]any) {})
			if bytes.Equal(body, origin) {
				t.Fatal("the test needs another encoding of the origin")
			}

			meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "builder-doc/abc", SourceFile: "",
				Forked: nil, Origin: originOf(origin), Document: body, Summary: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
			if err != nil {
				t.Fatalf("GetCurrentDocument returned error: %v", err)
			}

			// The draft holds the exact bytes of the origin.
			if diff := cmp.Diff(string(origin), string(snapshot.Data)); diff != "" {
				t.Errorf("GetCurrentDocument data mismatch (-want +got):\n%s", diff)
			}

			if got, want := meta.History[0].Digest, digestOf(origin); got != want {
				t.Errorf("CreateDraft snapshot digest = %s, want the origin's %s", got, want)
			}

			if diff := cmp.Diff(&test.origin, meta.Stamp); diff != "" {
				t.Errorf("CreateDraft stamp mismatch (-want +got):\n%s", diff)
			}

			if got, want := documentCreation(meta), [2]string{test.origin.CreatedBy, test.origin.CreatedAt}; got != want {
				t.Errorf("CreateDraft document creator and creation time = %q, want the origin's %q", got, want)
			}

			if got, want := [2]string{meta.History[0].CreatedBy, meta.CreatedBy}, [2]string{testActor, testActor}; got != want {
				t.Errorf("CreateDraft snapshot and draft creators = %q, want %q", got, want)
			}

			// A save then is the caller's edit of the origin's document: the
			// origin's creator stays, also when it has none.
			meta = appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

			want := builder.Provenance{
				CreatedBy: test.origin.CreatedBy,
				CreatedAt: test.origin.CreatedAt,
				UpdatedBy: testPeer,
				UpdatedAt: builder.FormatTime(meta.Current().CreatedAt),
			}

			if diff := cmp.Diff(want, snapshotProvenance(t, h, meta.ID, meta.Current().ID)); diff != "" {
				t.Errorf("provenance of the saved document mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(&want, meta.Stamp); diff != "" {
				t.Errorf("AppendSnapshot(topo-v2) stamp mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCreateDraftStampsAChangedCopy stamps a copy of an origin the caller changed
// like any other body.
func TestCreateDraftStampsAChangedCopy(t *testing.T) {
	published := builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2025-03-04T05:06:07Z", UpdatedBy: "dave", UpdatedAt: "2025-06-07T08:09:10Z",
	}

	origin := claimedDocument(t, "topo", published)

	for _, test := range []struct {
		name string
		body []byte
		want builder.Provenance
	}{
		{
			name: "changed content",
			body: mutateDocument(t, origin, func(doc map[string]any) { metadataOf(t, doc)["description"] = "edited" }),
			want: builder.Provenance{
				CreatedBy: "carol", CreatedAt: published.CreatedAt, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
			},
		},
		{
			name: "changed notes",
			body: mutateDocument(t, origin, func(doc map[string]any) { metadataOf(t, doc)["notes"] = []any{"edited"} }),
			want: builder.Provenance{
				CreatedBy: "carol", CreatedAt: published.CreatedAt, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
			},
		},
		{
			name: "a forged last editor",
			body: mutateDocument(t, origin, func(doc map[string]any) { metadataOf(t, doc)["updatedBy"] = "mallory" }),
			want: builder.Provenance{
				CreatedBy: "carol", CreatedAt: published.CreatedAt, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
			},
		},
		{
			name: "another creator",
			body: mutateDocument(t, origin, func(doc map[string]any) { metadataOf(t, doc)["createdBy"] = "mallory" }),
			want: builder.Provenance{
				CreatedBy: "mallory", CreatedAt: published.CreatedAt, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
			},
		},
		{
			name: "the creator removed",
			body: mutateDocument(t, origin, func(doc map[string]any) { delete(metadataOf(t, doc), "createdBy") }),
			want: builder.Provenance{
				CreatedBy: testActor, CreatedAt: published.CreatedAt, UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)

			meta, err := h.service.CreateDraft(context.Background(), CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "builder-doc/abc", SourceFile: "",
				Forked: nil, Origin: originOf(origin), Document: test.body, Summary: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			if meta.History[0].Digest == digestOf(origin) {
				t.Errorf("CreateDraft snapshot digest = %s, the origin's; want a digest of its own", meta.History[0].Digest)
			}

			if diff := cmp.Diff(test.want, snapshotProvenance(t, h, meta.ID, meta.History[0].ID)); diff != "" {
				t.Errorf("provenance of the stored document mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(&test.want, meta.Stamp); diff != "" {
				t.Errorf("CreateDraft stamp mismatch (-want +got):\n%s", diff)
			}

			if err := checkSavedBy(t, h, meta); err != nil {
				t.Errorf("after CreateDraft:\n%v", err)
			}
		})
	}
}

// TestCreateDraftWithoutAnOriginStampsAnEqualDocument checks that only an origin
// spares a document its stamp: the same bytes without one are stamped.
func TestCreateDraftWithoutAnOriginStampsAnEqualDocument(t *testing.T) {
	h := newHarness(t)

	origin := claimedDocument(t, "topo", builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2025-03-04T05:06:07Z", UpdatedBy: "dave", UpdatedAt: "2025-06-07T08:09:10Z",
	})

	meta, err := h.service.CreateDraft(context.Background(), CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: nil, Document: origin, Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	want := builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2025-03-04T05:06:07Z", UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
	}

	if diff := cmp.Diff(&want, meta.Stamp); diff != "" {
		t.Errorf("CreateDraft stamp mismatch (-want +got):\n%s", diff)
	}

	if meta.History[0].Digest == digestOf(origin) {
		t.Errorf("CreateDraft snapshot digest = %s, the origin's; want a digest of its own", meta.History[0].Digest)
	}

	// An origin that is not a digest is a caller's mistake, never ignored.
	_, err = h.service.CreateDraft(context.Background(), CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: &DocumentOrigin{Digest: "abc"}, Document: origin, Summary: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateDraft with the origin digest abc error = %s, want ErrInvalid", fmtErr(err))
	}
}

// TestMalformedProvenanceRefusesTheRequest checks that an invalid value is
// refused even when the service would replace it.
func TestMalformedProvenanceRefusesTheRequest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	for name, claim := range map[string]builder.Provenance{
		"createdBy with a control character": {CreatedBy: "al\x07ice", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
		"createdBy too long": {
			CreatedBy: strings.Repeat("a", builder.MaxUserBytes+1), CreatedAt: "", UpdatedBy: "", UpdatedAt: "",
		},
		"createdAt with an offset": {CreatedBy: "", CreatedAt: "2026-10-01T15:04:05+00:00", UpdatedBy: "", UpdatedAt: ""},
		"updatedBy too long": {
			CreatedBy: "", CreatedAt: "", UpdatedBy: strings.Repeat("b", builder.MaxUserBytes+1), UpdatedAt: "",
		},
		"updatedAt that is no time": {CreatedBy: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: "yesterday"},
	} {
		t.Run(name, func(t *testing.T) {
			document := claimedDocument(t, "topo-v2", claim)

			_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
				Forked: nil, Origin: nil, Document: document, Summary: "",
			})
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("CreateDraft error = %s, want ErrInvalid", fmtErr(err))
			}

			_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: document, Summary: "", OpID: "",
			})
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("AppendSnapshot error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	// Nothing was stored.
	if stored, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) returned error: %s", meta.ID, fmtErr(err))
	} else if stored.Revision != meta.Revision {
		t.Errorf("GetDraft(%s) revision = %d, want %d", meta.ID, stored.Revision, meta.Revision)
	}

	if got := h.store.Count(NamespaceDrafts); got != 1 {
		t.Errorf("%d drafts are stored, want 1", got)
	}
}

// documentOfSize returns a test document whose canonical encoding is exactly
// size bytes long, with the given provenance.
func documentOfSize(t *testing.T, name string, size int, provenance builder.Provenance) []byte {
	t.Helper()

	base := stampedDocument(t, testDocument(t, name, 0), provenance)
	data := stampedDocument(t, testDocument(t, name, size-len(base)), provenance)

	if len(data) != size {
		t.Fatalf("the document is %d bytes, want %d", len(data), size)
	}

	return data
}

// TestStampCountsTowardsTheDocumentLimit refuses a document that the stamp pushes
// over the size limit, and stores nothing.
func TestStampCountsTowardsTheDocumentLimit(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	var none builder.Provenance

	full := documentOfSize(t, "full", MaxDocumentBytes, none)

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: nil, Document: full, Summary: "",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("CreateDraft of a document the stamp pushes over the limit = %s, want ErrTooLarge", fmtErr(err))
	}

	if got := [2]int{h.store.Count(NamespaceDrafts), h.store.Count(NamespaceContent)}; got != [2]int{} {
		t.Errorf("drafts and content records stored = %d, want none", got)
	}

	// The same document is stored when it is the unchanged copy of an
	// origin, which is not stamped.
	copied, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "builder-doc/abc", SourceFile: "",
		Forked: nil, Origin: originOf(full), Document: full, Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft of an unchanged copy at the limit returned error: %s", fmtErr(err))
	}

	if got := copied.History[0].Size; got != MaxDocumentBytes {
		t.Errorf("CreateDraft of an unchanged copy at the limit stored %d bytes, want %d", got, MaxDocumentBytes)
	}

	// A save of it is stamped, and so refused: the draft is left as it was.
	content := h.store.Count(NamespaceContent)

	_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: copied.ID, Actor: testActor, ExpectedRevision: copied.Revision,
		Document: full, Summary: "", OpID: "",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("AppendSnapshot of a document the stamp pushes over the limit = %s, want ErrTooLarge", fmtErr(err))
	}

	if stored, err := h.service.GetDraft(ctx, copied.ID); err != nil {
		t.Errorf("GetDraft(%s) returned error: %s", copied.ID, fmtErr(err))
	} else if stored.Revision != copied.Revision {
		t.Errorf("GetDraft(%s) revision = %d, want the draft untouched at %d", copied.ID, stored.Revision, copied.Revision)
	}

	if got := h.store.Count(NamespaceContent); got != content {
		t.Errorf("%d content records are stored, want the %d there were", got, content)
	}

	// One that leaves room for exactly the stamp is stored at the limit.
	stamp := stampAt(testActor, 0, testActor, 0)
	room := len(stampedDocument(t, testDocument(t, "room", 0), stamp)) - len(testDocument(t, "room", 0))
	fits := documentOfSize(t, "room", MaxDocumentBytes-room, none)

	created, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: nil, Document: fits, Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft of a document the stamp fills the limit with returned error: %s", fmtErr(err))
	}

	if got := created.History[0].Size; got != MaxDocumentBytes {
		t.Errorf("CreateDraft of a document the stamp fills the limit with stored %d bytes, want %d", got, MaxDocumentBytes)
	}
}

// TestPutPublishedDocumentNeverStamps checks that publishing stores the exact
// bytes of a snapshot or a file, whatever their provenance says.
func TestPutPublishedDocumentNeverStamps(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	meta = appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	ticks := h.now.Load()

	for name, data := range map[string][]byte{
		"a snapshot":                   snapshot.Data,
		"a document that names nobody": testDocument(t, "plain", 0),
		"a document from elsewhere": claimedDocument(t, "elsewhere", builder.Provenance{
			CreatedBy: "carol", CreatedAt: "2019-05-06T07:08:09Z", UpdatedBy: "dave", UpdatedAt: "2031-01-01T00:00:00Z",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			published, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
				Target: "topo", Kind: "Topology", Actor: "erin", Document: data, DraftID: "", SnapshotID: "",
			})
			if err != nil {
				t.Fatalf("PutPublishedDocument returned error: %v", err)
			}

			_, stored, err := h.service.GetPublishedDocumentData(ctx, published.ID)
			if err != nil {
				t.Fatalf("GetPublishedDocumentData(%s) returned error: %v", published.ID, err)
			}

			// The published document is the exact bytes it was published from.
			if diff := cmp.Diff(string(data), string(stored)); diff != "" {
				t.Errorf("GetPublishedDocumentData(%s) mismatch (-want +got):\n%s", published.ID, diff)
			}

			if got, want := published.Digest, digestOf(data); got != want {
				t.Errorf("PutPublishedDocument digest = %s, want %s", got, want)
			}
		})
	}

	if published := h.now.Load() - ticks; published == 0 {
		t.Error("the test published nothing")
	}

	// The draft is as it was: publishing wrote no snapshot.
	if stored, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) returned error: %s", meta.ID, fmtErr(err))
	} else if stored.Revision != meta.Revision {
		t.Errorf("GetDraft(%s) revision = %d, want the draft untouched at %d", meta.ID, stored.Revision, meta.Revision)
	}
}

// TestCloneCopiesTheStamp checks that a copy of the metadata does not share
// its stamp.
func TestCloneCopiesTheStamp(t *testing.T) {
	meta := createTestDraft(t, newHarness(t), "topo")

	clone := meta.Clone()
	clone.Stamp.CreatedBy = "changed"

	if meta.Stamp.CreatedBy != testActor {
		t.Errorf("after changing the stamp of a Clone, the original stamp creator = %q, want %q",
			meta.Stamp.CreatedBy, testActor)
	}
}

func TestValidateSourceFile(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "none", value: "", valid: true},
		{name: "a file name", value: "pump-station.builder.json", valid: true},
		{name: "spaces and other scripts", value: "Wasserwerk Süd (final) v2.yaml", valid: true},
		{name: "a hidden file", value: ".builder.json", valid: true},
		{name: "dots inside", value: "a..b.json", valid: true},
		{name: "three dots", value: "...", valid: true},
		{name: "the longest", value: strings.Repeat("f", MaxSourceFileLength), valid: true},
		{name: "one byte too long", value: strings.Repeat("f", MaxSourceFileLength+1), valid: false},
		{name: "256 bytes in fewer characters", value: strings.Repeat("é", 128), valid: false},
		{name: "a slash", value: "dir/file.json", valid: false},
		{name: "an absolute path", value: "/etc/passwd", valid: false},
		{name: "a trailing slash", value: "file.json/", valid: false},
		{name: "a backslash", value: `C:\Users\alice\file.json`, valid: false},
		{name: "a lone backslash", value: `\`, valid: false},
		{name: "the directory itself", value: ".", valid: false},
		{name: "the parent directory", value: "..", valid: false},
		{name: "a parent path", value: "../file.json", valid: false},
		{name: "a newline", value: "file\n.json", valid: false},
		{name: "a NUL", value: "file\x00.json", valid: false},
		{name: "a tab", value: "file\t.json", valid: false},
		{name: "a delete character", value: "file\x7f.json", valid: false},
		{name: "invalid UTF-8", value: "file\xff.json", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSourceFile(test.value)

			switch {
			case test.valid && err != nil:
				t.Errorf("ValidateSourceFile(%q) = %s, want nil", test.value, fmtErr(err))
			case !test.valid && !errors.Is(err, ErrInvalid):
				t.Errorf("ValidateSourceFile(%q) = %s, want ErrInvalid", test.value, fmtErr(err))
			}
		})
	}
}

// TestCreateDraftRecordsTheSourceFile keeps the uploaded file name through every
// mutation, and refuses an invalid one before storing anything.
func TestCreateDraftRecordsTheSourceFile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	const name = "Pump station (rev 2).builder.yaml"

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: name,
		Forked: nil, Origin: nil, Document: testDocument(t, "topo", 0), Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %s", fmtErr(err))
	}

	if meta.SourceFile != name {
		t.Errorf("CreateDraft source file = %q, want %q", meta.SourceFile, name)
	}

	meta = appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	meta, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 0, SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %s", fmtErr(err))
	}

	if meta.SourceFile != name {
		t.Errorf("MoveCursor source file = %q, want %q", meta.SourceFile, name)
	}

	if stored, err := h.service.GetDraft(ctx, meta.ID); err != nil {
		t.Errorf("GetDraft(%s) returned error: %s", meta.ID, fmtErr(err))
	} else if stored.SourceFile != name {
		t.Errorf("GetDraft(%s) source file = %q, want %q", meta.ID, stored.SourceFile, name)
	}

	drafts, damaged, err := h.service.ListDraftsWithDamaged(ctx)
	switch {
	case err != nil:
		t.Errorf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
	case len(damaged) != 0 || len(drafts) != 1:
		t.Errorf("ListDraftsWithDamaged = %d drafts and %+v damaged, want 1 draft and none damaged", len(drafts), damaged)
	case drafts[0].SourceFile != name:
		t.Errorf("ListDraftsWithDamaged source file = %q, want %q", drafts[0].SourceFile, name)
	}

	// A draft made without one records none.
	if plain := createTestDraft(t, h, "plain"); plain.SourceFile != "" {
		t.Errorf("CreateDraft without a source file recorded %q, want none", plain.SourceFile)
	}

	for _, invalid := range []string{
		"dir/file.json", `dir\file.json`, "..", ".", "file\x1f.json", strings.Repeat("f", MaxSourceFileLength+1),
	} {
		_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
			Actor: testActor, Title: "", SourceToken: "", SourceFile: invalid,
			Forked: nil, Origin: nil, Document: testDocument(t, "refused", 0), Summary: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateDraft with source file %q = %s, want ErrInvalid", invalid, fmtErr(err))
		}
	}

	if got := h.store.Count(NamespaceDrafts); got != 2 {
		t.Errorf("%d drafts are stored, want the 2 that were accepted", got)
	}
}
