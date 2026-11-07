package cmd

import (
	"errors"
	"strconv"
	"testing"

	"pgregory.net/rapid"

	"phenix/api/vlan"
)

// vlanArgsModelID is the VLAN ID that parseCLIVLANID gives for arg, as a
// model: a base 10 whole number in 1–4094.
func vlanArgsModelID(arg string) (int, bool) {
	id, err := strconv.Atoi(arg)

	return id, err == nil && id >= vlan.MinID && id <= vlan.MaxID
}

// checkVLANAliasArgs reports how parseVLANAliasArgs differs from a model of
// the argument forms of phenix vlan alias.
func checkVLANAliasArgs(t settingsCmdTB, args []string, unpin, force bool) {
	t.Helper()

	got, err := parseVLANAliasArgs(args, unpin, force)

	var (
		want   vlanAliasRequest
		wantOK = true
	)

	switch {
	case unpin:
		wantOK = len(args) == 2 && !force
		if wantOK {
			want = vlanAliasRequest{action: vlanAliasUnpin, exp: args[0], alias: args[1], id: 0}
		}
	case len(args) == 3:
		id, ok := vlanArgsModelID(args[2])
		wantOK = ok
		want = vlanAliasRequest{action: vlanAliasPin, exp: args[0], alias: args[1], id: id}
	case force || len(args) > 3:
		wantOK = false
	case len(args) == 0:
		want = vlanAliasRequest{action: vlanAliasListAll, exp: "", alias: "", id: 0}
	case len(args) == 1:
		want = vlanAliasRequest{action: vlanAliasListExperiment, exp: args[0], alias: "", id: 0}
	default:
		want = vlanAliasRequest{action: vlanAliasLookup, exp: args[0], alias: args[1], id: 0}
	}

	if !wantOK {
		if err == nil {
			t.Fatalf("parseVLANAliasArgs(%q, %t, %t) = %+v, want an error", args, unpin, force, got)
		}

		if !errors.Is(err, errVLANArgs) && !errors.Is(err, errBadVLANID) {
			t.Fatalf("parseVLANAliasArgs(%q, %t, %t) error = %v, want %v or %v", args, unpin, force, err, errVLANArgs, errBadVLANID)
		}

		return
	}

	if err != nil || got != want {
		t.Fatalf("parseVLANAliasArgs(%q, %t, %t) = %+v, %v, want %+v, nil", args, unpin, force, got, err, want)
	}
}

// checkVLANRangeArgs reports how parseVLANRangeArgs differs from a model of
// the argument forms of phenix vlan range.
func checkVLANRangeArgs(t settingsCmdTB, args []string, clearRange, force bool) {
	t.Helper()

	got, err := parseVLANRangeArgs(args, clearRange, force)

	var (
		want   vlanRangeRequest
		wantOK = true
	)

	switch {
	case clearRange:
		wantOK = len(args) == 1 && !force
		if wantOK {
			want = vlanRangeRequest{action: vlanRangeClear, exp: args[0], lo: 0, hi: 0}
		}
	case len(args) == 3:
		lo, loOK := vlanArgsModelID(args[1])
		hi, hiOK := vlanArgsModelID(args[2])
		wantOK = loOK && hiOK && lo <= hi
		want = vlanRangeRequest{action: vlanRangeSet, exp: args[0], lo: lo, hi: hi}
	case force || len(args) == 2 || len(args) > 3:
		wantOK = false
	case len(args) == 0:
		want = vlanRangeRequest{action: vlanRangeListAll, exp: "", lo: 0, hi: 0}
	default:
		want = vlanRangeRequest{action: vlanRangeListExperiment, exp: args[0], lo: 0, hi: 0}
	}

	if !wantOK {
		if err == nil {
			t.Fatalf("parseVLANRangeArgs(%q, %t, %t) = %+v, want an error", args, clearRange, force, got)
		}

		if !errors.Is(err, errVLANArgs) && !errors.Is(err, errBadVLANID) {
			t.Fatalf("parseVLANRangeArgs(%q, %t, %t) error = %v, want %v or %v", args, clearRange, force, err, errVLANArgs, errBadVLANID)
		}

		return
	}

	if err != nil || got != want {
		t.Fatalf("parseVLANRangeArgs(%q, %t, %t) = %+v, %v, want %+v, nil", args, clearRange, force, got, err, want)
	}
}

// vlanArgsGen draws a VLAN command argument: a name, a number near the VLAN
// ID bounds, or other text.
func vlanArgsGen() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.StringMatching(`[A-Za-z][A-Za-z0-9_-]{0,8}`),
		rapid.Map(rapid.IntRange(-5, 4100), strconv.Itoa),
		rapid.SampledFrom([]string{"", "0", "1", "4094", "4095", "+7", "007", " 5", "1e3", "0x10"}),
		rapid.String(),
	)
}

// parseVLANAliasArgs and parseVLANRangeArgs accept each argument form of
// phenix vlan alias and range, with each flag, as the model does.
func TestVLANArgsProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		args := rapid.SliceOfN(vlanArgsGen(), 0, 5).Draw(t, "args")
		flag := rapid.Bool().Draw(t, "flag")
		force := rapid.Bool().Draw(t, "force")

		checkVLANAliasArgs(t, args, flag, force)
		checkVLANRangeArgs(t, args, flag, force)
	})
}

// FuzzVLANRangeArgs checks parseVLANRangeArgs and parseVLANAliasArgs against
// the model for three arguments, the form that parses VLAN IDs.
func FuzzVLANRangeArgs(f *testing.F) {
	for _, seed := range [][3]string{
		{"exp", "10", "20"}, {"exp", "20", "10"}, {"exp", "0", "4094"}, {"exp", "1", "4095"},
		{"exp", "x", "20"}, {"", "+1", "007"}, {"exp", " 5", "6"}, {"exp", "4094", "4094"},
	} {
		f.Add(seed[0], seed[1], seed[2], false)
		f.Add(seed[0], seed[1], seed[2], true)
	}

	f.Fuzz(func(t *testing.T, exp, first, second string, force bool) {
		args := []string{exp, first, second}

		checkVLANRangeArgs(t, args, false, force)
		checkVLANAliasArgs(t, args, false, force)
	})
}
