package mm

import (
	"os"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

// FuzzValidateArg checks the validators against minimega's own command parser:
// an accepted value reaches the handler as exactly the argument phenix wrote.
func FuzzValidateArg(f *testing.F) {
	registerArgsPatterns()

	for _, seed := range []string{
		"EXP_1", "ns//vlan-1.a", "ünïcode-✓", "file", "mailto:x", "ftp://example.com/x", "", "two words",
		"tab\there", "no break", "a#b", "it's", "line\nbreak", `say "hi"`, `say"hi"`, `back\slash`,
		"$HOME", "${HOME}", "bell\a", "carriage\rreturn", "nul\x00", "del\x7f", "\xff",
		"http://example.com/x", "HTTPS://example.com/x", "FILE:disk.qcow2", "tar:a.tar",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		quotedOK, bareOK := ValidateQuotedArg(value) == nil, ValidateArg(value) == nil

		if bareOK && !quotedOK {
			t.Fatalf("ValidateArg(%q) accepted a value ValidateQuotedArg rejects", value)
		}

		if quotedOK {
			cmd, err := minicli.Compile(`vm tag vm1 "k" "` + value + `"`)
			if err != nil || cmd == nil || cmd.StringArgs["value"] != value || cmd.StringArgs["key"] != "k" {
				t.Fatalf("ValidateQuotedArg(%q) accepted a value minimega reads as %#v (error %v)", value, cmd, err)
			}

			if expanded := os.Expand(value, func(string) string { return "<env>" }); expanded != value {
				t.Fatalf("ValidateQuotedArg(%q) accepted a value minimega expands to %q", value, expanded)
			}
		}

		if bareOK {
			cmd, err := minicli.Compile("vm net connect vm1 1 " + value + " phenix")
			if err != nil || cmd == nil || cmd.StringArgs["vlan"] != value || cmd.StringArgs["bridge"] != testBridge {
				t.Fatalf("ValidateArg(%q) accepted a value minimega reads as %#v (error %v)", value, cmd, err)
			}
		}
	})
}
