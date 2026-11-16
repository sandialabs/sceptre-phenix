package mm

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

// testBridge is the bridge name in the minimega commands that the tests compile.
const testBridge = "phenix"

func TestValidateArg(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value               string
		wantBare, wantQuote bool   // whether each accepts it
		wantRune            string // the rune the error names, if any
	}{
		{value: "EXP_1", wantBare: true, wantQuote: true},
		{value: "ns//vlan-1.a", wantBare: true, wantQuote: true},
		{value: "ünïcode-✓", wantBare: true, wantQuote: true},
		{value: "file", wantBare: true, wantQuote: true},
		{value: "mailto:x", wantBare: true, wantQuote: true},
		{value: "ftp://example.com/x", wantBare: true, wantQuote: true},
		{value: "", wantBare: false, wantQuote: true},
		{value: "two words", wantBare: false, wantQuote: true, wantRune: "' '"},
		{value: "tab\there", wantBare: false, wantQuote: true, wantRune: `'\t'`},
		{value: "no\u00a0break", wantBare: false, wantQuote: true, wantRune: `'\u00a0'`},
		{value: "a#b", wantBare: false, wantQuote: true, wantRune: "'#'"},
		{value: "it's", wantBare: false, wantQuote: true, wantRune: `'\''`},
		{value: "line\nbreak", wantBare: false, wantQuote: true, wantRune: `'\n'`},
		{value: `say "hi"`, wantBare: false, wantQuote: false, wantRune: "' '"},
		{value: `say"hi"`, wantBare: false, wantQuote: false, wantRune: `'"'`},
		{value: `back\slash`, wantBare: false, wantQuote: false, wantRune: `'\\'`},
		{value: "$HOME", wantBare: false, wantQuote: false, wantRune: "'$'"},
		{value: "bell\a", wantBare: false, wantQuote: false, wantRune: `'\a'`},
		{value: "carriage\rreturn", wantBare: false, wantQuote: false},
		{value: "nul\x00", wantBare: false, wantQuote: false, wantRune: `'\x00'`},
		{value: "del\x7f", wantBare: false, wantQuote: false, wantRune: `'\x7f'`},
		{value: "\xff", wantBare: false, wantQuote: false},
		{value: "http://example.com/x", wantBare: false, wantQuote: false},
		{value: "HTTPS://example.com/x", wantBare: false, wantQuote: false},
		{value: "FILE:disk.qcow2", wantBare: false, wantQuote: false},
		{value: "tar:a.tar", wantBare: false, wantQuote: false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()

			bare, quoted := ValidateArg(tc.value), ValidateQuotedArg(tc.value)

			if (bare == nil) != tc.wantBare || (bare != nil && !errors.Is(bare, ErrUnsafeArg)) {
				t.Errorf("ValidateArg(%q) = %v, want accepted %t", tc.value, bare, tc.wantBare)
			}

			if (quoted == nil) != tc.wantQuote || (quoted != nil && !errors.Is(quoted, ErrUnsafeArg)) {
				t.Errorf("ValidateQuotedArg(%q) = %v, want accepted %t", tc.value, quoted, tc.wantQuote)
			}

			if tc.wantRune != "" && (bare == nil || !strings.HasSuffix(bare.Error(), " contains "+tc.wantRune)) {
				t.Errorf("ValidateArg(%q) = %v, want it to name %s", tc.value, bare, tc.wantRune)
			}
		})
	}
}

func TestValidateTags(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		tags    map[string]string
		wantErr string // the error's prefix, "" for none
	}{
		"no tags":            {tags: nil},
		"empty tags":         {tags: map[string]string{}},
		"safe tags":          {tags: map[string]string{"a": "1", "two words": "it's #1", "": ""}},
		"unsafe value":       {tags: map[string]string{"a": "1", "b": `"x" "y"`}, wantErr: `value of tag "b": `},
		"unsafe key":         {tags: map[string]string{"a": "1", "$b": "2"}, wantErr: "tag key: "},
		"first in key order": {tags: map[string]string{"a": "1", "b": `"x" "y"`, "c": "$c"}, wantErr: `value of tag "b": `},
		"key before value":   {tags: map[string]string{"$a": "$1"}, wantErr: "tag key: "},
		"URL value":          {tags: map[string]string{"a": "http://x"}, wantErr: `value of tag "a": `},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidateTags(tc.tags)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("ValidateTags(%q) = %v, want nil", tc.tags, err)
			case tc.wantErr != "" && (!errors.Is(err, ErrUnsafeArg) || !strings.HasPrefix(err.Error(), tc.wantErr)):
				t.Errorf("ValidateTags(%q) = %v, want %v starting with %q", tc.tags, err, ErrUnsafeArg, tc.wantErr)
			}
		})
	}
}

//nolint:gochecknoglobals // minicli's handlers are global; register them once
var registerArgsPatterns = sync.OnceFunc(func() {
	minicli.MustRegister(&minicli.Handler{Patterns: []string{
		"vm tag <vm target> <key> <value>",
		"vm net connect <vm target> <tap position> <vlan> [bridge]",
	}})
})

// genArgsValue draws strings rich in what minimega's command line treats
// specially.
func genArgsValue() *rapid.Generator[string] {
	special := rapid.SampledFrom([]rune("\"'\\#$ \t\n\r:/ahtpfile\u00a0\u2028"))

	return rapid.StringOf(rapid.OneOf(special, special, rapid.Rune()))
}

// An accepted value reaches minimega's handler as exactly the argument phenix wrote, bare or quoted, and
// minimega's environment variable expansion leaves it as it is.
func TestValidateArgProperties(t *testing.T) { //nolint:paralleltest // minicli's registry is global
	registerArgsPatterns()

	rapid.Check(t, func(t *rapid.T) {
		value := genArgsValue().Draw(t, "value")

		if ValidateQuotedArg(value) == nil {
			cmd, err := minicli.Compile(`vm tag vm1 "k" "` + value + `"`)
			if err != nil || cmd == nil || cmd.StringArgs["value"] != value || cmd.StringArgs["key"] != "k" {
				t.Fatalf("ValidateQuotedArg(%q) accepted a value minimega reads as %#v (error %v)", value, cmd, err)
			}
		}

		if ValidateArg(value) == nil {
			cmd, err := minicli.Compile("vm net connect vm1 1 " + value + " phenix")
			if err != nil || cmd == nil || cmd.StringArgs["vlan"] != value || cmd.StringArgs["bridge"] != testBridge {
				t.Fatalf("ValidateArg(%q) accepted a value minimega reads as %#v (error %v)", value, cmd, err)
			}
		}

		expanded := os.Expand(value, func(string) string { return "<env>" })
		if ValidateQuotedArg(value) == nil && expanded != value {
			t.Fatalf("ValidateQuotedArg(%q) accepted a value minimega expands to %q", value, expanded)
		}

		if ValidateArg(value) == nil && ValidateQuotedArg(value) != nil {
			t.Fatalf("ValidateArg(%q) accepted a value ValidateQuotedArg rejects", value)
		}
	})
}

func TestUnsafeArgumentsSendNothing(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, call := range map[string]func() error{
		"tag value": func() error {
			return (Minimega{}).SetVMTags(NS("exp"), VMName("vm1"), Tags(map[string]string{"a": `1" "b`}))
		},
		"tag key": func() error {
			return (Minimega{}).SetVMTags(NS("exp"), VMName("vm1"), Tags(map[string]string{"$a": "1"}))
		},
		"VLAN": func() error {
			return (Minimega{}).ConnectVMInterface(NS("exp"), VMName("vm1"), ConnectInterface(0), ConnectVLAN("EXP_1 #"))
		},
		"bridge": func() error {
			return (Minimega{}).ConnectVMInterface(
				NS("exp"), VMName("vm1"), ConnectInterface(0), ConnectVLAN("EXP_1"), Bridge("br 2"),
			)
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, nil)

			if err := call(); !errors.Is(err, ErrUnsafeArg) {
				t.Errorf("error = %v, want %v", err, ErrUnsafeArg)
			}

			if diff := cmp.Diff([]mmtest.Command(nil), received()); diff != "" {
				t.Errorf("sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateUnexpanded(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "br0", "a b c", "$(df /)", "price $", "10.0.0.1/24", "fileserver:x", "a$"} {
		if err := ValidateUnexpanded(s); err != nil {
			t.Errorf("ValidateUnexpanded(%q) = %v, want nil", s, err)
		}
	}

	for _, s := range []string{"$HOME", "${X}", "a$1b", "$@", "x ${", "file:a", "router a http://b", `"tar:x"`, "HTTPS://x"} {
		if err := ValidateUnexpanded(s); !errors.Is(err, ErrUnsafeArg) {
			t.Errorf("ValidateUnexpanded(%q) = %v, want %v", s, err, ErrUnsafeArg)
		}
	}
}
