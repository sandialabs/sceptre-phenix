package builder_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phenix/types/builder"
)

func TestDecodeFixture(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	if doc.Schema != builder.SchemaURI {
		t.Fatalf("schema = %q, want %q", doc.Schema, builder.SchemaURI)
	}

	if doc.Revision != builder.SchemaRevision {
		t.Fatalf("revision = %d, want %d", doc.Revision, builder.SchemaRevision)
	}

	if got, want := len(doc.Nodes), 5; got != want {
		t.Fatalf("decoded %d nodes, want %d", got, want)
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("fixture document is invalid: %v", err)
	}
}

func TestEncodeUsesDollarSchemaKey(t *testing.T) {
	doc := builder.NewDocument("test")

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("encoding document: %v", err)
	}

	var raw map[string]json.RawMessage

	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshaling encoded document: %v", err)
	}

	if _, ok := raw["$schema"]; !ok {
		t.Fatalf("encoded document has no $schema key: %s", data)
	}

	if _, ok := raw["schema"]; ok {
		t.Fatalf("encoded document still carries a legacy schema key: %s", data)
	}
}

func TestDecodeReaderRejectsTrailingContent(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	reader := strings.NewReader(string(valid) + "\n" + string(valid))

	if _, err := builder.DecodeReader(reader); err == nil {
		t.Fatal("expected an error for concatenated documents, got nil")
	} else if !strings.Contains(err.Error(), "trailing content") {
		t.Fatalf("error %q does not mention trailing content", err.Error())
	}
}

func TestDecodeRejects(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	tests := []struct {
		name    string
		data    string
		wantErr error
		wantMsg string
	}{
		{
			name:    "unknown field",
			data:    strings.Replace(string(valid), `"name": "hand-authored"`, `"nickname": "nope"`, 1),
			wantMsg: "unknown field",
		},
		{
			name:    "wrong schema",
			data:    strings.Replace(string(valid), builder.SchemaURI, "https://example.com/other", 1),
			wantErr: builder.ErrUnsupportedSchema,
		},
		{
			name:    "wrong revision",
			data:    strings.Replace(string(valid), `"revision": 1`, `"revision": 2`, 1),
			wantErr: builder.ErrUnsupportedRevision,
		},
		{
			name:    "missing schema",
			data:    `{"revision": 1, "id": "x"}`,
			wantErr: builder.ErrUnsupportedSchema,
		},
		{
			name:    "legacy schema field",
			data:    strings.Replace(string(valid), `"$schema"`, `"schema"`, 1),
			wantMsg: "unknown field",
		},
		{
			name:    "concatenated documents",
			data:    string(valid) + string(valid),
			wantMsg: "trailing content",
		},
		{
			name:    "trailing object",
			data:    string(valid) + `{"$schema":"x"}`,
			wantMsg: "trailing content",
		},
		{
			name:    "trailing number",
			data:    string(valid) + " 5",
			wantMsg: "trailing content",
		},
		{
			name:    "trailing null",
			data:    string(valid) + " null",
			wantMsg: "trailing content",
		},
		{
			name:    "trailing garbage",
			data:    string(valid) + " nope",
			wantMsg: "trailing content",
		},
		{
			name:    "wrong type",
			data:    strings.Replace(string(valid), `"revision": 1`, `"revision": "one"`, 1),
			wantMsg: "decoding builder document",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := builder.Decode([]byte(test.data))
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error %v does not wrap %v", err, test.wantErr)
			}

			if test.wantMsg != "" && !strings.Contains(err.Error(), test.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), test.wantMsg)
			}
		})
	}
}

func TestParseValidates(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	if _, err := builder.Parse(valid); err != nil {
		t.Fatalf("Parse of valid fixture: %v", err)
	}

	broken := strings.Replace(string(valid), `"id": "`+idNetExp+`"`, `"id": ""`, 1)

	_, err = builder.Parse([]byte(broken))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !errors.Is(err, builder.ErrInvalidDocument) {
		t.Fatalf("error %v does not wrap ErrInvalidDocument", err)
	}

	var validationErr *builder.ValidationError

	if !errors.As(err, &validationErr) || len(validationErr.Issues) == 0 {
		t.Fatalf("expected a *ValidationError with issues, got %v", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !reflect.DeepEqual(doc, decoded) {
		t.Fatalf("round trip changed the document:\nbefore: %s\nafter: %s",
			asJSON(t, doc), asJSON(t, decoded))
	}
}

func TestNewDocumentIsValid(t *testing.T) {
	doc := builder.NewDocument("fresh")

	if doc.ID != builder.DocumentID("fresh") {
		t.Fatalf("document ID = %q, want %q", doc.ID, builder.DocumentID("fresh"))
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("new document is invalid: %v", err)
	}
}

func TestContentDigestIsStable(t *testing.T) {
	first, err := builder.ContentDigest(map[string]any{"b": 1, "a": []any{"x", "y"}})
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	second, err := builder.ContentDigest(map[string]any{"a": []any{"x", "y"}, "b": 1})
	if err != nil {
		t.Fatalf("ContentDigest: %v", err)
	}

	if first != second {
		t.Fatalf("digest depends on map order: %q != %q", first, second)
	}

	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("digest %q is not prefixed with the hash algorithm", first)
	}
}

func TestDeterministicIDsAreDistinctAndStable(t *testing.T) {
	ids := map[string]string{
		"document":  builder.DocumentID("example"),
		"device":    builder.DeviceNodeID("example"),
		"switch":    builder.SwitchNodeID("example"),
		"network":   builder.NetworkID("example"),
		"note":      builder.NoteNodeID("example"),
		"group":     builder.GroupNodeID("example"),
		"handle":    builder.InterfaceHandleID("example", "eth0", 0),
		"handle-1":  builder.InterfaceHandleID("example", "eth0", 1),
		"handle-ab": builder.InterfaceHandleID("exampleeth0", "", 0),
	}

	seen := map[string]string{}

	for name, id := range ids {
		if prev, ok := seen[id]; ok {
			t.Fatalf("%s and %s share the ID %q", prev, name, id)
		}

		seen[id] = name
	}

	if builder.DeviceNodeID("Example") != builder.DeviceNodeID("example") {
		t.Fatal("device IDs must be case-insensitive")
	}
}
