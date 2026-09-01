package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types/builder"
)

// tamperDraft rewrites the stored draft record through the given mutation,
// bypassing the service so the test can simulate a corrupted or hand-edited
// record.
func tamperDraft(t *testing.T, h *testHarness, meta *DraftMetadata, mutate func(map[string]any)) {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceDrafts, meta.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	var raw map[string]any

	if err := json.Unmarshal(record.Value, &raw); err != nil {
		t.Fatalf("unmarshalling the stored draft returned error: %v", err)
	}

	mutate(raw)

	value, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshalling the tampered draft returned error: %v", err)
	}

	if _, err := h.store.UpdateRecord(NamespaceDrafts, meta.ID, value, store.AnyRevision); err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}
}

func TestTamperedDraftMetadataIsRejected(t *testing.T) {
	tests := map[string]func(map[string]any){
		"claims another draft ID": func(raw map[string]any) {
			raw["id"] = "someone-elses-draft"
		},
		"carries an unknown field": func(raw map[string]any) {
			raw["injected"] = "value"
		},
		"has no owner": func(raw map[string]any) {
			raw["owner"] = ""
		},
		"has an empty history": func(raw map[string]any) {
			raw["history"] = []any{}
		},
		"has a cursor outside its history": func(raw map[string]any) {
			raw["cursor"] = 7
		},
		"repeats a snapshot ID": func(raw map[string]any) {
			history, _ := raw["history"].([]any)
			first, _ := history[0].(map[string]any)
			second, _ := history[1].(map[string]any)
			second["id"] = first["id"]
		},
		"names a snapshot with a path segment": func(raw map[string]any) {
			history, _ := raw["history"].([]any)
			first, _ := history[0].(map[string]any)
			first["id"] = "../../escape"
		},
		"holds a manifest digest that is not sha256": func(raw map[string]any) {
			history, _ := raw["history"].([]any)
			first, _ := history[0].(map[string]any)
			first["digest"] = "not-a-digest"
		},
		"holds a chunk digest that is not sha256": func(raw map[string]any) {
			history, _ := raw["history"].([]any)
			first, _ := history[0].(map[string]any)
			first["chunkDigests"] = []any{"../escape"}
		},
		"declares an unknown publication mode": func(raw map[string]any) {
			raw["publication"] = map[string]any{
				"mode": "delete-everything", "topologyTarget": "topo", "topologyAction": "create",
				"snapshotId": "id-2", "digest": digestOf([]byte("x")), "revision": 1,
				"publishedAt": fakeTime(1), "publishedBy": testActor,
			}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			meta := createTestDraft(t, h, "topo")
			meta = appendTestSnapshot(t, h, meta, "topo-v2", testActor)

			tamperDraft(t, h, meta, mutate)

			if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("GetDraft error = %s, want ErrCorrupt", fmtErr(err))
			}

			drafts, err := h.service.ListDrafts(ctx)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("ListDrafts error = %s, want ErrCorrupt (got %d drafts)", fmtErr(err), len(drafts))
			}
		})
	}
}

func TestTamperedDraftMetadataWithTrailingContentIsRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	meta := createTestDraft(t, h, "topo")

	record, err := h.store.GetRecord(NamespaceDrafts, meta.ID)
	if err != nil {
		t.Fatalf("GetRecord returned error: %v", err)
	}

	value := append(append([]byte(nil), record.Value...), []byte(`{"id":"other"}`)...)

	if _, err := h.store.UpdateRecord(NamespaceDrafts, meta.ID, value, store.AnyRevision); err != nil {
		t.Fatalf("UpdateRecord returned error: %v", err)
	}

	if _, err := h.service.GetDraft(ctx, meta.ID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetDraft error = %s, want ErrCorrupt", fmtErr(err))
	}
}

func TestTamperedPublishedMetadataIsRejected(t *testing.T) {
	tests := map[string]func(map[string]any){
		"claims another document ID": func(raw map[string]any) {
			raw["id"] = "someone-elses-document"
		},
		"carries an unknown field": func(raw map[string]any) {
			raw["injected"] = "value"
		},
		"was retargeted without rehashing": func(raw map[string]any) {
			raw["target"] = "another-topology"
		},
		"has no kind": func(raw map[string]any) {
			raw["kind"] = ""
		},
		"holds a chunk digest that is not sha256": func(raw map[string]any) {
			raw["chunkDigests"] = []any{"../escape"}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
				Target: "topo", Kind: "Topology", Actor: testActor,
				Document: testDocument(t, "topo", 0), DraftID: "", SnapshotID: "",
			})
			if err != nil {
				t.Fatalf("PutPublishedDocument returned error: %v", err)
			}

			record, err := h.store.GetRecord(NamespacePublished, doc.ID)
			if err != nil {
				t.Fatalf("GetRecord returned error: %v", err)
			}

			var raw map[string]any

			if err := json.Unmarshal(record.Value, &raw); err != nil {
				t.Fatalf("unmarshalling the stored document returned error: %v", err)
			}

			mutate(raw)

			value, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("marshalling the tampered document returned error: %v", err)
			}

			if _, err := h.store.UpdateRecord(NamespacePublished, doc.ID, value, store.AnyRevision); err != nil {
				t.Fatalf("UpdateRecord returned error: %v", err)
			}

			if _, err := h.service.GetPublishedDocument(ctx, doc.ID); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("GetPublishedDocument error = %s, want ErrCorrupt", fmtErr(err))
			}
		})
	}
}

func TestDecodeReferenceIsStrict(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: testDocument(t, "topo", 0), DraftID: "", SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	encoded, err := doc.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	if _, err := DecodeReference(encoded); err != nil {
		t.Fatalf("DecodeReference of a valid reference returned error: %v", err)
	}

	tests := map[string]func(map[string]any){
		"unknown field":         func(raw map[string]any) { raw["injected"] = "value" },
		"invalid id":            func(raw map[string]any) { raw["id"] = "../escape" },
		"invalid digest":        func(raw map[string]any) { raw["digest"] = "deadbeef" },
		"foreign schema":        func(raw map[string]any) { raw["schema"] = "https://example.com/other" },
		"zero size":             func(raw map[string]any) { raw["size"] = 0 },
		"oversized document":    func(raw map[string]any) { raw["size"] = MaxDocumentBytes + 1 },
		"zero chunks":           func(raw map[string]any) { raw["chunks"] = 0 },
		"too many chunks":       func(raw map[string]any) { raw["chunks"] = MaxChunks + 1 },
		"oversized chunk size":  func(raw map[string]any) { raw["chunkSize"] = ChunkBytes + 1 },
		"invalid draft id":      func(raw map[string]any) { raw["draftId"] = "not/valid" },
		"invalid timestamp":     func(raw map[string]any) { raw["createdAt"] = "yesterday" },
		"control chars in user": func(raw map[string]any) { raw["createdBy"] = "alice\x00" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var raw map[string]any

			if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
				t.Fatalf("unmarshalling the reference returned error: %v", err)
			}

			mutate(raw)

			value, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("marshalling the mutated reference returned error: %v", err)
			}

			if _, err := DecodeReference(string(value)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("DecodeReference error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	t.Run("trailing content", func(t *testing.T) {
		if _, err := DecodeReference(encoded + `{"id":"other"}`); !errors.Is(err, ErrInvalid) {
			t.Fatalf("DecodeReference error = %s, want ErrInvalid", fmtErr(err))
		}
	})

	t.Run("not an object", func(t *testing.T) {
		if _, err := DecodeReference(`"just a string"`); !errors.Is(err, ErrInvalid) {
			t.Fatalf("DecodeReference error = %s, want ErrInvalid", fmtErr(err))
		}
	})
}

func TestVerifyPublishedDocumentRejectsMismatchedReferences(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: testRandomDocument(t, "topo", 3000), DraftID: "", SnapshotID: "",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	ref := doc.Reference()

	if _, err := h.service.VerifyPublishedDocument(ctx, ref); err != nil {
		t.Fatalf("VerifyPublishedDocument returned error: %v", err)
	}

	t.Run("digest", func(t *testing.T) {
		other := ref
		other.Digest = digestOf([]byte("something else"))

		if _, err := h.service.VerifyPublishedDocument(ctx, other); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("VerifyPublishedDocument error = %s, want ErrCorrupt", fmtErr(err))
		}
	})

	t.Run("chunk count", func(t *testing.T) {
		other := ref
		other.Chunks = ref.Chunks + 1

		if _, err := h.service.VerifyPublishedDocument(ctx, other); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("VerifyPublishedDocument error = %s, want ErrCorrupt", fmtErr(err))
		}
	})

	t.Run("chunk size", func(t *testing.T) {
		other := ref
		other.ChunkSize = ref.ChunkSize / 2

		if _, err := h.service.VerifyPublishedDocument(ctx, other); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("VerifyPublishedDocument error = %s, want ErrCorrupt", fmtErr(err))
		}
	})

	t.Run("size", func(t *testing.T) {
		other := ref
		other.Size = ref.Size - 1

		if _, err := h.service.VerifyPublishedDocument(ctx, other); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("VerifyPublishedDocument error = %s, want ErrCorrupt", fmtErr(err))
		}
	})
}

func TestUntrustedStringsAreBounded(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	document := testDocument(t, "topo", 0)
	long := strings.Repeat("a", MaxSummaryLength+1)

	tests := map[string]CreateDraftRequest{
		"oversized owner": {
			Owner: strings.Repeat("o", MaxOwnerLength+1), Actor: testActor,
			Title: "", SourceToken: "", Document: document, Summary: "", ID: "",
		},
		"oversized actor": {
			Owner: testOwner, Actor: strings.Repeat("a", MaxOwnerLength+1),
			Title: "", SourceToken: "", Document: document, Summary: "", ID: "",
		},
		"oversized title": {
			Owner: testOwner, Actor: testActor, Title: strings.Repeat("t", MaxTitleLength+1),
			SourceToken: "", Document: document, Summary: "", ID: "",
		},
		"oversized source token": {
			Owner: testOwner, Actor: testActor, Title: "",
			SourceToken: strings.Repeat("s", MaxSourceTokenLength+1), Document: document, Summary: "", ID: "",
		},
		"oversized summary": {
			Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: long, ID: "",
		},
		"owner with control characters": {
			Owner: "alice\x00", Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: "", ID: "",
		},
		"owner with invalid utf-8": {
			Owner: "alice\xff", Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: "", ID: "",
		},
		"caller supplied id with a path segment": {
			Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: "", ID: "../escape",
		},
		"empty owner": {
			Owner: "", Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: "", ID: "",
		},
		"empty actor": {
			Owner: testOwner, Actor: "", Title: "", SourceToken: "",
			Document: document, Summary: "", ID: "",
		},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := h.service.CreateDraft(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateDraft error = %s, want ErrInvalid", fmtErr(err))
			}
		})
	}

	t.Run("published target", func(t *testing.T) {
		_, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: strings.Repeat("t", MaxTargetLength+1), Kind: "Topology", Actor: testActor,
			Document: document, DraftID: "", SnapshotID: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("PutPublishedDocument error = %s, want ErrInvalid", fmtErr(err))
		}
	})

	t.Run("published kind", func(t *testing.T) {
		_, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: "topo", Kind: strings.Repeat("k", MaxKindLength+1), Actor: testActor,
			Document: document, DraftID: "", SnapshotID: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("PutPublishedDocument error = %s, want ErrInvalid", fmtErr(err))
		}
	})
}

func TestGeneratedIdentifiersAreValidated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.service.newID = func() (string, error) { return "../escape", nil }

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, "topo", 0), Summary: "", ID: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDraft error = %s, want ErrInvalid for an unusable generated ID", fmtErr(err))
	}
}

func TestReferenceRoundTripsThroughAnAnnotation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	doc, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: testActor,
		Document: testDocument(t, "topo", 0), DraftID: "draft-1", SnapshotID: "snap-1",
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	encoded, err := doc.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	switch {
	case strings.Contains(encoded, "\n"):
		t.Fatal("an annotation value must be compact JSON")
	case len(encoded) > 1024:
		t.Fatalf("the reference encodes to %d bytes, too large for an annotation", len(encoded))
	}

	decoded, err := DecodeReference(encoded)
	if err != nil {
		t.Fatalf("DecodeReference returned error: %v", err)
	}

	if decoded != doc.Reference() {
		t.Fatalf("decoded reference = %+v, want %+v", decoded, doc.Reference())
	}

	if decoded.Schema != builder.SchemaURI {
		t.Fatalf("reference schema = %q, want %q", decoded.Schema, builder.SchemaURI)
	}

	data, err := h.service.VerifyPublishedDocument(ctx, decoded)
	if err != nil {
		t.Fatalf("VerifyPublishedDocument returned error: %v", err)
	}

	if _, err := builder.Parse(data); err != nil {
		t.Fatalf("the verified document must parse: %v", err)
	}
}
