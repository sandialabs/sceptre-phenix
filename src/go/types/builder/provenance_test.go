package builder_test

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
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
// description, and are left out when unset. The metadata follows the revision.
func TestProvenanceRoundTrip(t *testing.T) {
	plain := loadDocumentFixture(t, "document.json")
	stamped := loadDocumentFixture(t, "document.json")

	if diff := cmp.Diff(builder.Provenance{}, plain.Provenance()); diff != "" {
		t.Errorf("Provenance() of document.json mismatch (-want +got):\n%s", diff)
	}

	stamped.SetProvenance(testProvenance())

	if diff := cmp.Diff(testProvenance(), stamped.Provenance()); diff != "" {
		t.Errorf("Provenance() after SetProvenance mismatch (-want +got):\n%s", diff)
	}

	data, err := builder.Encode(stamped)
	if err != nil {
		t.Fatalf("Encode(stamped): %v", err)
	}

	decoded, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse(Encode(stamped)): %v", err)
	}

	if diff := cmp.Diff(stamped, decoded); diff != "" {
		t.Errorf("Parse(Encode(stamped)) mismatch (-want +got):\n%s", diff)
	}

	// The struct order is the canonical order, which an exported file shows.
	last := -1

	for _, key := range []string{
		"\n  \"revision\"", "\n  \"metadata\"", "\n    \"id\"", "\n    \"name\"", "\n    \"description\"",
		"\n    \"createdBy\"", "\n    \"createdAt\"", "\n    \"updatedBy\"", "\n    \"updatedAt\"", "\n  \"nodes\"",
	} {
		at := strings.Index(string(data), key)
		if at <= last {
			t.Errorf("Encode(stamped) writes %q at %d, want it after %d in metadata order:\n%.400s", key, at, last, data)
		}

		last = at
	}

	// Absent, none is written, and removing them gives the plain document.
	stamped.SetProvenance(builder.Provenance{})

	for name, doc := range map[string]*builder.Document{"plain": plain, "cleared": stamped} {
		t.Run(name, func(t *testing.T) {
			encoded, err := builder.Encode(doc)
			if err != nil {
				t.Fatalf("Encode(%s): %v", name, err)
			}

			var raw struct {
				Metadata map[string]any `json:"metadata"`
			}

			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatalf("decoding Encode(%s): %v", name, err)
			}

			for _, key := range []string{"createdBy", "createdAt", "updatedBy", "updatedAt"} {
				if value, ok := raw.Metadata[key]; ok {
					t.Errorf("Encode(%s) metadata.%s = %v, want it left out", name, key, value)
				}
			}
		})
	}
}

// TestProvenanceIsPartOfTheContent checks that a change of any provenance field changes the
// canonical encoding the draft service digests.
func TestProvenanceIsPartOfTheContent(t *testing.T) {
	base := loadDocumentFixture(t, "document.json")
	base.SetProvenance(testProvenance())

	want, err := builder.Encode(base)
	if err != nil {
		t.Fatalf("Encode(base): %v", err)
	}

	for name, change := range map[string]func(*builder.Provenance){
		"createdBy": func(p *builder.Provenance) { p.CreatedBy = "carol" },
		"createdAt": func(p *builder.Provenance) { p.CreatedAt = "2026-10-01T15:04:06Z" },
		"updatedBy": func(p *builder.Provenance) { p.UpdatedBy = "carol" },
		"updatedAt": func(p *builder.Provenance) { p.UpdatedAt = "2026-10-01T16:10:01Z" },
	} {
		t.Run(name, func(t *testing.T) {
			doc := loadDocumentFixture(t, "document.json")
			provenance := testProvenance()

			change(&provenance)
			doc.SetProvenance(provenance)

			got, err := builder.Encode(doc)
			if err != nil {
				t.Fatalf("Encode(doc with another %s): %v", name, err)
			}

			if bytes.Equal(got, want) {
				t.Errorf("Encode(doc with another %s) = the encoding of base, want another", name)
			}
		})
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
				t.Errorf("FormatTime(%v) = %q, want %q", test.time, got, test.want)
			}

			// What the service writes is always what the validator accepts.
			doc := builder.NewDocument("times")
			doc.Metadata.CreatedAt, doc.Metadata.UpdatedAt = got, got

			if err := doc.Validate(); err != nil {
				t.Errorf("Validate() with createdAt and updatedAt %q error = %v, want nil", got, err)
			}
		})
	}
}

// TestSchemaProvenanceProperties checks the schema of the four provenance fields: optional strings,
// bounded and patterned as the validator checks them.
func TestSchemaProvenanceProperties(t *testing.T) {
	metadata := mapAt(t, mapAt(t, mustSchema(t), "$defs"), "metadata")
	properties := mapAt(t, metadata, "properties")

	// Bounded text without control characters.
	wantUser := map[string]any{
		"type": "string", "maxLength": builder.MaxUserBytes, "pattern": mapAt(t, properties, "name")["pattern"],
	}

	for _, name := range []string{"createdBy", "updatedBy"} {
		property := mapAt(t, properties, name)
		got := map[string]any{"type": property["type"], "maxLength": property["maxLength"], "pattern": property["pattern"]}

		if diff := cmp.Diff(wantUser, got); diff != "" {
			t.Errorf("Schema() metadata.%s mismatch (-want +got):\n%s", name, diff)
		}
	}

	// A time in the fixed form.
	wantTime := map[string]any{
		"type": "string", "format": "date-time",
		"pattern": `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`,
	}

	for _, name := range []string{"createdAt", "updatedAt"} {
		property := mapAt(t, properties, name)
		got := map[string]any{"type": property["type"], "format": property["format"], "pattern": property["pattern"]}

		if diff := cmp.Diff(wantTime, got); diff != "" {
			t.Errorf("Schema() metadata.%s mismatch (-want +got):\n%s", name, diff)
		}
	}

	for _, name := range []string{"createdBy", "createdAt", "updatedBy", "updatedAt"} {
		if containsAny(metadata["required"], name) {
			t.Errorf("Schema() metadata.required = %v, want no %s", metadata["required"], name)
		}

		description, _ := mapAt(t, properties, name)["description"].(string)
		if !strings.Contains(description, " sets ") {
			t.Errorf("Schema() metadata.%s description = %q, want one that says the server sets it", name, description)
		}
	}
}

// TestSchemaNotesProperty checks the notes schema: a bounded list of bounded text that takes
// newlines and tabs but no other control character, and no blank note.
func TestSchemaNotesProperty(t *testing.T) {
	metadata := mapAt(t, mapAt(t, mustSchema(t), "$defs"), "metadata")
	notes := mapAt(t, mapAt(t, metadata, "properties"), "notes")
	note := mapAt(t, notes, "items")

	// A bounded list of bounded text.
	want := map[string]any{
		"type": "array", "maxItems": builder.MaxDiagramNotes,
		"items.type": "string", "items.minLength": 1, "items.maxLength": builder.MaxDiagramNoteBytes,
	}
	got := map[string]any{
		"type": notes["type"], "maxItems": notes["maxItems"],
		"items.type": note["type"], "items.minLength": note["minLength"], "items.maxLength": note["maxLength"],
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Schema() metadata.notes mismatch (-want +got):\n%s", diff)
	}

	if containsAny(metadata["required"], "notes") {
		t.Errorf("Schema() metadata.required = %v, want no notes", metadata["required"])
	}

	pattern, ok := note["pattern"].(string)
	if !ok {
		t.Fatalf("Schema() metadata.notes.items.pattern = %v, want a string", note["pattern"])
	}

	matcher := regexp.MustCompile(pattern)

	for _, text := range []string{"one", " padded ", "two\nlines", "a\ttab", "é"} {
		if !matcher.MatchString(text) {
			t.Errorf("note pattern MatchString(%q) = false, want true", text)
		}
	}

	for _, text := range []string{"", " ", "\n\t", "bell\a", "carriage\rreturn", "delete\x7f"} {
		if matcher.MatchString(text) {
			t.Errorf("note pattern MatchString(%q) = true, want false", text)
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
				t.Errorf("note pattern MatchString(%U) = %t, want %t as Validate accepts it", r, matched, accepted)
			}
		}
	}
}
