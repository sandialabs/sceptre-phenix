package builder_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"phenix/types/builder"
)

// docsExamples holds the Builder documents the documentation links to, which
// a reader uploads as they are.
var docsExamples = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "..", "docs", "content", "builder", "examples",
)

func testProvenance() builder.Provenance {
	return builder.Provenance{
		CreatedBy: "alice",
		CreatedAt: "2026-10-01T15:04:05Z",
		UpdatedBy: "bob",
		UpdatedAt: "2026-10-01T16:10:00Z",
	}
}

// TestProvenanceRoundTrip checks that the four provenance fields survive strict decoding, follow the
// description, and are left out when unset; the metadata follows the revision.
func TestProvenanceRoundTrip(t *testing.T) {
	plain := loadDocumentFixture(t, "document.json")
	stamped := loadDocumentFixture(t, "document.json")

	if got := plain.Provenance(); got != (builder.Provenance{}) {
		t.Fatalf("the fixture has provenance %+v, want none", got)
	}

	stamped.SetProvenance(testProvenance())

	if got := stamped.Provenance(); got != testProvenance() {
		t.Fatalf("Provenance() = %+v, want %+v", got, testProvenance())
	}

	data, err := builder.Encode(stamped)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !reflect.DeepEqual(stamped, decoded) {
		t.Fatalf("JSON round trip changed the document:\nbefore: %s\nafter: %s",
			asJSON(t, stamped), asJSON(t, decoded))
	}

	// The struct order is the canonical order, which an exported file shows.
	last := -1

	for _, key := range []string{
		"\n  \"revision\"", "\n  \"metadata\"", "\n    \"id\"", "\n    \"name\"", "\n    \"description\"",
		"\n    \"createdBy\"", "\n    \"createdAt\"", "\n    \"updatedBy\"", "\n    \"updatedAt\"", "\n  \"nodes\"",
	} {
		at := strings.Index(string(data), key)
		if at <= last {
			t.Fatalf("%q is not written in metadata order (at %d, after %d):\n%.400s", key, at, last, data)
		}

		last = at
	}

	// Absent, none is written, and removing them gives the plain document.
	stamped.SetProvenance(builder.Provenance{})

	for name, doc := range map[string]*builder.Document{"plain": plain, "cleared": stamped} {
		encoded, err := builder.Encode(doc)
		if err != nil {
			t.Fatalf("Encode(%s): %v", name, err)
		}

		var raw struct {
			Metadata map[string]any `json:"metadata"`
		}

		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatalf("decoding the %s document: %v", name, err)
		}

		for _, key := range []string{"createdBy", "createdAt", "updatedBy", "updatedAt"} {
			if _, ok := raw.Metadata[key]; ok {
				t.Fatalf("the %s document encodes metadata.%s", name, key)
			}
		}
	}
}

// TestProvenanceIsPartOfTheContent checks that a change of any provenance field changes the
// canonical encoding the draft service digests.
func TestProvenanceIsPartOfTheContent(t *testing.T) {
	base := loadDocumentFixture(t, "document.json")
	base.SetProvenance(testProvenance())

	want, err := builder.Encode(base)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for name, change := range map[string]func(*builder.Provenance){
		"createdBy": func(p *builder.Provenance) { p.CreatedBy = "carol" },
		"createdAt": func(p *builder.Provenance) { p.CreatedAt = "2026-10-01T15:04:06Z" },
		"updatedBy": func(p *builder.Provenance) { p.UpdatedBy = "carol" },
		"updatedAt": func(p *builder.Provenance) { p.UpdatedAt = "2026-10-01T16:10:01Z" },
	} {
		doc := loadDocumentFixture(t, "document.json")
		provenance := testProvenance()

		change(&provenance)
		doc.SetProvenance(provenance)

		got, err := builder.Encode(doc)
		if err != nil {
			t.Fatalf("Encode(%s): %v", name, err)
		}

		if string(got) == string(want) {
			t.Fatalf("changing %s did not change the encoding", name)
		}
	}
}

func TestFormatTime(t *testing.T) {
	zone := time.FixedZone("MDT", -6*60*60)

	for _, test := range []struct {
		name string
		time time.Time
		want string
	}{
		{name: "UTC", time: time.Date(2026, 10, 1, 15, 4, 5, 0, time.UTC), want: "2026-10-01T15:04:05Z"},
		{name: "cut to whole seconds", time: time.Date(2026, 10, 1, 15, 4, 5, 999999999, time.UTC), want: "2026-10-01T15:04:05Z"},
		{name: "another zone", time: time.Date(2026, 10, 1, 21, 4, 5, 0, zone), want: "2026-10-02T03:04:05Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := builder.FormatTime(test.time)
			if got != test.want {
				t.Fatalf("FormatTime = %q, want %q", got, test.want)
			}

			// What the service writes is always what the validator accepts.
			doc := builder.NewDocument("times")
			doc.Metadata.CreatedAt, doc.Metadata.UpdatedAt = got, got

			if err := doc.Validate(); err != nil {
				t.Fatalf("a formatted time is refused: %v", err)
			}
		})
	}
}

// TestSchemaProvenanceProperties checks the schema of the four provenance fields: optional strings,
// bounded and patterned as the validator checks them.
func TestSchemaProvenanceProperties(t *testing.T) {
	metadata := mapAt(t, mapAt(t, mustSchema(t), "$defs"), "metadata")
	properties := mapAt(t, metadata, "properties")

	for _, name := range []string{"createdBy", "updatedBy"} {
		property := mapAt(t, properties, name)

		if property["type"] != "string" || property["maxLength"] != builder.MaxUserBytes ||
			property["pattern"] != mapAt(t, properties, "name")["pattern"] {
			t.Fatalf("%s is not bounded text without control characters: %v", name, property)
		}
	}

	for _, name := range []string{"createdAt", "updatedAt"} {
		property := mapAt(t, properties, name)

		if property["type"] != "string" || property["format"] != "date-time" ||
			property["pattern"] != `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$` {
			t.Fatalf("%s is not a time in the fixed form: %v", name, property)
		}
	}

	for _, name := range []string{"createdBy", "createdAt", "updatedBy", "updatedAt"} {
		if containsAny(metadata["required"], name) {
			t.Fatalf("%s is required", name)
		}

		description, _ := mapAt(t, properties, name)["description"].(string)
		if !strings.Contains(description, "the server sets") {
			t.Fatalf("the description of %s does not say who sets it: %q", name, description)
		}
	}
}

// TestSchemaNotesProperty checks the notes schema: a bounded list of bounded text that takes
// newlines and tabs but no other control character, and no blank note.
func TestSchemaNotesProperty(t *testing.T) {
	metadata := mapAt(t, mapAt(t, mustSchema(t), "$defs"), "metadata")
	notes := mapAt(t, mapAt(t, metadata, "properties"), "notes")
	note := mapAt(t, notes, "items")

	if notes["type"] != "array" || notes["maxItems"] != builder.MaxDiagramNotes || containsAny(metadata["required"], "notes") ||
		note["type"] != "string" || note["minLength"] != 1 || note["maxLength"] != builder.MaxDiagramNoteBytes {
		t.Fatalf("notes are not an optional, bounded list of bounded text: %v", notes)
	}

	pattern, ok := note["pattern"].(string)
	if !ok {
		t.Fatalf("a note has no pattern: %v", note)
	}

	matcher := regexp.MustCompile(pattern)

	for _, text := range []string{"one", " padded ", "two\nlines", "a\ttab", "é"} {
		if !matcher.MatchString(text) {
			t.Fatalf("the note pattern rejects %q", text)
		}
	}

	for _, text := range []string{"", " ", "\n\t", "bell\a", "carriage\rreturn", "delete\x7f"} {
		if matcher.MatchString(text) {
			t.Fatalf("the note pattern accepts %q", text)
		}
	}

	// A one-character note matches exactly when Validate accepts it: U+0085, U+00A0 and U+2028 are
	// blank to both, and U+FEFF and the zero width characters are not.
	doc := loadDocumentFixture(t, "document.json")
	spans := [][2]rune{
		{0, 0xff}, {0x1680, 0x1680}, {0x180e, 0x180e}, {0x2000, 0x2010},
		{0x2028, 0x2030}, {0x205f, 0x2060}, {0x3000, 0x3001}, {0xfeff, 0xfeff},
	}

	for _, span := range spans {
		for r := span[0]; r <= span[1]; r++ {
			text := string(r)
			doc.Metadata.Notes = []string{text}
			accepted := doc.Validate() == nil

			if matched := matcher.MatchString(text); matched != accepted {
				t.Errorf("a note of %U: the pattern matches it: %t, Validate accepts it: %t", r, matched, accepted)
			}
		}
	}
}

// TestDocsExamplesParse checks that every Builder document the documentation
// offers for upload is one this package accepts.
func TestDocsExamplesParse(t *testing.T) {
	if _, err := os.Stat(docsExamples); err != nil {
		t.Skipf("no documentation examples here: %v", err)
	}

	examples, err := filepath.Glob(filepath.Join(docsExamples, "*.builder.json"))
	if err != nil {
		t.Fatalf("listing %s: %v", docsExamples, err)
	}

	if len(examples) == 0 {
		t.Fatalf("%s holds no Builder document", docsExamples)
	}

	for _, example := range examples {
		t.Run(filepath.Base(example), func(t *testing.T) {
			data, err := os.ReadFile(example)
			if err != nil {
				t.Fatalf("reading: %v", err)
			}

			if _, err := builder.Parse(data); err != nil {
				t.Fatalf("not a valid Builder document: %v", err)
			}
		})
	}
}
