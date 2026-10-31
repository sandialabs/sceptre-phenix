package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

// mutateDocument applies mutate to the document as a generic map and encodes it
// again, giving the malformed payloads an untrusted caller could send.
func mutateDocument(t *testing.T, data []byte, mutate func(map[string]any)) []byte {
	t.Helper()

	var generic map[string]any

	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("decoding document returned error: %v", err)
	}

	mutate(generic)

	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding mutated document returned error: %v", err)
	}

	return out
}

func TestServiceRejectsInvalidDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")
	valid := testDocument(t, "topo-v2", 0)

	tests := []struct {
		name     string
		document []byte
		is       error
	}{
		{
			name:     "unknown field",
			document: mutateDocument(t, valid, func(doc map[string]any) { doc["bogus"] = 1 }),
			is:       ErrInvalid,
		},
		{
			name:     "unsupported revision",
			document: mutateDocument(t, valid, func(doc map[string]any) { doc["revision"] = 2 }),
			is:       builder.ErrUnsupportedRevision,
		},
		{
			name:     "unsupported schema",
			document: mutateDocument(t, valid, func(doc map[string]any) { doc["$schema"] = "https://example.com/other" }),
			is:       builder.ErrUnsupportedSchema,
		},
		{
			name: "semantically invalid",
			document: mutateDocument(t, valid, func(doc map[string]any) {
				nodes, _ := doc["nodes"].([]any)
				node, _ := nodes[0].(map[string]any)
				delete(node, "note")
			}),
			is: ErrInvalid,
		},
		{
			name:     "trailing content",
			document: append(bytes.Clone(valid), []byte("{}")...),
			is:       ErrInvalid,
		},
		{
			name:     "empty",
			document: nil,
			is:       ErrInvalid,
		},
		{
			// The draft service records the name as the draft title, which
			// is refused rather than truncated.
			name: "name longer than a draft title",
			document: mutateDocument(t, valid, func(doc map[string]any) {
				metadata, _ := doc["metadata"].(map[string]any)
				metadata["name"] = strings.Repeat("n", MaxTitleLength+1)
			}),
			is: ErrInvalid,
		},
		{
			// The metadata fields live in the metadata object only.
			name:     "a name at the root",
			document: mutateDocument(t, valid, func(doc map[string]any) { doc["name"] = "root" }),
			is:       ErrInvalid,
		},
		{
			name: "an unknown metadata field",
			document: mutateDocument(t, valid, func(doc map[string]any) {
				metadata, _ := doc["metadata"].(map[string]any)
				metadata["author"] = testActor
			}),
			is: ErrInvalid,
		},
	}

	drafts, content := h.store.Count(NamespaceDrafts), h.store.Count(NamespaceContent)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "",
				Document: tt.document, Summary: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Errorf("CreateDraft(%s) error = %s, want %v and ErrInvalid", tt.name, fmtErr(err), tt.is)
			}

			_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: tt.document, Summary: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Errorf("AppendSnapshot(%s) error = %s, want %v and ErrInvalid", tt.name, fmtErr(err), tt.is)
			}

			_, err = h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
				Target: "topo", Kind: "Topology", Actor: testActor,
				Document: tt.document, DraftID: "", SnapshotID: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Errorf("PutPublishedDocument(%s) error = %s, want %v and ErrInvalid", tt.name, fmtErr(err), tt.is)
			}
		})
	}

	// Nothing invalid reached the store.
	if got := h.store.Count(NamespacePublished); got != 0 {
		t.Errorf("published records after invalid documents = %d, want 0", got)
	}

	if got := h.store.Count(NamespaceDrafts); got != drafts {
		t.Errorf("draft records after invalid documents = %d, want %d", got, drafts)
	}

	if got := h.store.Count(NamespaceContent); got != content {
		t.Errorf("content records after invalid documents = %d, want %d", got, content)
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	// Only a create or append call returns a stamp.
	if diff := cmp.Diff(meta, stored, cmpopts.IgnoreFields(DraftMetadata{}, "Stamp")); diff != "" {
		t.Errorf("GetDraft after invalid documents mismatch with the untouched draft (-want +got):\n%s", diff)
	}
}

func TestDocumentsAreCanonicalizedBeforeStorage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	canonical := testDocument(t, "topo", 0)

	// The same document, compactly encoded: valid, but not the canonical form.
	compact := mutateDocument(t, canonical, func(map[string]any) {})

	if bytes.Equal(compact, canonical) {
		t.Fatal("the test needs a non-canonical encoding of the same document")
	}

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "",
		Document: compact, Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	// What is stored is the canonical encoding of the document with the stamp
	// the service wrote, whatever encoding the caller sent.
	stamped := stampedDocument(t, canonical, *meta.Stamp)

	if want := digestOf(stamped); meta.History[0].Digest != want {
		t.Errorf("CreateDraft(compact document) digest = %s, want the canonical encoding digest %s", meta.History[0].Digest, want)
	}

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if diff := cmp.Diff(string(stamped), string(snapshot.Data)); diff != "" {
		t.Errorf("GetCurrentDocument(draft) mismatch with the canonical encoding (-want +got):\n%s", diff)
	}

	// Decode hands over the document the service parsed once, then decodes
	// again, so each caller gets a document of its own.
	first, err := snapshot.Decode()
	if err != nil {
		t.Fatalf("first Decode returned error: %v", err)
	}

	second, err := snapshot.Decode()
	if err != nil {
		t.Fatalf("second Decode returned error: %v", err)
	}

	if first == second {
		t.Error("Snapshot.Decode returned the same document twice, want a document of its own for each call")
	}

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("second Snapshot.Decode mismatch with the first (-want +got):\n%s", diff)
	}

	// Publishing canonicalizes too, so the same content is content addressed the
	// same way however it was encoded.
	fromCompact, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: compact, DraftID: "", SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	fromCanonical, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: canonical, DraftID: "", SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	if fromCompact.ID != fromCanonical.ID {
		t.Errorf("PutPublishedDocument(compact).ID = %s, want %s of the canonical encoding", fromCompact.ID, fromCanonical.ID)
	}

	if want := digestOf(canonical); fromCompact.Digest != want {
		t.Errorf("PutPublishedDocument(compact).Digest = %s, want the canonical encoding digest %s", fromCompact.Digest, want)
	}
}

func TestDraftTitleFollowsDocumentName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "ignored when the document is named",
		SourceToken: "", Document: testDocument(t, "topo", 0), Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	if meta.Title != "topo" {
		t.Errorf("CreateDraft(document topo).Title = %q, want the document name %q", meta.Title, "topo")
	}

	renamed, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "topo-renamed", 0), Summary: "rename",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot returned error: %v", err)
	}

	if renamed.Title != "topo-renamed" {
		t.Errorf("AppendSnapshot(document topo-renamed).Title = %q, want %q", renamed.Title, "topo-renamed")
	}

	// A document without a name falls back to the caller supplied title.
	unnamed := mutateDocument(t, testDocument(t, "topo", 0), func(doc map[string]any) {
		metadata, _ := doc["metadata"].(map[string]any)
		delete(metadata, "name")
	})

	fallback, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "Fallback Title",
		SourceToken: "", Document: unnamed, Summary: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft with an unnamed document returned error: %v", err)
	}

	if fallback.Title != "Fallback Title" {
		t.Errorf("CreateDraft(unnamed document).Title = %q, want the caller supplied title %q", fallback.Title, "Fallback Title")
	}
}

func TestEncodeDocumentValidates(t *testing.T) {
	doc := builder.NewDocument("topo")

	// A note node without a note payload is structurally decodable but invalid.
	doc.Nodes = append(doc.Nodes, builder.Node{
		ID: builder.NoteNodeID("topo"), Kind: builder.NodeKindNote,
		Label: "", Position: builder.Position{X: 0, Y: 0}, Size: nil,
		ParentID: "", Device: nil, Switch: nil, Note: nil, Group: nil,
	})

	_, err := EncodeDocument(doc)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("EncodeDocument(note without a note) error = %s, want ErrInvalid", fmtErr(err))
	}

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Errorf("EncodeDocument(note without a note) error = %s, want a *ValidationError", fmtErr(err))
	} else if invalid.Cause == nil {
		t.Errorf("EncodeDocument(note without a note) error = %s, want a *ValidationError carrying its cause", fmtErr(err))
	}

	if !errors.Is(err, builder.ErrInvalidDocument) {
		t.Errorf("EncodeDocument(note without a note) error = %s, want builder.ErrInvalidDocument to remain matchable", fmtErr(err))
	}
}

func TestStoredContentIsParsedAfterIntegrityChecks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Intact, verifiable content that does not hold a builder document.
	scope := snapshotKey("draft-x", "snap-1")
	manifest := storePayload(t, h, scope, []byte(`{"not":"a builder document"}`))
	manifest.ID = "snap-1"

	meta := &DraftMetadata{
		ID: "draft-x", CreatedBy: testActor, Title: "x", SourceToken: "",
		Created: memrecord.Time(1), Updated: memrecord.Time(1), LastModifiedBy: testActor,
		History: []SnapshotManifest{manifest}, Cursor: 0, Publication: nil, Revision: 0,
	}

	value, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("encoding metadata returned error: %v", err)
	}

	if _, err := h.store.CreateRecord(NamespaceDrafts, meta.ID, value); err != nil {
		t.Fatalf("CreateRecord returned error: %v", err)
	}

	_, err = h.service.GetCurrentDocument(ctx, meta.ID)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("GetCurrentDocument(draft of a non-builder payload) error = %s, want ErrCorrupt", fmtErr(err))
	}

	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Errorf("GetCurrentDocument(draft of a non-builder payload) error = %s, want a *CorruptError", fmtErr(err))
	} else if !strings.Contains(corrupt.Reason, "not a valid builder document") {
		t.Errorf("GetCurrentDocument(draft of a non-builder payload) error reason = %q, want it to report an unparsable stored document",
			corrupt.Reason)
	}
}
