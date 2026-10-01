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

	doc["name"] = name

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the test document: %v", err)
	}

	return data
}

// TestDocumentNameRulesAgreeWithDraftTitles asserts that every name the
// document validator accepts can be recorded as a draft title, and that the
// names it cannot record are refused by the document validator itself.
func TestDocumentNameRulesAgreeWithDraftTitles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	longest := strings.Repeat("n", MaxTitleLength)

	if _, err := h.service.CreateDraft(ctx, CreateDraftRequest{
		Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
		Document: testDocument(t, longest, 0), Summary: "", ID: "",
	}); err != nil {
		t.Fatalf("CreateDraft with a %d-byte name returned error: %v", MaxTitleLength, err)
	}

	for _, name := range []string{strings.Repeat("n", MaxTitleLength+1), "my\ttopology"} {
		doc := builder.NewDocument(name)
		if _, err := EncodeDocument(doc); !errors.Is(err, ErrInvalid) {
			t.Fatalf("EncodeDocument(%.20q) error = %s, want ErrInvalid", name, fmtErr(err))
		}

		_, err := h.service.CreateDraft(ctx, CreateDraftRequest{
			Owner: testOwner, Actor: testActor, Title: "", SourceToken: "",
			Document: namedDocument(t, name), Summary: "", ID: "",
		})

		var invalid *builder.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("CreateDraft(%.20q) error = %s, want the document validator's error", name, fmtErr(err))
		}
	}
}
