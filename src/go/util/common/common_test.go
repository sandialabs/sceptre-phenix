package common_test

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

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

// commonEnvSet holds a placeholder of its own, which ParseEnv must not expand again.
const commonEnvSet = "v:${PHENIX_RAPID_SET}"

// TestParseEnvProperties builds inputs from literal text, which holds no "$", and
// placeholders, and checks each placeholder is replaced once and the text kept as is.
func TestParseEnvProperties(t *testing.T) {
	t.Setenv("PHENIX_RAPID_SET", commonEnvSet)
	t.Setenv("PHENIX_RAPID_EMPTY", "")

	values := map[string]string{"PHENIX_RAPID_SET": commonEnvSet, "PHENIX_RAPID_EMPTY": ""}

	rapid.Check(t, func(t *rapid.T) {
		var input, want strings.Builder

		for range rapid.IntRange(0, 6).Draw(t, "tokens") {
			if rapid.Bool().Draw(t, "literal") {
				text := rapid.StringMatching(`[a{}: _-]{0,4}`).Draw(t, "text")
				input.WriteString(text)
				want.WriteString(text)

				continue
			}

			name := rapid.SampledFrom([]string{"PHENIX_RAPID_SET", "PHENIX_RAPID_EMPTY", "PHENIX_RAPID_UNSET"}).Draw(t, "name")
			value, set := values[name]

			placeholder := "${" + name
			if rapid.Bool().Draw(t, "has default") {
				def := rapid.StringMatching(`[d:${ ]{0,4}`).Draw(t, "default")
				placeholder += ":" + def

				if !set {
					value = def
				}
			}

			input.WriteString(placeholder + "}")
			want.WriteString(value)
		}

		if got := common.ParseEnv(input.String()); got != want.String() {
			t.Fatalf("ParseEnv(%q) = %q, want %q", input.String(), got, want.String())
		}
	})
}

// ParseDiskCheck takes the three modes in any case, and "" as the default error mode.
func TestParseDiskCheck(t *testing.T) {
	tests := []struct {
		mode    string
		want    common.DiskCheck
		wantErr string
	}{
		{mode: "", want: common.DiskCheckError},
		{mode: "error", want: common.DiskCheckError},
		{mode: "WARN", want: common.DiskCheckWarn},
		{mode: "Off", want: common.DiskCheckOff},
		{mode: "loose", want: common.DiskCheckError, wantErr: "unknown disk check mode provided: loose"},
	}

	for _, tt := range tests {
		got, err := common.ParseDiskCheck(tt.mode)

		var gotErr string
		if err != nil {
			gotErr = err.Error()
		}

		if got != tt.want || gotErr != tt.wantErr {
			t.Errorf("ParseDiskCheck(%q) = %q, %q, want %q, %q", tt.mode, got, gotErr, tt.want, tt.wantErr)
		}
	}
}

// SetDiskCheck keeps the current mode when it refuses one, and ParseDiskCheck refuses exactly the values that are not a
// mode in some case.
func TestSetDiskCheckProperties(t *testing.T) {
	t.Cleanup(func() { _ = common.SetDiskCheck("") })

	rapid.Check(t, func(t *rapid.T) {
		before := rapid.SampledFrom([]string{"error", "warn", "off"}).Draw(t, "before")
		mode := rapid.OneOf(rapid.SampledFrom([]string{"", "Error", "wARN", "OFF", "none"}), rapid.String()).Draw(t, "mode")

		_ = common.SetDiskCheck(before)

		valid := slices.Contains([]string{"", "error", "warn", "off"}, strings.ToLower(mode))
		err := common.SetDiskCheck(mode)

		switch {
		case valid != (err == nil):
			t.Fatalf("SetDiskCheck(%q) error = %v, want valid %t", mode, err, valid)
		case !valid && common.CurrentDiskCheck() != common.DiskCheck(before):
			t.Fatalf("SetDiskCheck(%q) changed the mode to %q, want %q kept", mode, common.CurrentDiskCheck(), before)
		}
	})
}
