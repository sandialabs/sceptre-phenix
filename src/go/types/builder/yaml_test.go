package builder_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"phenix/types/builder"
)

// builderExamples are the example Builder documents in testdata/examples.
var builderExamples = []string{ //nolint:gochecknoglobals // test fixture
	"lakeside-treatment.builder.json",
	"pump-station.builder.json",
	"riverside-water.builder.json",
}

// readExample returns a file of testdata/examples.
func readExample(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "examples", name))
	if err != nil {
		t.Fatalf("reading the example: %v", err)
	}

	return data
}

// canonicalDocument returns the canonical JSON of the document in data, which
// is what a document's digest is the digest of.
func canonicalDocument(t *testing.T, data []byte) string {
	t.Helper()

	document, err := builder.Parse(data)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	canonical, err := builder.Encode(document)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	return string(canonical)
}

// TestJSONFromYAMLKeepsTheDocument writes each example document as YAML and
// reads it back: the document, and so its digest, is the JSON file's.
func TestJSONFromYAMLKeepsTheDocument(t *testing.T) {
	t.Parallel()

	for _, name := range builderExamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := readExample(t, name)
			want := canonicalDocument(t, source)

			var generic any
			if err := json.Unmarshal(source, &generic); err != nil {
				t.Fatalf("decoding the example: %v", err)
			}

			text, err := yaml.Marshal(generic)
			if err != nil {
				t.Fatalf("encoding the example as YAML: %v", err)
			}

			if converted, err := builder.JSONFromYAML(text); err != nil {
				t.Errorf("JSONFromYAML(%s as YAML) returned error: %v", name, err)
			} else if diff := cmp.Diff(want, canonicalDocument(t, converted)); diff != "" {
				t.Errorf("canonical JSONFromYAML(%s as YAML) mismatch (-want +got):\n%s", name, diff)
			}

			// The content decides which of the two a file's text is.
			if fromText, err := builder.JSONFromText(text); err != nil {
				t.Errorf("JSONFromText(%s as YAML) returned error: %v", name, err)
			} else if diff := cmp.Diff(want, canonicalDocument(t, fromText)); diff != "" {
				t.Errorf("canonical JSONFromText(%s as YAML) mismatch (-want +got):\n%s", name, diff)
			}

			if asIs, err := builder.JSONFromText(source); err != nil {
				t.Errorf("JSONFromText(%s) returned error: %v", name, err)
			} else if diff := cmp.Diff(string(source), string(asIs)); diff != "" {
				t.Errorf("JSONFromText(%s) mismatch (-want +got), want the text it was given:\n%s", name, diff)
			}

			// JSON is YAML too, so a JSON file is read the same either way.
			if fromJSON, err := builder.JSONFromYAML(source); err != nil {
				t.Errorf("JSONFromYAML(%s) returned error: %v", name, err)
			} else if diff := cmp.Diff(want, canonicalDocument(t, fromJSON)); diff != "" {
				t.Errorf("canonical JSONFromYAML(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// TestJSONFromYAMLKeepsPresentationFields checks that colors (which start with "#"), icon names and
// base64 data survive YAML, so the document and its digest do.
func TestJSONFromYAMLKeepsPresentationFields(t *testing.T) {
	t.Parallel()

	source, err := builder.Encode(decoratedDocument(t))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var generic any
	if err := json.Unmarshal(source, &generic); err != nil {
		t.Fatalf("decoding the document: %v", err)
	}

	text, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the document as YAML: %v", err)
	}

	converted, err := builder.JSONFromYAML(text)
	if err != nil {
		t.Fatalf("JSONFromYAML returned error: %v", err)
	}

	if diff := cmp.Diff(string(source), canonicalDocument(t, converted)); diff != "" {
		t.Errorf("canonical JSONFromYAML(decorated document as YAML) mismatch (-want +got):\n%s", diff)
	}
}

// TestJSONFromYAMLScalars reads scalars the way the Builder reads an
// uploaded YAML file: only null, booleans and numbers are not text.
func TestJSONFromYAMLScalars(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct{ text, want string }{
		"a date stays text":         {"a: 2001-01-01", `{"a":"2001-01-01"}`},
		"a time stays text":         {"a: 2001-01-01T10:00:00Z", `{"a":"2001-01-01T10:00:00Z"}`},
		"yes and no stay text":      {"a: yes\nb: No\nc: on\nd: OFF\ne: y", `{"a":"yes","b":"No","c":"on","d":"OFF","e":"y"}`},
		"booleans":                  {"a: true\nb: False\nc: TRUE", `{"a":true,"b":false,"c":true}`},
		"other spellings of true":   {"a: tRue\nb: truE", `{"a":"tRue","b":"truE"}`},
		"null":                      {"a: ~\nb: null\nc: Null\nd: NULL\ne:", `{"a":null,"b":null,"c":null,"d":null,"e":null}`},
		"other spellings of null":   {"a: nUll\nb: nil", `{"a":"nUll","b":"nil"}`},
		"integers":                  {"a: 12\nb: -7\nc: +3\nd: 0\ne: -0", `{"a":12,"b":-7,"c":3,"d":0,"e":0}`},
		"a leading zero is decimal": {"a: 012\nb: 0089", `{"a":12,"b":89}`},
		"prefixed integers":         {"a: 0x1F\nb: 0o17\nc: 0b101\nd: -0x10", `{"a":31,"b":15,"c":5,"d":-16}`},
		"prefixes are lower case":   {"a: 0X1F\nb: 0O17\nc: 0B101", `{"a":"0X1F","b":"0O17","c":"0B101"}`},
		"underscores make text":     {"a: 1_000\nb: 1_0.5", `{"a":"1_000","b":"1_0.5"}`},
		"a prefix without digits":   {"a: 0x\nb: 0b2\nc: 0o8\nd: +", `{"a":"0x","b":"0b2","c":"0o8","d":"+"}`},
		"fractions": {
			"a: 1.5\nb: -0.25\nc: .5\nd: 1.\ne: 1e3\nf: 2.5E-1",
			`{"a":1.5,"b":-0.25,"c":0.5,"d":1,"e":1000,"f":0.25}`,
		},
		"negative zero is zero": {"a: -0.0\nb: -0e5", `{"a":0,"b":0}`},
		"a signed leading dot":  {"a: +.5\nb: -.5", `{"a":"+.5","b":"-.5"}`},
		"a version stays text": {
			"a: 1.2.3\nb: 10.0.0.1\nc: 00:11:22:33:44:55",
			`{"a":"1.2.3","b":"10.0.0.1","c":"00:11:22:33:44:55"}`,
		},
		"sexagesimal stays text":      {"a: 1:30\nb: 190:20:30", `{"a":"1:30","b":"190:20:30"}`},
		"a long integer is a float":   {"a: 12345678901234567890", `{"a":12345678901234567000}`},
		"a number too large is text":  {"a: 1e999\nb: " + strings.Repeat("9", 400), `{"a":"1e999","b":"` + strings.Repeat("9", 400) + `"}`},
		"quoted scalars are text":     {`a: "12"` + "\nb: 'true'\nc: \"\"\nd: '~'", `{"a":"12","b":"true","c":"","d":"~"}`},
		"block scalars are text":      {"a: |\n  12\nb: >\n  true\n", `{"a":"12\n","b":"true\n"}`},
		"an environment name is text": {"a: ${HOME}/x", `{"a":"${HOME}/x"}`},
		"explicit tags": {
			`a: !!str 12` + "\nb: !!int \"12\"\nc: !!float 3\nd: !!bool \"true\"\ne: !!null ~",
			`{"a":"12","b":12,"c":3,"d":true,"e":null}`,
		},
		"keys are text": {
			"1.0: a\ntrue: b\n~: c\n0x10: e\n2001-01-01: f\n\"x y\": g\n1e21: h",
			`{"1":"a","16":"e","1e+21":"h","2001-01-01":"f","null":"c","true":"b","x y":"g"}`,
		},
		"a quoted merge key is a key": {`"<<": 1`, `{"<<":1}`},
		"lists and maps":              {"a:\n  - 1\n  - b: [x, {c: 2}]\n", `{"a":[1,{"b":["x",{"c":2}]}]}`},
		"a list":                      {"- 1\n- a", `[1,"a"]`},
		"JSON": {
			`{"a": [1, 2.5, "x", true, null], "b": {"c": "2001-01-01"}}`,
			`{"a":[1,2.5,"x",true,null],"b":{"c":"2001-01-01"}}`,
		},
		"a byte order mark": {"\ufeffa: 1", `{"a":1}`},
		"a document marker": {"---\na: 1\n...\n", `{"a":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := builder.JSONFromYAML([]byte(test.text))
			if err != nil {
				t.Fatalf("JSONFromYAML returned error: %v", err)
			}

			if diff := cmp.Diff(test.want, string(got)); diff != "" {
				t.Errorf("JSONFromYAML(%q) mismatch (-want +got):\n%s", test.text, diff)
			}
		})
	}
}

// TestJSONFromYAMLLongIntegers reads a plain integer as a number while a float64 holds it, and as
// text from the first that is too large, however many digits it has.
func TestJSONFromYAMLLongIntegers(t *testing.T) {
	t.Parallel()

	power := func(exponent uint) string {
		return new(big.Int).Lsh(big.NewInt(1), exponent).String()
	}

	for name, test := range map[string]struct {
		text   string
		number bool
	}{
		"1e308 written out":                  {"1" + strings.Repeat("0", 308), true},
		"1e309 written out":                  {"1" + strings.Repeat("0", 309), false},
		"2 to the power 1023":                {power(1023), true},
		"2 to the power 1024":                {power(1024), false},
		"negative 2 to the power 1023":       {"-" + power(1023), true},
		"negative 2 to the power 1024":       {"-" + power(1024), false},
		"zeros before a digit":               {strings.Repeat("0", 5000) + "7", true},
		"zeros only":                         {strings.Repeat("0", 5000), true},
		"255 hexadecimal digits":             {"0x" + strings.Repeat("f", 255), true},
		"257 hexadecimal digits":             {"0x" + strings.Repeat("f", 257), false},
		"1024 binary digits":                 {"0b1" + strings.Repeat("0", 1023), true},
		"1025 binary digits":                 {"0b1" + strings.Repeat("0", 1024), false},
		"zeros before 1024 binary digits":    {"0b" + strings.Repeat("0", 2000) + "1" + strings.Repeat("0", 1023), true},
		"more octal digits than a float has": {"0o" + strings.Repeat("7", 400), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := builder.JSONFromYAML([]byte("a: " + test.text))
			if err != nil {
				t.Fatalf("JSONFromYAML returned error: %v", err)
			}

			var read map[string]any
			if err := json.Unmarshal(got, &read); err != nil {
				t.Fatalf("decoding %s: %v", got, err)
			}

			switch value := read["a"].(type) {
			case float64:
				if !test.number {
					t.Errorf("JSONFromYAML(a: %s) read a as the number %v, want the scalar as text", name, value)
				}
			case string:
				if test.number {
					t.Errorf("JSONFromYAML(a: %s) read a as text, want a number", name)
				} else if value != test.text {
					t.Errorf("JSONFromYAML(a: %s) read a as text of %d bytes, want the %d bytes of the scalar",
						name, len(value), len(test.text))
				}
			default:
				t.Errorf("JSONFromYAML(a: %s) read a as %T, want a number or text", name, value)
			}
		})
	}
}

// TestJSONFromYAMLRefusals refuses YAML a Builder document may not use, with
// an error that says what and where and never repeats a value of the text.
func TestJSONFromYAMLRefusals(t *testing.T) {
	t.Parallel()

	const marker = "MARKER-VALUE"

	for name, test := range map[string]struct{ text, reason string }{
		"an alias":                    {"a: &x {b: " + marker + "}\nc: *x\n", "line 1: a Builder document cannot have an anchor (&name)"},
		"an alias of a scalar":        {"a:\n  - &x " + marker + "\n  - *x\n", "line 2: a Builder document cannot have an anchor (&name)"},
		"an alias as a key":           {"? &x " + marker + "\n: 1\n*x : 2\n", "an anchor (&name)"},
		"an anchor":                   {"a: &x 1\nb: " + marker + "\n", "line 1: a Builder document cannot have an anchor (&name)"},
		"an anchor on a key":          {"&x a: " + marker + "\n", "line 1: a Builder document cannot have an anchor (&name)"},
		"a merge key":                 {"a: {b: " + marker + "}\nc:\n  <<: {d: 1}\n", "line 3: a Builder document cannot have a merge key (<<)"},
		"a second document":           {"a: " + marker + "\n---\nb: 2\n", "a second document"},
		"an empty second document":    {"a: " + marker + "\n---\n", "a second document"},
		"a malformed second document": {"a: " + marker + "\n---\nb: [\n", "line 3: did not find expected node content"},
		"a map as a key":              {"? {a: " + marker + "}\n: 1\n", "line 1: a Builder document cannot have a key that is a map or a list"},
		"a list as a key":             {"? [" + marker + "]\n: 1\n", "line 1: a Builder document cannot have a key that is a map or a list"},
		"a key used twice":            {"a: 1\nb: " + marker + "\na: 2\n", "line 3: a Builder document cannot have a key used twice in one map"},
		"keys that read the same":     {"1: " + marker + "\n\"1\": 2\n", "line 2: a Builder document cannot have a key used twice in one map"},
		"numbers that read the same":  {"1: " + marker + "\n1.0: 2\n", "line 2: a Builder document cannot have a key used twice in one map"},
		"infinity":                    {"a: .inf\nb: " + marker + "\n", "line 1: a Builder document cannot have an infinite number or one that is not a number"},
		"negative infinity":           {"a: " + marker + "\nb: -.Inf\n", "line 2: a Builder document cannot have an infinite number or one that is not a number"},
		"not a number":                {"a: .NaN\nb: " + marker + "\n", "line 1: a Builder document cannot have an infinite number or one that is not a number"},
		"a tagged infinity":           {"a: !!float .inf\nb: " + marker + "\n", "line 1: a Builder document cannot have an infinite number or one that is not a number"},
		"a binary value":              {"a: !!binary aGVsbG8=\nb: " + marker + "\n", "line 1: a Builder document cannot have the tag of this value"},
		"a tag of the document":       {"a: !secret " + marker + "\n", "line 1: a Builder document cannot have the tag of this value"},
		"a tag that does not match":   {"a: !!int " + marker + "\n", "line 1: a Builder document cannot have the tag of this value"},
		"a tagged map":                {"a: !!set {" + marker + "}\n", "line 1: a Builder document cannot have the tag of this map"},
		"a tagged list":               {"a: !!omap [" + marker + ": 1]\n", "line 1: a Builder document cannot have the tag of this list"},
		"no document":                 {"", "the text holds no document"},
		"only a comment":              {"# " + marker + "\n", "the text holds no document"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := builder.JSONFromYAML([]byte(test.text))
			if !errors.Is(err, builder.ErrUnsupportedYAML) {
				t.Fatalf("JSONFromYAML(%q) = %s, %v, want an error matching ErrUnsupportedYAML", test.text, got, err)
			}

			if !strings.Contains(err.Error(), test.reason) {
				t.Errorf("JSONFromYAML(%q) error = %q, want it to say %q", test.text, err, test.reason)
			}

			if strings.Contains(err.Error(), marker) {
				t.Errorf("JSONFromYAML(%q) error = %q, want it not to repeat a value of the text", test.text, err)
			}
		})
	}

	// Text that is not YAML at all is refused by the YAML reader itself.
	if got, err := builder.JSONFromYAML([]byte("a: [1, 2\nb: }")); !errors.Is(err, builder.ErrUnsupportedYAML) {
		t.Errorf("JSONFromYAML(malformed YAML) = %s, %v, want an error matching ErrUnsupportedYAML", got, err)
	}

	// Text that is not JSON is read as YAML, which is laxer about commas.
	if got, err := builder.JSONFromText([]byte(`{"a": 1,}`)); err != nil || string(got) != `{"a":1}` {
		t.Errorf(`JSONFromText({"a": 1,}) = %s, %v, want {"a":1}, read as YAML`, got, err)
	}

	if got, err := builder.JSONFromText(nil); !errors.Is(err, builder.ErrUnsupportedYAML) {
		t.Errorf("JSONFromText(nil) = %s, %v, want an error matching ErrUnsupportedYAML", got, err)
	}
}

// TestJSONFromYAMLDoesNotExpandAliases refuses a document of nested aliases
// without building what they expand to.
func TestJSONFromYAMLDoesNotExpandAliases(t *testing.T) {
	t.Parallel()

	var text strings.Builder

	text.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")

	for i := 1; i < 9; i++ {
		previous := "*a" + string(rune('0'+i-1))

		text.WriteString("a" + string(rune('0'+i)) + ": &a" + string(rune('0'+i)) + " [" +
			strings.TrimSuffix(strings.Repeat(previous+", ", 10), ", ") + "]\n")
	}

	if got, err := builder.JSONFromYAML([]byte(text.String())); !errors.Is(err, builder.ErrUnsupportedYAML) {
		t.Errorf("JSONFromYAML(nested aliases) = %d bytes, %v, want an error matching ErrUnsupportedYAML", len(got), err)
	}
}

// yamlOfJSON returns JSON text written as YAML.
func yamlOfJSON(t *testing.T, data []byte) []byte {
	t.Helper()

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("decoding JSON: %v", err)
	}

	text, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding YAML: %v", err)
	}

	return text
}

// TestFileKindOfDocument tells Builder documents from phenix configs by what
// the text holds, as JSON and as YAML, whatever else is wrong with it.
func TestFileKindOfDocument(t *testing.T) {
	t.Parallel()

	for _, name := range builderExamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data := readExample(t, name)

			if got := builder.FileKindOf(data); got != builder.FileKindDocument {
				t.Errorf("FileKindOf(%s) = %q, want %q", name, got, builder.FileKindDocument)
			}

			if got := builder.FileKindOf(yamlOfJSON(t, data)); got != builder.FileKindDocument {
				t.Errorf("FileKindOf(%s as YAML) = %q, want %q", name, got, builder.FileKindDocument)
			}
		})
	}

	for _, name := range []string{"pump-station.topology.yaml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := builder.FileKindOf(readExample(t, name)); got != builder.FileKindOther {
				t.Errorf("FileKindOf(%s) = %q, want %q for a topology config", name, got, builder.FileKindOther)
			}
		})
	}

	for name, test := range map[string]struct {
		text string
		want builder.FileKind
	}{
		"JSON with only the schema":          {`{"$schema": "https://phenix.sandia.gov/schemas/builder/v1"}`, builder.FileKindDocument},
		"a later revision of the schema":     {`{"$schema": "https://phenix.sandia.gov/schemas/builder/v2", "revision": 2}`, builder.FileKindDocument},
		"YAML with only the schema":          {"$schema: https://phenix.sandia.gov/schemas/builder/v1\n", builder.FileKindDocument},
		"YAML a document may not use":        {"$schema: https://phenix.sandia.gov/schemas/builder/v1\nnodes: &n []\nedges: *n\n", builder.FileKindDocument},
		"a document that is not valid":       {`{"$schema": "https://phenix.sandia.gov/schemas/builder/v1", "nodes": 4}`, builder.FileKindDocument},
		"JSON with a kind":                   {`{"$schema": "https://phenix.sandia.gov/schemas/builder/v1", "kind": "Topology"}`, builder.FileKindOther},
		"YAML with a kind":                   {"$schema: https://phenix.sandia.gov/schemas/builder/v1\nkind: Topology\n", builder.FileKindOther},
		"YAML with a null kind":              {"$schema: https://phenix.sandia.gov/schemas/builder/v1\nkind:\n", builder.FileKindOther},
		"another schema":                     {`{"$schema": "https://json-schema.org/draft/2020-12/schema"}`, builder.FileKindOther},
		"a schema that is not text":          {`{"$schema": 1}`, builder.FileKindOther},
		"a YAML schema that is not text":     {"$schema: [a]\n", builder.FileKindOther},
		"a topology config":                  {"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: x\n", builder.FileKindOther},
		"a JSON list":                        {`[{"$schema": "https://phenix.sandia.gov/schemas/builder/v1"}]`, builder.FileKindOther},
		"a YAML list":                        {"- $schema: https://phenix.sandia.gov/schemas/builder/v1\n", builder.FileKindOther},
		"text":                               {"https://phenix.sandia.gov/schemas/builder/v1", builder.FileKindOther},
		"nothing":                            {"", builder.FileKindOther},
		"text that is neither JSON nor YAML": {"{\"$schema\": \"https://phenix.sandia.gov/schemas/builder/v1\"", builder.FileKindOther},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := builder.FileKindOf([]byte(test.text)); got != test.want {
				t.Errorf("FileKindOf(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
