package builder

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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

// assertSavedBy checks every snapshot's document names the user and the time,
// in whole seconds, that its manifest records.
func assertSavedBy(t *testing.T, h *testHarness, meta *DraftMetadata) {
	t.Helper()

	for _, manifest := range meta.History {
		got := snapshotProvenance(t, h, meta.ID, manifest.ID)

		if got.UpdatedBy != manifest.CreatedBy || got.UpdatedAt != builder.FormatTime(manifest.CreatedAt) {
			t.Fatalf("snapshot %s holds updatedBy %q at %q, but was stored by %q at %q",
				manifest.ID, got.UpdatedBy, got.UpdatedAt, manifest.CreatedBy, builder.FormatTime(manifest.CreatedAt))
		}
	}
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
				Forked: nil, Origin: nil, Document: claimedDocument(t, "topo", test.claim), Summary: "", ID: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			switch {
			case *meta.Stamp != test.want:
				t.Fatalf("stamp = %+v, want %+v", *meta.Stamp, test.want)
			case h.now.Load() != 1:
				t.Fatalf("CreateDraft read the clock %d times, want once", h.now.Load())
			case !meta.Created.Equal(memrecord.Time(1)) || !meta.Updated.Equal(memrecord.Time(1)) ||
				!meta.History[0].CreatedAt.Equal(memrecord.Time(1)):
				t.Fatalf("created %s, updated %s, snapshot %s; want one time for all three",
					meta.Created, meta.Updated, meta.History[0].CreatedAt)
			}

			if got := snapshotProvenance(t, h, meta.ID, meta.History[0].ID); got != test.want {
				t.Fatalf("the stored document holds %+v, want %+v", got, test.want)
			}

			if meta.DocumentCreatedBy != test.want.CreatedBy || meta.DocumentCreatedAt != test.want.CreatedAt {
				t.Fatalf("the draft records %q at %q, want what the document was stored with",
					meta.DocumentCreatedBy, meta.DocumentCreatedAt)
			}

			assertSavedBy(t, h, meta)

			// A read writes nothing, so it returns no stamp.
			stored, err := h.service.GetDraft(context.Background(), meta.ID)
			if err != nil || stored.Stamp != nil || stored.DocumentCreatedBy != test.want.CreatedBy {
				t.Fatalf("GetDraft = %+v, %s; want no stamp and the recorded creator", stored, fmtErr(err))
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
		t.Fatalf("AppendSnapshot read the clock %d times, want once", got)
	}

	want := stampAt(testActor, 1, testPeer, 2)

	switch {
	case updated.Stamp == nil || *updated.Stamp != want:
		t.Fatalf("stamp = %+v, want %+v", updated.Stamp, want)
	case updated.DocumentCreatedBy != testActor || updated.DocumentCreatedAt != first.CreatedAt:
		t.Fatalf("the draft now records %q at %q; a save must not change either",
			updated.DocumentCreatedBy, updated.DocumentCreatedAt)
	case !updated.Updated.Equal(updated.Current().CreatedAt):
		t.Fatalf("the draft was updated at %s and its snapshot stored at %s; want one time",
			updated.Updated, updated.Current().CreatedAt)
	}

	if got := snapshotProvenance(t, h, meta.ID, updated.Current().ID); got != want {
		t.Fatalf("the saved document holds %+v, want %+v", got, want)
	}

	// The first snapshot is as it was stored.
	if got := snapshotProvenance(t, h, meta.ID, updated.History[0].ID); got != first {
		t.Fatalf("the first document now holds %+v, want %+v", got, first)
	}

	// The owner saves again: the creator stays, the last editor changes back.
	updated = appendTestSnapshot(t, h, updated, "topo-v3", testActor)

	if want := stampAt(testActor, 1, testActor, 3); *updated.Stamp != want {
		t.Fatalf("stamp = %+v, want %+v", *updated.Stamp, want)
	}

	assertSavedBy(t, h, updated)
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

	unchanged := func(step string, after *DraftMetadata) {
		t.Helper()

		if after.Stamp != nil {
			t.Fatalf("%s returned stamp %+v; it stored no document", step, *after.Stamp)
		}

		for id, digest := range digests(after) {
			if before[id] != digest {
				t.Fatalf("%s changed the digest of snapshot %s", step, id)
			}
		}
	}

	ticks := h.now.Load()

	// Undo, as another user, later.
	meta, err := h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 1, SnapshotID: "", UseIndex: true,
	})
	if err != nil {
		t.Fatalf("MoveCursor returned error: %v", err)
	}

	unchanged("MoveCursor", meta)

	current, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if got := documentProvenance(t, current.Data); got != second || got.UpdatedBy != testPeer {
		t.Fatalf("after an undo the document holds %+v, want the stamp of the save that made it %+v", got, second)
	}

	// The draft itself was touched by the undo, which is another fact.
	if meta.LastModifiedBy != testActor || !meta.Updated.After(current.Manifest.CreatedAt) {
		t.Fatalf("the draft was last modified by %q at %s, want the undo", meta.LastModifiedBy, meta.Updated)
	}

	meta, err = h.service.MarkPublished(ctx, markPublishedRequest(meta, meta.Revision))
	if err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	unchanged("MarkPublished", meta)

	if meta.Publication.Digest != before[meta.Current().ID] {
		t.Fatal("the publication does not record the digest of the snapshot it published")
	}

	meta = deleteTestSnapshot(t, h, meta, meta.History[2].ID, testPeer)

	unchanged("DeleteSnapshot", meta)

	// Each of the three read the clock once.
	if got := h.now.Load() - ticks; got != 3 {
		t.Fatalf("an undo, a publication and a snapshot delete read the clock %d times, want 3", got)
	}

	if got := snapshotProvenance(t, h, meta.ID, meta.Current().ID); got != second {
		t.Fatalf("the current document holds %+v, want %+v", got, second)
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
				Forked: nil, Origin: originOf(origin), Document: body, Summary: "", ID: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
			if err != nil {
				t.Fatalf("GetCurrentDocument returned error: %v", err)
			}

			switch {
			case !bytes.Equal(snapshot.Data, origin):
				t.Fatalf("the draft does not hold the origin's bytes:\n%s", snapshot.Data)
			case meta.History[0].Digest != digestOf(origin):
				t.Fatalf("digest = %s, want the origin's %s", meta.History[0].Digest, digestOf(origin))
			case *meta.Stamp != test.origin:
				t.Fatalf("stamp = %+v, want the origin's %+v", *meta.Stamp, test.origin)
			case meta.DocumentCreatedBy != test.origin.CreatedBy || meta.DocumentCreatedAt != test.origin.CreatedAt:
				t.Fatalf("the draft records %q at %q, want the origin's %q at %q",
					meta.DocumentCreatedBy, meta.DocumentCreatedAt, test.origin.CreatedBy, test.origin.CreatedAt)
			case meta.History[0].CreatedBy != testActor || meta.CreatedBy != testActor:
				t.Fatalf("the snapshot was stored by %q in a draft created by %q", meta.History[0].CreatedBy, meta.CreatedBy)
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

			if got := snapshotProvenance(t, h, meta.ID, meta.Current().ID); got != want || *meta.Stamp != want {
				t.Fatalf("the saved document holds %+v (stamp %+v), want %+v", got, *meta.Stamp, want)
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
				Forked: nil, Origin: originOf(origin), Document: test.body, Summary: "", ID: "",
			})
			if err != nil {
				t.Fatalf("CreateDraft returned error: %v", err)
			}

			if meta.History[0].Digest == digestOf(origin) {
				t.Fatal("a changed copy was stored under the origin's digest")
			}

			if got := snapshotProvenance(t, h, meta.ID, meta.History[0].ID); got != test.want || *meta.Stamp != test.want {
				t.Fatalf("the stored document holds %+v (stamp %+v), want %+v", got, *meta.Stamp, test.want)
			}

			assertSavedBy(t, h, meta)
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
		Forked: nil, Origin: nil, Document: origin, Summary: "", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	want := builder.Provenance{
		CreatedBy: "carol", CreatedAt: "2025-03-04T05:06:07Z", UpdatedBy: testActor, UpdatedAt: "2024-01-01T00:00:01Z",
	}

	if *meta.Stamp != want || meta.History[0].Digest == digestOf(origin) {
		t.Fatalf("stamp = %+v with the origin's digest %t; want %+v and a digest of its own",
			*meta.Stamp, meta.History[0].Digest == digestOf(origin), want)
	}

	// An origin that is not a digest is a caller's mistake, never ignored.
	_, err = h.service.CreateDraft(context.Background(), CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: &DocumentOrigin{Digest: "abc"}, Document: origin, Summary: "", ID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDraft with an origin without a digest = %s, want ErrInvalid", fmtErr(err))
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
				Forked: nil, Origin: nil, Document: document, Summary: "", ID: "",
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateDraft error = %s, want ErrInvalid", fmtErr(err))
			}

			_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: document, Summary: "", OpID: "",
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("AppendSnapshot error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil || stored.Revision != meta.Revision || h.store.Count(NamespaceDrafts) != 1 {
		t.Fatalf("GetDraft = %+v, %s with %d drafts; want nothing stored",
			stored, fmtErr(err), h.store.Count(NamespaceDrafts))
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
		Forked: nil, Origin: nil, Document: full, Summary: "", ID: "",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("CreateDraft of a document the stamp pushes over the limit = %s, want ErrTooLarge", fmtErr(err))
	}

	if h.store.Count(NamespaceDrafts) != 0 || h.store.Count(NamespaceChunks) != 0 {
		t.Fatalf("%d drafts and %d chunks were stored, want none",
			h.store.Count(NamespaceDrafts), h.store.Count(NamespaceChunks))
	}

	// The same document is stored when it is the unchanged copy of an
	// origin, which is not stamped.
	copied, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "builder-doc/abc", SourceFile: "",
		Forked: nil, Origin: originOf(full), Document: full, Summary: "", ID: "",
	})
	if err != nil || copied.History[0].Size != MaxDocumentBytes {
		t.Fatalf("CreateDraft of an unchanged copy at the limit = %+v, %s", copied, fmtErr(err))
	}

	// A save of it is stamped, and so refused: the draft is left as it was.
	chunks := h.store.Count(NamespaceChunks)

	_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: copied.ID, Actor: testActor, ExpectedRevision: copied.Revision,
		Document: full, Summary: "", OpID: "",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("AppendSnapshot of a document the stamp pushes over the limit = %s, want ErrTooLarge", fmtErr(err))
	}

	stored, err := h.service.GetDraft(ctx, copied.ID)
	if err != nil || stored.Revision != copied.Revision || h.store.Count(NamespaceChunks) != chunks {
		t.Fatalf("GetDraft = %+v, %s; want the draft and its chunks untouched", stored, fmtErr(err))
	}

	// One that leaves room for exactly the stamp is stored at the limit.
	stamp := stampAt(testActor, 0, testActor, 0)
	room := len(stampedDocument(t, testDocument(t, "room", 0), stamp)) - len(testDocument(t, "room", 0))
	fits := documentOfSize(t, "room", MaxDocumentBytes-room, none)

	created, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "", SourceFile: "",
		Forked: nil, Origin: nil, Document: fits, Summary: "", ID: "",
	})
	if err != nil || created.History[0].Size != MaxDocumentBytes {
		t.Fatalf("CreateDraft of a document the stamp fills the limit with = %+v, %s", created, fmtErr(err))
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
		published, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: "topo", Kind: "Topology", Actor: "erin", Document: data, DraftID: "", SnapshotID: "",
		})
		if err != nil {
			t.Fatalf("%s: PutPublishedDocument returned error: %v", name, err)
		}

		_, stored, err := h.service.GetPublishedDocumentData(ctx, published.ID)
		if err != nil {
			t.Fatalf("%s: GetPublishedDocumentData returned error: %v", name, err)
		}

		if !bytes.Equal(stored, data) || published.Digest != digestOf(data) {
			t.Fatalf("%s: the published document is not the bytes it was published from", name)
		}
	}

	if published := h.now.Load() - ticks; published == 0 {
		t.Fatal("the test published nothing")
	}

	// The draft is as it was: publishing wrote no snapshot.
	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil || stored.Revision != meta.Revision {
		t.Fatalf("GetDraft = %+v, %s; want the draft untouched", stored, fmtErr(err))
	}
}

// TestCloneCopiesTheStamp checks that a copy of the metadata does not share
// its stamp.
func TestCloneCopiesTheStamp(t *testing.T) {
	meta := createTestDraft(t, newHarness(t), "topo")

	clone := meta.Clone()
	clone.Stamp.CreatedBy = "changed"

	if meta.Stamp.CreatedBy != testActor {
		t.Fatal("a clone shares its stamp with the metadata it was made from")
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
				t.Fatalf("ValidateSourceFile(%q) = %s, want it accepted", test.value, fmtErr(err))
			case !test.valid && !errors.Is(err, ErrInvalid):
				t.Fatalf("ValidateSourceFile(%q) = %s, want ErrInvalid", test.value, fmtErr(err))
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
		Forked: nil, Origin: nil, Document: testDocument(t, "topo", 0), Summary: "", ID: "",
	})
	if err != nil || meta.SourceFile != name {
		t.Fatalf("CreateDraft = %+v, %s; want source file %q", meta, fmtErr(err), name)
	}

	meta = appendTestSnapshot(t, h, meta, "topo-v2", testPeer)

	meta, err = h.service.MoveCursor(ctx, MoveCursorRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision, Index: 0, SnapshotID: "", UseIndex: true,
	})
	if err != nil || meta.SourceFile != name {
		t.Fatalf("MoveCursor = %+v, %s; want source file %q", meta, fmtErr(err), name)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil || stored.SourceFile != name {
		t.Fatalf("GetDraft = %+v, %s; want source file %q", stored, fmtErr(err), name)
	}

	drafts, damaged, err := h.service.ListDraftsWithDamaged(ctx)
	if err != nil || len(damaged) != 0 || len(drafts) != 1 || drafts[0].SourceFile != name {
		t.Fatalf("ListDraftsWithDamaged = %+v, %+v, %s; want source file %q", drafts, damaged, fmtErr(err), name)
	}

	// A draft made without one records none.
	plain := createTestDraft(t, h, "plain")
	if plain.SourceFile != "" {
		t.Fatalf("source file = %q, want none", plain.SourceFile)
	}

	for _, invalid := range []string{
		"dir/file.json", `dir\file.json`, "..", ".", "file\x1f.json", strings.Repeat("f", MaxSourceFileLength+1),
	} {
		_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
			Actor: testActor, Title: "", SourceToken: "", SourceFile: invalid,
			Forked: nil, Origin: nil, Document: testDocument(t, "refused", 0), Summary: "", ID: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("CreateDraft with source file %q = %s, want ErrInvalid", invalid, fmtErr(err))
		}
	}

	if got := h.store.Count(NamespaceDrafts); got != 2 {
		t.Fatalf("%d drafts are stored, want the 2 that were accepted", got)
	}
}
