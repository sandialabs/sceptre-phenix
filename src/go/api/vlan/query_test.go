package vlan

import (
	"errors"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm"
)

// queryTestInputs: a stopped experiment a pinning 102 and 105 in 100–199, a
// running experiment b on 102 and 110, and a blacklist of 103.
func queryTestInputs() Inputs {
	return Inputs{
		Experiments: []ExperimentInput{
			exp("a", false, 100, 199, map[string]int{"X": 102, "Y": 105},
				VMInterface{VM: "a1", Host: "", Interface: "IF0", Alias: "X", VLAN: 0}),
			exp("b", true, 0, 0, map[string]int{"P": 0, "Q": 0},
				VMInterface{VM: "b1", Host: "", Interface: "eth0", Alias: "P", VLAN: 0}),
		},
		Minimega: up([]int{103}, ns("b", map[string]int{"P": 102, "Q": 110},
			VMInterface{VM: "b1", Host: "compute1", Interface: "0", Alias: "P", VLAN: 102},
			VMInterface{VM: "b2", Host: "compute2", Interface: "1", Alias: "Q", VLAN: 110})),
	}
}

// Filter keeps the rows, experiments and issues of the query.
func TestFilter(t *testing.T) {
	t.Parallel()

	rep := Merge(queryTestInputs(), seeAll)

	ids := func(rows []Row) [][2]any {
		out := make([][2]any, 0, len(rows))
		for _, r := range rows {
			out = append(out, [2]any{r.ID, r.Experiment})
		}

		return out
	}

	for name, tc := range map[string]struct {
		q          Query
		wantRows   [][2]any
		wantExps   int
		wantIssues int
	}{
		"nothing": {
			q:        Query{},
			wantRows: [][2]any{{102, "a"}, {102, "b"}, {103, ""}, {105, "a"}, {110, "b"}}, wantExps: 2, wantIssues: 2,
		},
		"an experiment":  {q: Query{Experiment: "b"}, wantRows: [][2]any{{102, "b"}, {110, "b"}}, wantExps: 1, wantIssues: 1},
		"a VLAN ID":      {q: Query{VLAN: 102}, wantRows: [][2]any{{102, "a"}, {102, "b"}}, wantExps: 2, wantIssues: 2},
		"a state":        {q: Query{State: StateBlacklisted}, wantRows: [][2]any{{103, ""}}, wantExps: 2, wantIssues: 2},
		"issues only":    {q: Query{IssuesOnly: true}, wantRows: [][2]any{{102, "a"}, {102, "b"}}, wantExps: 2, wantIssues: 2},
		"a free VLAN ID": {q: Query{VLAN: 200}, wantRows: [][2]any{}, wantExps: 2, wantIssues: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Filter(rep, tc.q)

			if diff := cmp.Diff(tc.wantRows, ids(got.VLANs)); diff != "" {
				t.Errorf("Filter(%+v) rows mismatch (-want +got):\n%s", tc.q, diff)
			}

			if len(got.Experiments) != tc.wantExps || len(got.Issues) != tc.wantIssues {
				t.Errorf("Filter(%+v) = %d experiments and %d issues, want %d and %d",
					tc.q, len(got.Experiments), len(got.Issues), tc.wantExps, tc.wantIssues)
			}
		})
	}
}

// Filter by VLAN ID keeps the issues that the ID's rows carry: the live ID
// of id-differs and the IDs of an unowned namespace.
func TestFilterKeepsIssuesOfRows(t *testing.T) {
	t.Parallel()

	rep := Merge(Inputs{
		Experiments: []ExperimentInput{exp("c", true, 0, 0, map[string]int{"X": 104, "Y": 0})},
		Minimega:    up(nil, ns("c", map[string]int{"X": 110, "Y": 120}), ns("old", map[string]int{"P": 400})),
	}, seeAll)

	for id, want := range map[int][]string{
		104: {KindIDDiffers},
		110: {KindIDDiffers},
		120: {},
		400: {KindUnowned},
	} {
		got := []string{}
		for _, i := range Filter(rep, Query{Experiment: "", VLAN: id, State: "", IssuesOnly: false}).Issues {
			got = append(got, i.Kind)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Filter(VLAN %d) issue kinds mismatch (-want +got):\n%s", id, diff)
		}
	}
}

// VMsOn lists the VM interfaces on an ID, named as the topology names them.
func TestVMsOn(t *testing.T) {
	t.Parallel()

	rep := Merge(queryTestInputs(), seeAll)

	want := []VM{
		{Experiment: "a", VM: "a1", Host: "", Interface: "IF0"},
		{Experiment: "b", VM: "b1", Host: "compute1", Interface: "eth0"},
	}

	if diff := cmp.Diff(want, VMsOn(rep, 102)); diff != "" {
		t.Errorf("VMsOn(102) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]VM{}, VMsOn(rep, 999)); diff != "" {
		t.Errorf("VMsOn(999) mismatch (-want +got):\n%s", diff)
	}
}

// FindFree skips every ID in use, and with outside every range as well.
func TestFindFree(t *testing.T) {
	t.Parallel()

	in := queryTestInputs()

	for name, tc := range map[string]struct {
		count, lo, hi      int
		outside            bool
		wantStart, wantEnd int
		wantErr            error
	}{
		"one ID":          {count: 1, lo: 102, hi: 4094, wantStart: 104, wantEnd: 104},
		"a block":         {count: 3, lo: 100, hi: 4094, wantStart: 106, wantEnd: 108},
		"outside ranges":  {count: 2, lo: 100, hi: 4094, outside: true, wantStart: 200, wantEnd: 201},
		"too small":       {count: 2, lo: 104, hi: 105, wantErr: ErrNoFreeBlock},
		"at the very end": {count: 1, lo: 4094, hi: 4094, wantStart: 4094, wantEnd: 4094},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			start, end, err := FindFree(in, tc.count, tc.lo, tc.hi, tc.outside)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FindFree(%d, %d, %d) error = %v, want %v", tc.count, tc.lo, tc.hi, err, tc.wantErr)
			}

			if start != tc.wantStart || end != tc.wantEnd {
				t.Errorf("FindFree(%d, %d, %d) = %d–%d, want %d–%d", tc.count, tc.lo, tc.hi, start, end, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// FindFree outside the ranges reads a stored range past 4094 only up to the
// IDs it searches, so a huge range does not hang it.
func TestFindFreeHugeRange(t *testing.T) {
	t.Parallel()

	in := Inputs{
		Experiments: []ExperimentInput{exp("a", false, 4000, math.MaxInt32, map[string]int{})},
		Minimega:    up(nil),
	}

	start, end, err := FindFree(in, 1, 3990, MaxID, true)
	if err != nil || start != 3990 || end != 3990 {
		t.Errorf("FindFree(1, 3990, 4094, outside) = %d–%d, %v, want 3990–3990", start, end, err)
	}

	if _, _, err := FindFree(in, 1, 4000, MaxID, true); !errors.Is(err, ErrNoFreeBlock) {
		t.Errorf("FindFree(1, 4000, 4094, outside) error = %v, want %v", err, ErrNoFreeBlock)
	}
}

// Check gives the issues a proposal adds, for a new or an existing
// experiment, with hidden experiments named "another experiment".
func TestCheck(t *testing.T) {
	t.Parallel()

	in := queryTestInputs()

	for name, tc := range map[string]struct {
		p    Proposal
		view View
		want []Issue
	}{
		"a free pin": {p: Proposal{Experiment: "new", Aliases: map[string]int{"Z": 150}}, view: seeAll, want: []Issue{}},
		"a pin on another's live ID, seen": {
			p: Proposal{Experiment: "new", Aliases: map[string]int{"Z": 110}}, view: seeAll,
			want: []Issue{testIssue(KindDuplicatePin, intp(110), "Z", []string{"new", "b"}, "", nil, "Also used by b (Q).")},
		},
		"a pin on another's live ID, hidden": {
			p: Proposal{Experiment: "new", Aliases: map[string]int{"Z": 110}}, view: seeOnly(),
			want: []Issue{
				testIssue(KindDuplicatePin, intp(110), "Z", []string{"new", HiddenExperiment}, "", nil,
					"Also used by another experiment."),
			},
		},
		"a blacklisted pin outside a range that overlaps": {
			p: Proposal{Experiment: "new", Aliases: map[string]int{"Z": 103}, Min: 150, Max: 160}, view: seeAll,
			want: []Issue{
				testIssue(KindPinBlacklisted, intp(103), "Z", []string{"new"}, "", nil, "minimega blacklists 103."),
				testIssue(KindRangeOverlap, nil, "", []string{"new", "a"}, "", &[2]int{150, 160}, "150–160 overlaps a."),
				testIssue(KindOutOfRange, intp(103), "Z", []string{"new"}, "", &[2]int{150, 160}, "Outside the range 150–160."),
			},
		},
		"an existing experiment's changed pin": {
			p: Proposal{Experiment: "a", Aliases: map[string]int{"X": 120, "Y": 105}, Min: 100, Max: 199}, view: seeOnly("a"),
			want: []Issue{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, Check(in, tc.view, tc.p), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Check(%+v) mismatch (-want +got):\n%s", tc.p, diff)
			}
		})
	}
}

// vmInterfaces keeps the interfaces on a VLAN, with or without an alias.
func TestVMInterfaces(t *testing.T) {
	t.Parallel()

	vms := mm.VMs{
		{Name: "vm1", Host: "compute1", Networks: []string{"EXP_1 (101)", "disconnected", "100"}},
	}

	want := []VMInterface{
		{VM: "vm1", Host: "compute1", Interface: "0", Alias: "EXP_1", VLAN: 101},
		{VM: "vm1", Host: "compute1", Interface: "2", Alias: "", VLAN: 100},
	}

	if diff := cmp.Diff(want, vmInterfaces(vms)); diff != "" {
		t.Errorf("vmInterfaces() mismatch (-want +got):\n%s", diff)
	}
}
