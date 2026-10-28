package builder_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
	"phenix/util"
)

func TestDecodeFixture(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")

	type header struct {
		Schema   string
		Revision int
		Nodes    int
	}

	want := header{Schema: builder.SchemaURI, Revision: builder.SchemaRevision, Nodes: 5}
	got := header{Schema: doc.Schema, Revision: doc.Revision, Nodes: len(doc.Nodes)}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Decode(document.json) mismatch (-want +got):\n%s", diff)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of document.json = %v, want nil", err)
	}
}

func TestDecodeReaderRejectsTrailingContent(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading document.json: %v", err)
	}

	reader := strings.NewReader(string(valid) + "\n" + string(valid))

	if _, err := builder.DecodeReader(reader); !errors.Is(err, util.ErrTrailingJSON) {
		t.Errorf("DecodeReader(two documents) error = %v, want %v", err, util.ErrTrailingJSON)
	}
}

func TestDecodeRejects(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading document.json: %v", err)
	}

	tests := []struct {
		name    string
		data    string
		wantErr error
		wantMsg string
		// wantCode is the code of an issue the error lists, when it is a
		// validation error.
		wantCode builder.Code
		// wantTypeError is whether the error is the decoder's for a value of the wrong type.
		wantTypeError bool
	}{
		{
			name:     "unknown field",
			data:     strings.Replace(string(valid), `"name": "hand-authored"`, `"nickname": "nope"`, 1),
			wantMsg:  "unknown field",
			wantCode: builder.CodeDocumentFieldUnknown,
		},
		{
			name:    "unknown grid field",
			data:    strings.Replace(string(valid), `"size": 16 }`, `"size": 16, "spacing": 2 }`, 1),
			wantMsg: `unknown field "spacing"`,
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
			data:    `{"revision": 1, "metadata": {"id": "x"}}`,
			wantErr: builder.ErrUnsupportedSchema,
		},
		{
			name:    "concatenated documents",
			data:    string(valid) + string(valid),
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing object",
			data:    string(valid) + `{"$schema":"x"}`,
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing number",
			data:    string(valid) + " 5",
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing null",
			data:    string(valid) + " null",
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing garbage",
			data:    string(valid) + " nope",
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing brace",
			data:    string(valid) + "}",
			wantErr: util.ErrTrailingJSON,
		},
		{
			name:    "trailing bracket",
			data:    string(valid) + " ]",
			wantErr: util.ErrTrailingJSON,
		},
		{
			// The editor checks no type of the revision, which is the
			// decoder's to refuse.
			name:          "wrong type",
			data:          strings.Replace(string(valid), `"revision": 1`, `"revision": "one"`, 1),
			wantTypeError: true,
		},
		{
			// The key is the icon's name: an entry has no name of its own.
			name: "a name of a custom icon",
			data: strings.Replace(string(valid), `"viewport":`,
				`"icons": {"`+iconFixtureName+`": {"name": "plc", "data": "`+iconFixtureData+`"}}, "viewport":`, 1),
			wantMsg: `unknown field "name"`,
		},
		{
			name: "unknown field of a template",
			data: strings.Replace(string(valid), `"viewport":`,
				`"templates": [{"id": "`+idTemplate+`", "name": "x", "hint": "y", "device": {"spec": {}}}], "viewport":`, 1),
			wantMsg: `unknown field "hint"`,
		},
		{
			// A template fills in a device, and has none of its own
			// fields.
			name: "a hostname of a template device",
			data: strings.Replace(string(valid), `"viewport":`,
				`"templates": [{"id": "`+idTemplate+`", "name": "x", "device": {"hostname": "y", "spec": {}}}], "viewport":`, 1),
			wantMsg: `unknown field "hostname"`,
		},
		{
			name: "unknown field of a switch",
			data: strings.Replace(string(valid),
				`"switch": { "networkId": "`+idNetExp+`" }`,
				`"switch": { "networkId": "`+idNetExp+`", "color": "#fff" }`, 1),
			wantMsg: `unknown field "color"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := builder.Decode([]byte(test.data))
			if err == nil {
				t.Fatal("Decode() error = nil, want an error")
			}

			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Errorf("Decode() error = %v, want %v", err, test.wantErr)
			}

			if test.wantMsg != "" && !strings.Contains(err.Error(), test.wantMsg) {
				t.Errorf("Decode() error = %q, want one containing %q", err.Error(), test.wantMsg)
			}

			var typeErr *json.UnmarshalTypeError
			if test.wantTypeError && !errors.As(err, &typeErr) {
				t.Errorf("Decode() error = %v, want a *json.UnmarshalTypeError", err)
			}

			if test.wantCode != "" && !slices.ContainsFunc(builder.ErrorIssues(err), func(issue builder.Issue) bool {
				return issue.Code == test.wantCode
			}) {
				t.Errorf("Decode() error = %v, want one listing an issue of code %s", err, test.wantCode)
			}
		})
	}
}

// TestDecodeReportsWrongShapesAsIssues checks that the decoder's refusals become coded issues at their
// paths where the editor refuses them too, and keep the decoder's own error elsewhere.
func TestDecodeReportsWrongShapesAsIssues(t *testing.T) {
	// fixture returns the fixture document as JSON decodes it.
	fixture := func(t *testing.T) map[string]any {
		t.Helper()

		data, err := os.ReadFile(filepath.Join("testdata", "document.json"))
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}

		var document map[string]any

		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("decoding the fixture: %v", err)
		}

		return document
	}

	nodes, _ := fixture(t)["nodes"].([]any)

	tests := []struct {
		name string
		edit func(document map[string]any)
		// want is every issue of the error, as "code path: message". It is nil
		// for an error that is the decoder's own.
		want []string
	}{
		{
			name: "values of the wrong type at the paths the editor checks, and an unknown key",
			edit: func(document map[string]any) {
				metadata := document["metadata"].(map[string]any)
				metadata["createdBy"] = 7
				metadata["notes"] = []any{"kept", 3}
				metadata["author"] = "alice"

				nodes := document["nodes"].([]any)
				nodes = append(nodes, map[string]any{
					"id": "8a6b5dbc-2bd3-4a43-9dd2-3c6f3d0b54a1", "kind": "line", "position": map[string]any{"x": 0, "y": 0},
					"line": map[string]any{"points": []any{}, "endArrow": "yes"},
				})
				document["nodes"] = nodes

				document["source"] = map[string]any{
					"kind": "manual", "includeTopologies": []any{"corp", 5}, "annotations": map[string]any{"count": 3},
				}
				document["layout"] = 5
			},
			want: []string{
				"document.layout.not-text layout: layout must be a string",
				"document.field.unknown metadata.author: unknown field \"author\"",
				"metadata.user.not-text metadata.createdBy: metadata.createdBy must be a string",
				"metadata.note.not-text metadata.notes[1]: note must be text",
				"drawing.arrow.not-boolean nodes[" + strconv.Itoa(len(nodes)) + "].line.endArrow: endArrow must be true or false",
				"source.annotation.not-text source.annotations.count: annotation \"count\" must be text",
				"include.name.required source.includeTopologies[1]: an included topology needs a name",
			},
		},
		{
			name: "lists that are not lists",
			edit: func(document map[string]any) {
				document["edges"] = "none"
				document["templates"] = map[string]any{}
				document["source"] = map[string]any{"kind": "manual", "unresolvedIncludes": "plant"}
			},
			want: []string{
				"document.list.missing edges: edges must be an array",
				"include.list.not-list source.unresolvedIncludes: included topologies must be a list of topology names",
				"template.list.not-list templates: templates must be a list of templates",
			},
		},
		{
			// The editor checks no type of a position, so the decoder's own error is kept.
			name: "a value of the wrong type at a path the editor does not check",
			edit: func(document map[string]any) {
				nodes := document["nodes"].([]any)
				nodes[0].(map[string]any)["position"].(map[string]any)["x"] = "left"
				document["layout"] = 5
			},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := fixture(t)

			test.edit(document)

			data, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("encoding the document: %v", err)
			}

			_, err = builder.Decode(data)
			if err == nil {
				t.Fatal("Decode() error = nil, want an error")
			}

			var invalid *builder.ValidationError

			if test.want == nil {
				var typeErr *json.UnmarshalTypeError
				if errors.As(err, &invalid) || !errors.As(err, &typeErr) {
					t.Errorf("Decode() error = %v, want the decoder's own *json.UnmarshalTypeError", err)
				}

				return
			}

			if !errors.As(err, &invalid) || !errors.Is(err, builder.ErrInvalidDocument) {
				t.Fatalf("Decode() error = %v, want a validation error", err)
			}

			got := make([]string, 0, len(invalid.Issues))

			for _, issue := range invalid.Issues {
				if issue.Severity != builder.SeverityError {
					t.Errorf("Decode() issue %+v severity = %q, want %q", issue, issue.Severity, builder.SeverityError)
				}

				got = append(got, string(issue.Code)+" "+issue.Path+": "+issue.Message)
			}

			slices.Sort(got)

			want := slices.Sorted(slices.Values(test.want))

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Decode() issues mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseValidates(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading document.json: %v", err)
	}

	if _, err := builder.Parse(valid); err != nil {
		t.Errorf("Parse(document.json) error = %v, want nil", err)
	}

	broken := strings.Replace(string(valid), `"id": "`+idNetExp+`"`, `"id": ""`, 1)

	_, err = builder.Parse([]byte(broken))
	if err == nil {
		t.Fatal("Parse(network without an ID) error = nil, want an error")
	}

	if !errors.Is(err, builder.ErrInvalidDocument) {
		t.Errorf("Parse(network without an ID) error = %v, want %v", err, builder.ErrInvalidDocument)
	}

	var validationErr *builder.ValidationError

	if !errors.As(err, &validationErr) || len(validationErr.Issues) == 0 {
		t.Errorf("Parse(network without an ID) error = %v, want a *ValidationError with issues", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Edges[0].Color = "#c0392b"

	data, err := builder.Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if diff := cmp.Diff(doc, decoded); diff != "" {
		t.Errorf("Decode(Encode(doc)) mismatch (-want +got):\n%s", diff)
	}
}

func TestNewDocumentIsValid(t *testing.T) {
	doc := builder.NewDocument("fresh")

	want := builder.Metadata{ID: builder.DocumentID("fresh"), Name: "fresh"}
	if diff := cmp.Diff(want, doc.Metadata); diff != "" {
		t.Errorf("NewDocument(fresh).Metadata mismatch (-want +got):\n%s", diff)
	}

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of NewDocument(fresh) = %v, want nil", err)
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
		t.Errorf("ContentDigest() in another map order = %q, want %q", second, first)
	}

	if !strings.HasPrefix(first, "sha256:") {
		t.Errorf("ContentDigest() = %q, want one prefixed with sha256:", first)
	}
}
