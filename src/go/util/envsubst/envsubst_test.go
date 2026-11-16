package envsubst_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/envsubst"
)

// envsubstLookup returns a Lookup that reads values.
func envsubstLookup(values map[string]string) envsubst.Lookup {
	return func(name string) (string, bool) {
		value, ok := values[name]

		return value, ok
	}
}

func TestTokens(t *testing.T) {
	t.Parallel()

	const text = "${A} ${B_2:x:y} $C ${D-e} ${}\n${F:} $${G}\n$$${H:d}"

	want := []envsubst.Token{
		{Start: 0, End: 4, Line: 1, Raw: "${A}", Name: "A", Default: "", HasDefault: false, Escaped: false},
		{Start: 5, End: 15, Line: 1, Raw: "${B_2:x:y}", Name: "B_2", Default: "x:y", HasDefault: true, Escaped: false},
		{Start: 30, End: 35, Line: 2, Raw: "${F:}", Name: "F", Default: "", HasDefault: true, Escaped: false},
		{Start: 36, End: 41, Line: 2, Raw: "$${G}", Name: "G", Default: "", HasDefault: false, Escaped: true},
		{Start: 43, End: 50, Line: 3, Raw: "$${H:d}", Name: "H", Default: "d", HasDefault: true, Escaped: true},
	}

	if diff := cmp.Diff(want, envsubst.Tokens([]byte(text))); diff != "" {
		t.Errorf("Tokens(%q) mismatch (-want +got):\n%s", text, diff)
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()

	lookup := envsubstLookup(map[string]string{"SET": "value", "EMPTY": "", "NESTED": "v:${SET}"})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "a name", input: "name: ${SET}-topo", want: "name: value-topo"},
		{name: "a set name with a default", input: "${SET:fallback}", want: "value"},
		{name: "a name set to the empty string, with a default", input: "[${EMPTY:fallback}]", want: "[]"},
		{name: "an unset name with a default", input: "${UNSET:fallback}", want: "fallback"},
		{name: "an unset name with an empty default", input: "[${UNSET:}]", want: "[]"},
		{name: "an unset name", input: "[${UNSET}]", want: "[]"},
		{
			name:  "text without placeholders",
			input: "name: $SET {x} ${} ${A-B} $${",
			want:  "name: $SET {x} ${} ${A-B} $${",
		},
		{name: "several placeholders", input: "${SET}/${UNSET:d}", want: "value/d"},
		{name: "a value that holds a placeholder", input: "${NESTED}", want: "v:${SET}"},
		{name: "an escaped set name", input: "$${SET}", want: "${SET}"},
		{name: "an escaped name with a default", input: "$${UNSET:d}", want: "${UNSET:d}"},
		{name: "an escape after a dollar sign", input: "$$${SET}", want: "$${SET}"},
		{name: "an escape next to a placeholder", input: "a$${SET}b${SET}", want: "a${SET}bvalue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := string(envsubst.Expand([]byte(tt.input), lookup)); got != tt.want {
				t.Errorf("Expand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandOnly(t *testing.T) {
	t.Parallel()

	set := envsubstLookup(map[string]string{"BRANCH_NAME": "lab", "SECRET": "server-secret"})
	unset := envsubstLookup(map[string]string{"SECRET": "server-secret"})

	tests := []struct {
		name   string
		lookup envsubst.Lookup
		input  string
		want   string
	}{
		{name: "the name", lookup: set, input: "${BRANCH_NAME}", want: "lab"},
		{name: "the name in a path", lookup: set, input: "/injects/${BRANCH_NAME}/motd", want: "/injects/lab/motd"},
		{name: "the name with a default", lookup: set, input: "${BRANCH_NAME:other}", want: "lab"},
		{name: "another name", lookup: set, input: "${SECRET}", want: "${SECRET}"},
		{name: "another name with a default", lookup: set, input: "${SECRET:x}", want: "${SECRET:x}"},
		{name: "no braces", lookup: set, input: "$BRANCH_NAME $SECRET", want: "$BRANCH_NAME $SECRET"},
		{name: "both names", lookup: set, input: "${BRANCH_NAME}-${SECRET}", want: "lab-${SECRET}"},
		{name: "the escaped name", lookup: set, input: "$${BRANCH_NAME}", want: "${BRANCH_NAME}"},
		{name: "another escaped name", lookup: set, input: "$${SECRET}", want: "$${SECRET}"},
		{name: "the unset name with a default", lookup: unset, input: "${BRANCH_NAME:main}", want: "main"},
		{name: "the unset name", lookup: unset, input: "[${BRANCH_NAME}]", want: "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := string(envsubst.ExpandOnly([]byte(tt.input), "BRANCH_NAME", tt.lookup)); got != tt.want {
				t.Errorf("ExpandOnly(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	t.Parallel()

	const text = "${A}-${B}-$${C}"

	got := envsubst.Replace([]byte(text), func(tok envsubst.Token) ([]byte, bool) {
		return []byte("<" + tok.Name + ">"), tok.Name != "B"
	})

	if want := "<A>-${B}-<C>"; string(got) != want {
		t.Errorf("Replace(%q) = %q, want %q", text, got, want)
	}
}

// TestExpandProperties builds inputs from literal text, which holds no "$",
// placeholders and escapes. Each placeholder is replaced one time, each escape
// loses its first "$", and the literal text stays.
func TestExpandProperties(t *testing.T) {
	t.Parallel()

	const nested = "v:${SET}"

	values := map[string]string{"SET": nested, "EMPTY": ""}

	rapid.Check(t, func(t *rapid.T) {
		var input, want strings.Builder

		for range rapid.IntRange(0, 6).Draw(t, "tokens") {
			if rapid.Bool().Draw(t, "literal") {
				text := rapid.StringMatching(`[a{}: _-]{0,4}`).Draw(t, "text")
				input.WriteString(text)
				want.WriteString(text)

				continue
			}

			name := rapid.SampledFrom([]string{"SET", "EMPTY", "UNSET"}).Draw(t, "name")
			value, set := values[name]

			placeholder := "${" + name
			if rapid.Bool().Draw(t, "has default") {
				def := rapid.StringMatching(`[d:${ ]{0,4}`).Draw(t, "default")
				placeholder += ":" + def

				if !set {
					value = def
				}
			}

			placeholder += "}"

			if rapid.Bool().Draw(t, "escaped") {
				input.WriteString("$" + placeholder)
				want.WriteString(placeholder)

				continue
			}

			input.WriteString(placeholder)
			want.WriteString(value)
		}

		if got := string(envsubst.Expand([]byte(input.String()), envsubstLookup(values))); got != want.String() {
			t.Fatalf("Expand(%q) = %q, want %q", input.String(), got, want.String())
		}
	})
}
