package common_test

import (
	"slices"
	"testing"

	"phenix/util/common"
)

func TestParseEnv(t *testing.T) {
	t.Setenv("PHENIX_TEST_SET", "value")
	t.Setenv("PHENIX_TEST_EMPTY", "")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "a name", input: "name: ${PHENIX_TEST_SET}-topo", want: "name: value-topo"},
		{name: "a set name with a default", input: "${PHENIX_TEST_SET:fallback}", want: "value"},
		{name: "a name set to the empty string, with a default", input: "[${PHENIX_TEST_EMPTY:fallback}]", want: "[]"},
		{name: "an unset name with a default", input: "${PHENIX_TEST_UNSET:fallback}", want: "fallback"},
		{name: "an unset name with an empty default", input: "[${PHENIX_TEST_UNSET:}]", want: "[]"},
		{name: "an unset name", input: "[${PHENIX_TEST_UNSET}]", want: "[]"},
		{
			name:  "text without placeholders",
			input: "name: $PHENIX_TEST_SET {x} ${} ${A-B} $${",
			want:  "name: $PHENIX_TEST_SET {x} ${} ${A-B} $${",
		},
		{name: "several placeholders", input: "${PHENIX_TEST_SET}/${PHENIX_TEST_UNSET:d}", want: "value/d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := common.ParseEnv(tt.input); got != tt.want {
				t.Errorf("ParseEnv(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestEnvPlaceholder pins the placeholders the pattern finds: the name in the
// first group and the default, if any, in the second.
func TestEnvPlaceholder(t *testing.T) {
	got := common.EnvPlaceholder.FindAllStringSubmatch("${A} ${B_2:x:y} $C ${D-e} ${} ${F:}", -1)

	want := [][]string{{"${A}", "A", ""}, {"${B_2:x:y}", "B_2", "x:y"}, {"${F:}", "F", ""}}

	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("EnvPlaceholder matches = %q, want %q", got, want)
	}
}
