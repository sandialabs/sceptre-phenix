package cmd

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// vmSetInterfaceModel is the --interface value that parseVMInterfaceFlag
// gives for value, as a model, and whether it accepts value.
func vmSetInterfaceModel(value string) (vmInterfaceChange, bool) {
	index, vlanName, ok := strings.Cut(value, "=")
	if !ok {
		return vmInterfaceChange{}, false
	}

	i, err := strconv.Atoi(index)
	if err != nil || i < 0 || vlanName != strings.TrimSpace(vlanName) {
		return vmInterfaceChange{}, false
	}

	return vmInterfaceChange{index: i, vlan: vlanName}, true
}

// FuzzParseVMInterfaceFlag checks that vm set takes --interface only as
// <index>=<vlan> with an index of 0 or more and no space at an end of the
// VLAN, and refuses every other value with errBadInterfaceFlag.
func FuzzParseVMInterfaceFlag(f *testing.F) {
	for _, seed := range []string{
		"0=EXP_1", "1=", "12=101", "=EXP", "x=EXP", "-1=EXP", "0", "", "0= EXP", "0=EXP ", "0=a=b", "+1=EXP", "07=", " 1=EXP",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		got, err := parseVMInterfaceFlag(value)

		want, ok := vmSetInterfaceModel(value)
		if !ok {
			if !errors.Is(err, errBadInterfaceFlag) {
				t.Fatalf("parseVMInterfaceFlag(%q) = %+v, %v; want an error wrapping errBadInterfaceFlag", value, got, err)
			}

			return
		}

		if err != nil || got != want {
			t.Fatalf("parseVMInterfaceFlag(%q) = %+v, %v; want %+v, nil", value, got, err, want)
		}

		again, err := parseVMInterfaceFlag(strconv.Itoa(got.index) + "=" + got.vlan)
		if err != nil || again != got {
			t.Fatalf("parseVMInterfaceFlag of %+v again = %+v, %v; want %+v, nil", got, again, err, got)
		}
	})
}

// vmSetFlagsValueGen draws a flag value: key=value text with booleans, JSON,
// numbers and spaces, or other text.
func vmSetFlagsValueGen() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{
			"k", "phenix/default-apps", " ", "=", "true", "false", " true", "[1, 2]", `{"a": 1}`, "[", "5", "0", "EXP_1", "x",
		}), 0, 4), func(parts []string) string { return strings.Join(parts, "") }),
		rapid.String(),
	)
}

// vm set reads repeated --annotation values with the parser of experiment
// create --node-annotation, and --interface with parseVMInterfaceFlag, and
// refuses the command line when either refuses a value.
func TestVMSetFlagsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		annotations := rapid.SliceOfN(vmSetFlagsValueGen(), 0, 3).Draw(t, "annotations")
		withIface := rapid.Bool().Draw(t, "withInterface")
		iface := vmSetFlagsValueGen().Draw(t, "interface")

		args := make([]string, 0, len(annotations)+1)
		for _, a := range annotations {
			args = append(args, "--annotation="+a)
		}

		if withIface {
			args = append(args, "--interface="+iface)
		}

		cmd := newVMSetCmd()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("ParseFlags(%q) error = %v", args, err)
		}

		got, err := parseVMSetFlags(cmd)

		wantAnnotations, annotationErr := parseNodeAnnotationFlags(annotations)
		wantIface, ifaceOK := vmSetInterfaceModel(iface)

		switch {
		case len(annotations) > 0 && annotationErr != nil, withIface && !ifaceOK:
			if err == nil {
				t.Fatalf("parseVMSetFlags(%q) = %+v, want an error", args, got)
			}

			return
		case len(annotations) == 0 && !withIface:
			if err == nil {
				t.Fatalf("parseVMSetFlags(%q) = %+v, want the error for no values", args, got)
			}

			return
		}

		if err != nil {
			t.Fatalf("parseVMSetFlags(%q) error = %v", args, err)
		}

		if len(annotations) == 0 {
			wantAnnotations = nil
		}

		if diff := cmp.Diff(wantAnnotations, got.annotations); diff != "" {
			t.Fatalf("parseVMSetFlags(%q) annotations mismatch (-want +got):\n%s", args, diff)
		}

		switch {
		case !withIface && got.iface != nil:
			t.Fatalf("parseVMSetFlags(%q) interface = %+v, want none", args, *got.iface)
		case withIface && (got.iface == nil || *got.iface != wantIface):
			t.Fatalf("parseVMSetFlags(%q) interface = %+v, want %+v", args, got.iface, wantIface)
		}
	})
}
