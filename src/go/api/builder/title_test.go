package builder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"phenix/types/builder"
)

// namedDocument returns the JSON of a valid document renamed to name without
// validating it again, the way a client that skipped validation would send it.
func namedDocument(t *testing.T, name string) []byte {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(testDocument(t, "topo", 0), &doc); err != nil {
		t.Fatalf("decoding the test document: %v", err)
	}

	metadata, ok := doc["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("the test document has no metadata object: %v", doc)
	}

	metadata["name"] = name

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the test document: %v", err)
	}

	return data
}

// TestDocumentNameRulesAgreeWithDraftTitles asserts every document name the
// validator accepts is a valid draft title.
func TestDocumentNameRulesAgreeWithDraftTitles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	longest := strings.Repeat("n", MaxTitleLength)

	if _, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, longest, 0), Summary: "",
	}); err != nil {
		t.Errorf("CreateDraft(a %d-byte name) returned error: %v", MaxTitleLength, err)
	}

	tests := []struct {
		name    string
		docName string
	}{
		{name: "a name one byte too long", docName: strings.Repeat("n", MaxTitleLength+1)},
		{name: "a tab in the name", docName: "my\ttopology"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := builder.NewDocument(tt.docName)
			if _, err := EncodeDocument(doc); !errors.Is(err, ErrInvalid) {
				t.Errorf("EncodeDocument(NewDocument(%.20q)) = %s, want ErrInvalid", tt.docName, fmtErr(err))
			}

			_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
				Actor: testActor, Title: "", SourceToken: "",
				Document: namedDocument(t, tt.docName), Summary: "",
			})

			var invalid *builder.ValidationError
			if !errors.As(err, &invalid) {
				t.Errorf("CreateDraft(%.20q) = %s, want a *builder.ValidationError", tt.docName, fmtErr(err))
			}
		})
	}
}
