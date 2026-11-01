package mm

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

func TestValidateArg(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value               string
		wantBare, wantQuote bool // whether each accepts it
	}{
		{value: "EXP_1", wantBare: true, wantQuote: true},
		{value: "ns//vlan-1.a", wantBare: true, wantQuote: true},
		{value: "", wantBare: false, wantQuote: true},
		{value: "two words", wantBare: false, wantQuote: true},
		{value: "a#b", wantBare: false, wantQuote: true},
		{value: "it's", wantBare: false, wantQuote: true},
		{value: "line\nbreak", wantBare: false, wantQuote: true},
		{value: `say "hi"`, wantBare: false, wantQuote: false},
		{value: `back\slash`, wantBare: false, wantQuote: false},
		{value: "$HOME", wantBare: false, wantQuote: false},
		{value: "bell\a", wantBare: false, wantQuote: false},
		{value: "\xff", wantBare: false, wantQuote: false},
		{value: "http://example.com/x", wantBare: false, wantQuote: false},
		{value: "FILE:disk.qcow2", wantBare: false, wantQuote: false},
		{value: "tar:a.tar", wantBare: false, wantQuote: false},
		{value: "ftp://example.com/x", wantBare: true, wantQuote: true},
	} {
		if err := ValidateArg(tc.value); (err == nil) != tc.wantBare || (err != nil && !errors.Is(err, ErrUnsafeArg)) {
			t.Errorf("ValidateArg(%q) = %v, want accepted %t", tc.value, err, tc.wantBare)
		}

		if err := ValidateQuotedArg(tc.value); (err == nil) != tc.wantQuote || (err != nil && !errors.Is(err, ErrUnsafeArg)) {
			t.Errorf("ValidateQuotedArg(%q) = %v, want accepted %t", tc.value, err, tc.wantQuote)
		}
	}
}

func TestValidateTagsNamesTheBadTag(t *testing.T) {
	t.Parallel()

	err := ValidateTags(map[string]string{"a": "1", "b": `"x" "y"`, "c": "$c"})
	if !errors.Is(err, ErrUnsafeArg) || !strings.Contains(err.Error(), `tag "b"`) {
		t.Errorf("ValidateTags(b and c unsafe) = %v, want %v naming tag b first", err, ErrUnsafeArg)
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
			if err != nil || cmd == nil || cmd.StringArgs["vlan"] != value || cmd.StringArgs["bridge"] != "phenix" {
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
