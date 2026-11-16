package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/vlan"
)

func TestParseVLANAliasPin(t *testing.T) {
	tests := []struct {
		value     string
		wantAlias string
		wantID    int
		wantErr   bool
	}{
		{value: "EXP_1=101", wantAlias: "EXP_1", wantID: 101},
		{value: "EXP_1=0", wantAlias: "EXP_1", wantID: 0},
		{value: "a=4094", wantAlias: "a", wantID: 4094},
		{value: "a=b=1", wantErr: true},
		{value: "EXP_1", wantErr: true},
		{value: "=101", wantErr: true},
		{value: "EXP 1=101", wantErr: true},
		{value: "EXP_1=4095", wantErr: true},
		{value: "EXP_1=-1", wantErr: true},
		{value: "EXP_1=x", wantErr: true},
		{value: "EXP_1=", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			alias, id, err := parseVLANAliasPin(tc.value)
			if tc.wantErr {
				if !errors.Is(err, errBadVLANAlias) {
					t.Errorf("parseVLANAliasPin(%q) error = %v, want %v", tc.value, err, errBadVLANAlias)
				}

				return
			}

			if err != nil || alias != tc.wantAlias || id != tc.wantID {
				t.Errorf("parseVLANAliasPin(%q) = %q, %d, %v, want %q, %d, nil", tc.value, alias, id, err, tc.wantAlias, tc.wantID)
			}
		})
	}
}

// An alias name and an ID of 0 or a VLAN ID, joined by "=", parse back to
// themselves; any other ID is refused.
func TestParseVLANAliasPinProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		alias := rapid.StringMatching(`[A-Za-z0-9_.-]{1,12}`).Draw(t, "alias")
		id := rapid.IntRange(-10, vlan.MaxID+10).Draw(t, "id")

		gotAlias, gotID, err := parseVLANAliasPin(alias + "=" + strconv.Itoa(id))

		if id != 0 && !vlan.ValidID(id) {
			if !errors.Is(err, errBadVLANAlias) {
				t.Fatalf("parseVLANAliasPin(%s=%d) error = %v, want %v", alias, id, err, errBadVLANAlias)
			}

			return
		}

		if err != nil || gotAlias != alias || gotID != id {
			t.Fatalf("parseVLANAliasPin(%s=%d) = %q, %d, %v, want %q, %d, nil", alias, id, gotAlias, gotID, err, alias, id)
		}
	})
}

func TestParseVLANRange(t *testing.T) {
	tests := []struct {
		value  string
		lo, hi int
		ok     bool
	}{
		{value: "100-200", lo: 100, hi: 200, ok: true},
		{value: "1-4094", lo: 1, hi: 4094, ok: true},
		{value: "7-7", lo: 7, hi: 7, ok: true},
		{value: "200-100"},
		{value: "0-10"},
		{value: "1-4095"},
		{value: "100"},
		{value: "-100"},
		{value: "a-b"},
		{value: "100-200-300"},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			lo, hi, err := parseVLANRange(tc.value)
			if !tc.ok {
				if !errors.Is(err, errBadVLANRange) {
					t.Errorf("parseVLANRange(%q) error = %v, want %v", tc.value, err, errBadVLANRange)
				}

				return
			}

			if err != nil || lo != tc.lo || hi != tc.hi {
				t.Errorf("parseVLANRange(%q) = %d, %d, %v, want %d, %d, nil", tc.value, lo, hi, err, tc.lo, tc.hi)
			}
		})
	}
}

// Two VLAN IDs, lowest first, parse back to themselves; the range that
// parseVLANRange takes is one that vlan.CheckProposal takes.
func TestParseVLANRangeProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.IntRange(-5, vlan.MaxID+5).Draw(t, "a")
		b := rapid.IntRange(-5, vlan.MaxID+5).Draw(t, "b")
		value := fmt.Sprintf("%d-%d", a, b)

		lo, hi, err := parseVLANRange(value)

		valid := vlan.ValidID(a) && vlan.ValidID(b) && a <= b
		if !valid {
			if err == nil {
				t.Fatalf("parseVLANRange(%q) = %d, %d, nil, want an error", value, lo, hi)
			}

			return
		}

		if err != nil || lo != a || hi != b {
			t.Fatalf("parseVLANRange(%q) = %d, %d, %v, want %d, %d, nil", value, lo, hi, err, a, b)
		}

		if err := vlan.CheckProposal(vlan.Proposal{Experiment: "e", Aliases: nil, Min: lo, Max: hi}); err != nil {
			t.Fatalf("vlan.CheckProposal(range %d-%d) = %v, want nil", lo, hi, err)
		}
	})
}

func TestParseVLANState(t *testing.T) {
	for _, state := range []string{"", vlan.StatePinned, vlan.StateAllocated, vlan.StateBlacklisted} {
		if got, err := parseVLANState(state); err != nil || got != state {
			t.Errorf("parseVLANState(%q) = %q, %v, want %q, nil", state, got, err, state)
		}
	}

	for _, state := range []string{"Pinned", "free", " pinned"} {
		if _, err := parseVLANState(state); !errors.Is(err, errBadVLANState) {
			t.Errorf("parseVLANState(%q) error = %v, want %v", state, err, errBadVLANState)
		}
	}
}

// A VLAN check starts from the experiment's stored pins and range, then the flags pin aliases and replace the range.
func TestVLANProposal(t *testing.T) {
	in := vlan.Inputs{
		Experiments: []vlan.ExperimentInput{
			{Name: "other", Pins: map[string]int{"X": 5}, Min: 1, Max: 9},
			{Name: "exp", Pins: map[string]int{"A": 100, "B": 0}, Min: 100, Max: 199},
		},
		Minimega: vlan.Minimega{},
	}

	tests := []struct {
		name    string
		exp     string
		aliases map[string]int
		lo, hi  int
		want    vlan.Proposal
	}{
		{
			name: "the stored spec", exp: "exp",
			want: vlan.Proposal{Experiment: "exp", Aliases: map[string]int{"A": 100, "B": 0}, Min: 100, Max: 199},
		},
		{
			name: "a pin and a range", exp: "exp", aliases: map[string]int{"B": 150, "C": 0}, lo: 150, hi: 160,
			want: vlan.Proposal{Experiment: "exp", Aliases: map[string]int{"A": 100, "B": 150, "C": 0}, Min: 150, Max: 160},
		},
		{
			name: "an experiment that does not exist", exp: "new", aliases: map[string]int{"A": 7},
			want: vlan.Proposal{Experiment: "new", Aliases: map[string]int{"A": 7}, Min: 0, Max: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := vlanProposal(in, tc.exp, tc.aliases, tc.lo, tc.hi)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("vlanProposal(%s) mismatch (-want +got):\n%s", tc.exp, diff)
			}
		})
	}
}
