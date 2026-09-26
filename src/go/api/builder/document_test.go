package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"phenix/types/builder"
)

// mutateDocument decodes canonical document bytes into a generic map, applies
// mutate, and re-encodes them compactly. It produces the malformed and
// non-canonical payloads an untrusted caller could send.
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
				Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
				Document: tt.document, Summary: "", ID: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateDraft error = %s, want %v", fmtErr(err), tt.is)
			}

			_, err = h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
				DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
				Document: tt.document, Summary: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("AppendSnapshot error = %s, want %v", fmtErr(err), tt.is)
			}

			_, err = h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
				Target: "topo", Kind: "Topology", Actor: testActor,
				Document: tt.document, DraftID: "", SnapshotID: "",
			})
			if !errors.Is(err, tt.is) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("PutPublishedDocument error = %s, want %v", fmtErr(err), tt.is)
			}
		})
	}

	// Nothing invalid reached the store.
	if h.store.count(NamespacePublished) != 0 {
		t.Fatal("an invalid document must never be published")
	}

	stored, err := h.service.GetDraft(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}

	if stored.Revision != meta.Revision || len(stored.History) != 1 {
		t.Fatal("invalid documents must not modify draft metadata")
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
		Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
		Document: compact, Summary: "", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	if meta.History[0].Digest != digestOf(canonical) {
		t.Fatal("stored digest must be the digest of the canonical encoding")
	}

	snapshot, err := h.service.GetCurrentDocument(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetCurrentDocument returned error: %v", err)
	}

	if !bytes.Equal(snapshot.Data, canonical) {
		t.Fatal("stored bytes must be the canonical encoding, not the caller's encoding")
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

	if fromCompact.ID != fromCanonical.ID || fromCompact.Digest != digestOf(canonical) {
		t.Fatalf("published documents = %s and %s, want one content addressed document", fromCompact.ID, fromCanonical.ID)
	}
}

func TestDraftTitleFollowsDocumentName(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "ignored when the document is named",
		SourceToken: "", Document: testDocument(t, "topo", 0), Summary: "", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft returned error: %v", err)
	}

	if meta.Title != "topo" {
		t.Fatalf("title = %q, want the document name %q", meta.Title, "topo")
	}

	renamed, err := h.service.AppendSnapshot(ctx, AppendSnapshotRequest{
		DraftID: meta.ID, Actor: testActor, ExpectedRevision: meta.Revision,
		Document: testDocument(t, "topo-renamed", 0), Summary: "rename",
	})
	if err != nil {
		t.Fatalf("AppendSnapshot returned error: %v", err)
	}

	if renamed.Title != "topo-renamed" {
		t.Fatalf("title after rename = %q, want %q", renamed.Title, "topo-renamed")
	}

	// A document without a name falls back to the caller supplied title.
	unnamed := mutateDocument(t, testDocument(t, "topo", 0), func(doc map[string]any) { delete(doc, "name") })

	fallback, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "Fallback Title",
		SourceToken: "", Document: unnamed, Summary: "", ID: "",
	})
	if err != nil {
		t.Fatalf("CreateDraft with an unnamed document returned error: %v", err)
	}

	if fallback.Title != "Fallback Title" {
		t.Fatalf("title = %q, want the caller supplied title", fallback.Title)
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
		t.Fatalf("EncodeDocument error = %s, want ErrInvalid", fmtErr(err))
	}

	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Cause == nil {
		t.Fatalf("EncodeDocument error = %s, want a *ValidationError carrying its cause", fmtErr(err))
	}

	if !errors.Is(err, builder.ErrInvalidDocument) {
		t.Fatalf("EncodeDocument error = %s, want the document validation failure to remain matchable", fmtErr(err))
	}
}

func TestStoredContentIsParsedAfterIntegrityChecks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Intact, verifiable chunks that do not hold a builder document.
	scope := snapshotScope("draft-x", "snap-1")
	manifest := storePayload(t, h, scope, []byte(`{"not":"a builder document"}`))
	manifest.ID = "snap-1"

	meta := &DraftMetadata{
		ID: "draft-x", Owner: testOwner, Title: "x", SourceToken: "",
		Created: fakeTime(1), Updated: fakeTime(1), LastModifiedBy: testActor,
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
		t.Fatalf("GetCurrentDocument error = %s, want ErrCorrupt", fmtErr(err))
	}

	var corrupt *CorruptError
	if !errors.As(err, &corrupt) || !strings.Contains(corrupt.Reason, "not a valid builder document") {
		t.Fatalf("GetCurrentDocument error = %s, want it to report an unparsable stored document", fmtErr(err))
	}
}
