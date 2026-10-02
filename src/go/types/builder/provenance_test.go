package builder_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"phenix/types/builder"
)

// docsExamples holds the Builder documents the documentation links to, which
// a reader uploads as they are.
var docsExamples = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "..", "docs", "content", "builder-v2", "examples",
)

func testProvenance() builder.Provenance {
	return builder.Provenance{
		Author:    "alice",
		CreatedAt: "2026-10-01T15:04:05Z",
		UpdatedBy: "bob",
		UpdatedAt: "2026-10-01T16:10:00Z",
	}
}

// TestProvenanceRoundTrip covers the four header fields the draft service
// sets: they survive encoding and strict decoding, are written after the
// description and before the nodes, and are left out when the document has
// none.
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
		`"description"`, `"author"`, `"createdAt"`, `"updatedBy"`, `"updatedAt"`, `"nodes"`,
	} {
		at := strings.Index(string(data), "\n  "+key)
		if at <= last {
			t.Fatalf("%s is not written in header order (at %d, after %d):\n%.400s", key, at, last, data)
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

		for _, key := range []string{`"author"`, `"createdAt"`, `"updatedBy"`, `"updatedAt"`} {
			// source.updatedAt is another field, indented further.
			if strings.Contains(string(encoded), "\n  "+key) {
				t.Fatalf("the %s document encodes %s", name, key)
			}
		}
	}
}

// TestProvenanceIsPartOfTheContent checks that a change of any of the four
// fields changes the canonical encoding, which is what the draft service
// digests.
func TestProvenanceIsPartOfTheContent(t *testing.T) {
	base := loadDocumentFixture(t, "document.json")
	base.SetProvenance(testProvenance())

	want, err := builder.Encode(base)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for name, change := range map[string]func(*builder.Provenance){
		"author":    func(p *builder.Provenance) { p.Author = "carol" },
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
			doc.CreatedAt, doc.UpdatedAt = got, got

			if err := doc.Validate(); err != nil {
				t.Fatalf("a formatted time is refused: %v", err)
			}
		})
	}
}

// TestValidateProvenance covers each rule of the four header fields, and the
// path its issue is reported at.
func TestValidateProvenance(t *testing.T) {
	longest := strings.Repeat("a", builder.MaxUserBytes)

	users := []struct {
		name  string
		value string
		want  string
	}{
		{name: "none", value: "", want: ""},
		{name: "a user", value: "alice", want: ""},
		{name: "a name no phenix user has", value: "Alice Smith <alice@example.com>", want: ""},
		{name: "the longest", value: longest, want: ""},
		{name: "outside ASCII", value: "ålice", want: ""},
		{name: "one byte too long", value: longest + "a", want: "must be at most 256 bytes"},
		{
			name:  "too long in bytes, not in characters",
			value: strings.Repeat("€", builder.MaxUserBytes/3+1),
			want:  "must be at most 256 bytes",
		},
		{name: "a newline", value: "alice\nadmin", want: "must not contain control characters"},
		{name: "a NUL", value: "alice\x00", want: "must not contain control characters"},
		{name: "a delete character", value: "alice\x7f", want: "must not contain control characters"},
	}

	times := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "none", value: "", valid: true},
		{name: "whole seconds in UTC", value: "2026-10-01T15:04:05Z", valid: true},
		{name: "a leap day", value: "2024-02-29T00:00:00Z", valid: true},
		{name: "the last second of a day", value: "2026-12-31T23:59:59Z", valid: true},
		{name: "an offset of zero", value: "2026-10-01T15:04:05+00:00", valid: false},
		{name: "another offset", value: "2026-10-01T09:04:05-06:00", valid: false},
		{name: "a fraction of a second", value: "2026-10-01T15:04:05.5Z", valid: false},
		{name: "a fraction of zero", value: "2026-10-01T15:04:05.000Z", valid: false},
		{name: "a comma fraction", value: "2026-10-01T15:04:05,5Z", valid: false},
		{name: "a space for the T", value: "2026-10-01 15:04:05Z", valid: false},
		{name: "a lower case t", value: "2026-10-01t15:04:05Z", valid: false},
		{name: "a lower case z", value: "2026-10-01T15:04:05z", valid: false},
		{name: "no zone", value: "2026-10-01T15:04:05", valid: false},
		{name: "February 30", value: "2026-02-30T00:00:00Z", valid: false},
		{name: "February 29 outside a leap year", value: "2026-02-29T00:00:00Z", valid: false},
		{name: "hour 24", value: "2026-10-01T24:00:00Z", valid: false},
		{name: "a leap second", value: "2026-12-31T23:59:60Z", valid: false},
		{name: "month 13", value: "2026-13-01T00:00:00Z", valid: false},
		{name: "a date only", value: "2026-10-01", valid: false},
		{name: "a one digit day", value: "2026-10-1T15:04:05Z", valid: false},
		{name: "a two digit year", value: "26-10-01T15:04:05Z", valid: false},
		{name: "space around it", value: " 2026-10-01T15:04:05Z ", valid: false},
		{name: "a trailing newline", value: "2026-10-01T15:04:05Z\n", valid: false},
		{name: "text", value: "yesterday", valid: false},
	}

	for _, field := range []struct {
		path string
		set  func(*builder.Document, string)
	}{
		{path: "author", set: func(d *builder.Document, v string) { d.Author = v }},
		{path: "updatedBy", set: func(d *builder.Document, v string) { d.UpdatedBy = v }},
	} {
		for _, test := range users {
			t.Run(field.path+"/"+test.name, func(t *testing.T) {
				doc := loadDocumentFixture(t, "document.json")
				field.set(doc, test.value)

				assertHeaderIssue(t, doc.Validate(), field.path, test.want)
			})
		}
	}

	for _, field := range []struct {
		path string
		set  func(*builder.Document, string)
	}{
		{path: "createdAt", set: func(d *builder.Document, v string) { d.CreatedAt = v }},
		{path: "updatedAt", set: func(d *builder.Document, v string) { d.UpdatedAt = v }},
	} {
		for _, test := range times {
			t.Run(field.path+"/"+test.name, func(t *testing.T) {
				doc := loadDocumentFixture(t, "document.json")
				field.set(doc, test.value)

				want := ""
				if !test.valid {
					want = "must be a UTC time in the form YYYY-MM-DDTHH:MM:SSZ"
				}

				assertHeaderIssue(t, doc.Validate(), field.path, want)
			})
		}
	}
}

// TestValidateProvenanceHasNoPairingRule checks that the four fields are
// independent: one without the others is valid, and so is a creation time
// after the last edit, which a document made on another server may hold.
func TestValidateProvenanceHasNoPairingRule(t *testing.T) {
	for name, provenance := range map[string]builder.Provenance{
		"author only":             {Author: "alice", CreatedAt: "", UpdatedBy: "", UpdatedAt: ""},
		"createdAt only":          {Author: "", CreatedAt: "2026-10-01T15:04:05Z", UpdatedBy: "", UpdatedAt: ""},
		"updatedBy only":          {Author: "", CreatedAt: "", UpdatedBy: "bob", UpdatedAt: ""},
		"updatedAt only":          {Author: "", CreatedAt: "", UpdatedBy: "", UpdatedAt: "2026-10-01T15:04:05Z"},
		"created after last edit": {Author: "", CreatedAt: "2026-10-02T00:00:00Z", UpdatedBy: "", UpdatedAt: "2026-10-01T00:00:00Z"},
	} {
		doc := loadDocumentFixture(t, "document.json")
		doc.SetProvenance(provenance)

		if err := doc.Validate(); err != nil {
			t.Fatalf("%s is refused: %v", name, err)
		}
	}
}

// assertHeaderIssue checks that err reports exactly one issue at path, whose
// message ends with want, or, when want is empty, that the document is valid.
func assertHeaderIssue(t *testing.T, err error, path, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("refused: %v", err)
		}

		return
	}

	var invalid *builder.ValidationError

	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want a validation error at %s", err, path)
	}

	if len(invalid.Issues) != 1 || invalid.Issues[0].Path != path ||
		invalid.Issues[0].Message != path+" "+want {
		t.Fatalf("issues = %+v, want one at %s: %q", invalid.Issues, path, path+" "+want)
	}
}

// TestSchemaProvenanceProperties checks the schema of the four header fields:
// optional strings, bounded and patterned as the validator checks them.
func TestSchemaProvenanceProperties(t *testing.T) {
	schema := mustSchema(t)
	properties := mapAt(t, schema, "properties")

	for _, name := range []string{"author", "updatedBy"} {
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

	for _, name := range []string{"author", "createdAt", "updatedBy", "updatedAt"} {
		if containsAny(schema["required"], name) {
			t.Fatalf("%s is required", name)
		}

		description, _ := mapAt(t, properties, name)["description"].(string)
		if !strings.Contains(description, "The server sets it") {
			t.Fatalf("the description of %s does not say who sets it: %q", name, description)
		}
	}
}

// TestDocumentFieldsMatchSchemaProperties ties the document struct to the
// hand-built schema: both decode strictly, so a field one of them lacks is a
// document the other refuses.
func TestDocumentFieldsMatchSchemaProperties(t *testing.T) {
	document := reflect.TypeFor[builder.Document]()

	var (
		fields   = make([]string, 0, document.NumField())
		optional []string
	)

	for i := range document.NumField() {
		tag := document.Field(i).Tag.Get("json")

		name, options, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s has no JSON name", document.Field(i).Name)
		}

		fields = append(fields, name)

		if strings.Contains(options, "omitempty") {
			optional = append(optional, name)
		}
	}

	schema := mustSchema(t)
	properties := mapAt(t, schema, "properties")

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}

	slices.Sort(names)

	sorted := slices.Clone(fields)
	slices.Sort(sorted)

	if !slices.Equal(sorted, names) {
		t.Fatalf("document fields %v are not the schema's root properties %v", sorted, names)
	}

	// A field left out when empty cannot be required, and the others are
	// always written.
	for _, name := range fields {
		if required := containsAny(schema["required"], name); required == slices.Contains(optional, name) {
			t.Fatalf("%s: required = %t, but the struct omits it when empty = %t",
				name, required, slices.Contains(optional, name))
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
