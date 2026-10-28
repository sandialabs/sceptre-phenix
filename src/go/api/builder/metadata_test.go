package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

// tamperDraft rewrites the stored draft record through mutate, bypassing the
// service.
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
	set := func(key string, value any) func(map[string]any) {
		return func(raw map[string]any) { raw[key] = value }
	}

	tests := map[string]func(map[string]any){
		"a creator with a control character":  set("documentCreatedBy", "al\nice"),
		"a creator that is too long":          set("documentCreatedBy", strings.Repeat("a", MaxOwnerLength+1)),
		"a creator that is not text":          set("documentCreatedBy", 7),
		"a creation time with an offset":      set("documentCreatedAt", "2026-10-01T15:04:05+00:00"),
		"a creation time with a fraction":     set("documentCreatedAt", "2026-10-01T15:04:05.5Z"),
		"a creation time that is no time":     set("documentCreatedAt", "then"),
		"a source file that is a path":        set("sourceFile", "dir/file.json"),
		"a source file that is a parent":      set("sourceFile", ".."),
		"a source file that is too long":      set("sourceFile", strings.Repeat("f", MaxSourceFileLength+1)),
		"a source file with a control":        set("sourceFile", "file\x00.json"),
		"a stored stamp, which is never kept": set("stamp", map[string]any{"createdBy": "alice"}),
		"claims another draft ID": func(raw map[string]any) {
			raw["id"] = "someone-elses-draft"
		},
		"carries an unknown field": func(raw map[string]any) {
			raw["injected"] = "value"
		},
		"has no creator": func(raw map[string]any) {
			raw["createdBy"] = ""
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
		"declares a compressed size past the limit": func(raw map[string]any) {
			history, _ := raw["history"].([]any)
			first, _ := history[0].(map[string]any)
			first["compressedSize"] = MaxCompressedBytes + 1
		},
		"declares an unknown publication mode": func(raw map[string]any) {
			raw["publication"] = map[string]any{
				"mode": "delete-everything", "topologyTarget": "topo", "topologyAction": "create",
				"snapshotId": "id-2", "digest": digestOf([]byte("x")), "revision": 1,
				"publishedAt": memrecord.Time(1), "publishedBy": testActor,
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
				t.Errorf("GetDraft(%s) error = %s, want ErrCorrupt", meta.ID, fmtErr(err))
			}

			// The listing leaves the draft out. Orphan cleanup keeps its content.
			if drafts, _, err := h.service.ListDraftsWithDamaged(ctx); err != nil {
				t.Errorf("ListDraftsWithDamaged returned error: %s", fmtErr(err))
			} else if len(drafts) != 0 {
				t.Errorf("ListDraftsWithDamaged = %d drafts, want the tampered draft left out", len(drafts))
			}

			h.passOrphanGracePeriod()

			if removed, err := h.service.CleanupOrphanedContent(ctx, false); err != nil {
				t.Errorf("CleanupOrphanedContent returned error: %s", fmtErr(err))
			} else if removed != 0 {
				t.Errorf("CleanupOrphanedContent = %d, want 0: the content of the tampered draft kept", removed)
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
		t.Errorf("GetDraft(%s) error = %s, want ErrCorrupt", meta.ID, fmtErr(err))
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
		"declares no compressed size": func(raw map[string]any) {
			raw["compressedSize"] = 0
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
				t.Errorf("GetPublishedDocument(%s) error = %s, want ErrCorrupt", doc.ID, fmtErr(err))
			}
		})
	}
}

// TestDecodeReferenceIsStrict decodes each combination of digest, id and path,
// and refuses any other value.
func TestDecodeReferenceIsStrict(t *testing.T) {
	t.Parallel()

	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		id     = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
		file   = "/phenix/topologies/site/builder.yaml"
	)

	for name, want := range map[string]DocumentReference{
		"digest":          {Digest: digest},
		"id":              {ID: id},
		"digest and id":   {Digest: digest, ID: id},
		"path":            {Path: file},
		"path and digest": {Digest: digest, Path: file},
		"path and id":     {ID: id, Path: file},
		"all three":       {Digest: digest, ID: id, Path: file},
		"a json file":     {Path: "/phenix/builder.json"},
		"a yml file":      {Path: "/phenix/a b/builder.yml"},
		"the longest path": {
			Path: "/" + strings.Repeat("a", MaxDocumentPathLength-len("/.json")) + ".json",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			encoded, err := want.EncodeReference()
			if err != nil {
				t.Fatalf("EncodeReference returned error: %v", err)
			}

			got, err := DecodeReference(encoded)
			if err != nil {
				t.Fatalf("DecodeReference(%s) returned error: %s", encoded, fmtErr(err))
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("DecodeReference(%s) mismatch (-want +got):\n%s", encoded, diff)
			}
		})
	}

	// The sub-keys are written in a fixed order, and an empty one is left out:
	// the exact text is the annotation's encoding.
	encoded, err := DocumentReference{Digest: digest, ID: id, Path: file}.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference(all three) returned error: %v", err)
	}

	if diff := cmp.Diff(`{"digest":"`+digest+`","id":"`+id+`","path":"`+file+`"}`, encoded); diff != "" {
		t.Errorf("EncodeReference(all three) mismatch (-want +got):\n%s", diff)
	}

	if idOnly, err := (DocumentReference{ID: id}).EncodeReference(); err != nil {
		t.Errorf("EncodeReference(id) returned error: %v", err)
	} else if diff := cmp.Diff(`{"id":"`+id+`"}`, idOnly); diff != "" {
		t.Errorf("EncodeReference(id) mismatch (-want +got):\n%s", diff)
	}

	for name, value := range map[string]string{
		"empty":                    "",
		"empty object":             "{}",
		"null":                     "null",
		"not json":                 "not json",
		"not an object":            `"just a string"`,
		"a list":                   `["` + id + `"]`,
		"trailing content":         encoded + `{"id":"other"}`,
		"trailing brace":           encoded + "}",
		"unknown sub-key":          `{"id":"` + id + `","file":"` + file + `"}`,
		"a field of the old shape": `{"id":"` + id + `","digest":"` + digest + `","draftId":"draft-1"}`,
		"a number in the old shape": `{"id":"` + id + `","digest":"` + digest + `","size":10,"chunks":1,"chunkSize":1024,` +
			`"schema":"` + builder.SchemaURI + `","createdAt":"2026-09-29T10:00:00Z"}`,
		"a number":                          `{"id":42}`,
		"a list sub-key":                    `{"id":["` + id + `"]}`,
		"a null sub-key":                    `{"digest":"` + digest + `","id":null}`,
		"an empty sub-key":                  `{"digest":"` + digest + `","id":""}`,
		"invalid id":                        `{"id":"../escape"}`,
		"an id that is too long":            `{"id":"` + strings.Repeat("a", MaxIDLength+1) + `"}`,
		"invalid digest":                    `{"digest":"deadbeef"}`,
		"a digest in upper case":            `{"digest":"` + strings.ToUpper(digest) + `"}`,
		"a digest of another sum":           `{"digest":"md5:` + strings.Repeat("0", 64) + `"}`,
		"a relative path":                   `{"path":"topologies/builder.yaml"}`,
		"a path with ..":                    `{"path":"/phenix/../etc/builder.yaml"}`,
		"a path with .":                     `{"path":"/phenix/./builder.yaml"}`,
		"a path with //":                    `{"path":"/phenix//builder.yaml"}`,
		"a path ending in /":                `{"path":"/phenix/builder.yaml/"}`,
		"a path with no extension":          `{"path":"/phenix/builder"}`,
		"a path to another file":            `{"path":"/etc/phenix/store.bdb"}`,
		"an extension in upper case":        `{"path":"/phenix/builder.YAML"}`,
		"a path with a control character":   `{"path":"/phenix/a\nb.yaml"}`,
		"a path with a NUL":                 `{"path":"/phenix/a\u0000.yaml"}`,
		"a path that is too long":           `{"path":"/` + strings.Repeat("a", MaxDocumentPathLength) + `.json"}`,
		"a valid id beside an invalid path": `{"id":"` + id + `","path":"builder.yaml"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := DecodeReference(value)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("DecodeReference(%s) error = %s, want ErrInvalid", value, fmtErr(err))
			}

			if got != (DocumentReference{}) {
				t.Errorf("DecodeReference(%s) = %+v, want no reference", value, got)
			}
		})
	}
}

// TestValidateDocumentPath checks the syntax rules of a Builder file path on
// their own, as a caller that sets one checks it.
func TestValidateDocumentPath(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"/phenix/builder.json", "/phenix/site/phenix-configs/builder.yaml", "/b.yml", "/phenix/.yaml"} {
		if err := ValidateDocumentPath(value); err != nil {
			t.Errorf("ValidateDocumentPath(%q) returned error: %v", value, err)
		}
	}

	for _, value := range []string{
		"", "builder.yaml", "./builder.yaml", "/phenix/builder", "/phenix/builder.txt", "/phenix/builder.yaml/",
		"/phenix/../builder.yaml", "/phenix//builder.yaml", "/phenix/\x7f.yaml", "/phenix/builder.yaml\n",
	} {
		if err := ValidateDocumentPath(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateDocumentPath(%q) error = %s, want ErrInvalid", value, fmtErr(err))
		}
	}
}

// TestDocumentAnnotationIsStructured asserts the store shows the document
// reference annotation, which it names by a literal, as a map of its sub-keys.
func TestDocumentAnnotationIsStructured(t *testing.T) {
	t.Parallel()

	if !store.StructuredAnnotation(DocumentAnnotation) {
		t.Errorf("store.StructuredAnnotation(%q) = false, want the annotation shown as a map", DocumentAnnotation)
	}
}

func TestUntrustedStringsAreBounded(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	document := testDocument(t, "topo", 0)
	long := strings.Repeat("a", MaxSummaryLength+1)

	tests := map[string]CreateDraftRequest{
		"oversized actor": {
			Actor: strings.Repeat("a", MaxOwnerLength+1),
			Title: "", SourceToken: "", Document: document, Summary: "",
		},
		"oversized title": {
			Actor: testActor, Title: strings.Repeat("t", MaxTitleLength+1),
			SourceToken: "", Document: document, Summary: "",
		},
		"oversized source token": {
			Actor: testActor, Title: "",
			SourceToken: strings.Repeat("s", MaxSourceTokenLength+1), Document: document, Summary: "",
		},
		"oversized summary": {
			Actor: testActor, Title: "", SourceToken: "",
			Document: document, Summary: long,
		},
		"actor with control characters": {
			Actor: "alice\x00", Title: "", SourceToken: "",
			Document: document, Summary: "",
		},
		"actor with invalid utf-8": {
			Actor: "alice\xff", Title: "", SourceToken: "",
			Document: document, Summary: "",
		},
		"empty actor": {
			Actor: "", Title: "", SourceToken: "",
			Document: document, Summary: "",
		},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := h.service.CreateDraft(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Errorf("CreateDraft(%s request) error = %s, want ErrInvalid", name, fmtErr(err))
			}
		})
	}

	t.Run("published target", func(t *testing.T) {
		_, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: strings.Repeat("t", MaxTargetLength+1), Kind: "Topology", Actor: testActor,
			Document: document, DraftID: "", SnapshotID: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("PutPublishedDocument with an oversized target error = %s, want ErrInvalid", fmtErr(err))
		}
	})

	t.Run("published kind", func(t *testing.T) {
		_, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
			Target: "topo", Kind: strings.Repeat("k", MaxKindLength+1), Actor: testActor,
			Document: document, DraftID: "", SnapshotID: "",
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("PutPublishedDocument with an oversized kind error = %s, want ErrInvalid", fmtErr(err))
		}
	})
}

func TestGeneratedIdentifiersAreValidated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.service.newID = func() (string, error) { return "../escape", nil }

	_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, "topo", 0), Summary: "",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateDraft with the generated ID ../escape error = %s, want ErrInvalid", fmtErr(err))
	}
}
