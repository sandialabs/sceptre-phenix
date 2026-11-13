package vlan

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// clusterTestRead is when the test's minimega side was read.
var clusterTestRead = time.Date(2026, 11, 5, 15, 4, 5, 0, time.UTC) //nolint:gochecknoglobals // fixed test time

// seeAll sees every experiment, the blacklist and unowned namespaces.
var seeAll = View{Visible: func(string) bool { return true }, All: true} //nolint:gochecknoglobals // test view

// seeOnly sees only the named experiments.
func seeOnly(names ...string) View {
	return View{Visible: func(e string) bool { return slices.Contains(names, e) }, All: false}
}

// up is a minimega side that answered, with the namespaces and blacklist.
func up(blacklist []int, namespaces ...Namespace) Minimega {
	m := Minimega{
		OK: true, Err: "", Read: clusterTestRead, Namespaces: map[string]Namespace{}, Blacklist: blacklist,
		BlacklistRemoval: false,
	}

	for _, ns := range namespaces {
		m.Namespaces[ns.Name] = ns
	}

	return m
}

func upStatus() MinimegaStatus {
	return MinimegaStatus{OK: true, Read: "2026-11-05T15:04:05Z", Error: "", BlacklistRemoval: false}
}

func exp(name string, running bool, lo, hi int, pins map[string]int, vms ...VMInterface) ExperimentInput {
	return ExperimentInput{Name: name, Running: running, Min: lo, Max: hi, Pins: pins, VMs: vms}
}

func ns(name string, aliases map[string]int, vms ...VMInterface) Namespace {
	return Namespace{Name: name, Aliases: aliases, VMs: vms}
}

// testRow is a row of an experiment's alias, with no VMs.
func testRow(id int, alias, experiment, namespace string, running bool, phenix, minimega *int, states, issues []string) Row {
	return Row{
		ID: id, Alias: alias, Experiment: experiment, Namespace: namespace, Running: running, Owned: experiment != "",
		States: states, Sources: Sources{Phenix: phenix, Minimega: minimega}, VMs: 0, Hosts: []string{},
		Issues: issues,
	}
}

func summary(name string, running bool, lo, hi int, issues []string, aliases ...AliasSummary) ExperimentSummary {
	return ExperimentSummary{Name: name, Running: running, Min: lo, Max: hi, Aliases: aliases, Issues: issues}
}

func testIssue(kind string, id *int, alias string, exps []string, namespace string, rng *[2]int, msg string) Issue {
	return Issue{
		Kind: kind, Severity: Severity(kind), VLAN: id, Alias: alias, Experiments: exps, Namespace: namespace,
		Range: rng, Message: msg,
	}
}

var (
	pinnedOnly = []string{StatePinned}                 //nolint:gochecknoglobals // test states
	liveOnly   = []string{StateAllocated}              //nolint:gochecknoglobals // test states
	both       = []string{StatePinned, StateAllocated} //nolint:gochecknoglobals // test states
	none       = []string{}                            //nolint:gochecknoglobals // no issues
)

// Merge gives each owner, alias and ID a row, sorted by ID and then owner,
// marks each kind of issue on its
// rows and in the issues, names hidden experiments "another experiment" and
// gives the phenix side only when minimega is down.
//
//nolint:maintidx // one table
func TestMerge(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   Inputs
		view View
		want Report
	}{
		"pin and live ID agree": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", true, 0, 0, map[string]int{"X": 101},
					VMInterface{VM: "vm1", Host: "", Interface: "IF0", Alias: "X", VLAN: 0})},
				Minimega: up(nil, ns("a", map[string]int{"X": 101},
					VMInterface{VM: "vm1", Host: "compute1", Interface: "0", Alias: "X", VLAN: 101},
					VMInterface{VM: "vm2", Host: "compute2", Interface: "1", Alias: "X", VLAN: 101})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{{
					ID: 101, Alias: "X", Experiment: "a", Namespace: "a", Running: true, Owned: true, States: both,
					Sources: Sources{Phenix: intp(101), Minimega: intp(101)}, VMs: 2,
					Hosts: []string{"compute1", "compute2"}, Issues: none,
				}},
				Experiments: []ExperimentSummary{
					summary("a", true, 0, 0, none, AliasSummary{Alias: "X", Pinned: 101, Live: 101, VMs: 2}),
				},
				Issues: []Issue{}, Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"two stopped experiments pin one ID": {
			in: Inputs{
				Experiments: []ExperimentInput{
					exp("a", false, 0, 0, map[string]int{"X": 102}),
					exp("b", false, 0, 0, map[string]int{"Y": 102, "Z": 0}),
				},
				Minimega: up(nil),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(102, "X", "a", "", false, intp(102), nil, pinnedOnly, []string{KindDuplicatePin}),
					testRow(102, "Y", "b", "", false, intp(102), nil, pinnedOnly, []string{KindDuplicatePin}),
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 0, 0, []string{KindDuplicatePin}, AliasSummary{Alias: "X", Pinned: 102, Live: 0, VMs: 0}),
					summary("b", false, 0, 0, []string{KindDuplicatePin},
						AliasSummary{Alias: "Y", Pinned: 102, Live: 0, VMs: 0}, AliasSummary{Alias: "Z", Pinned: 0, Live: 0, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindDuplicatePin, intp(102), "X", []string{"a", "b"}, "", nil, "Also pinned by b (Y)."),
					testIssue(KindDuplicatePin, intp(102), "Y", []string{"b", "a"}, "", nil, "Also pinned by a (X)."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"a running experiment's live ID is another's pin": {
			in: Inputs{
				Experiments: []ExperimentInput{
					exp("a", true, 0, 0, map[string]int{"X": 0}),
					exp("b", false, 0, 0, map[string]int{"Y": 102}),
				},
				Minimega: up(nil, ns("a", map[string]int{"X": 102})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(102, "X", "a", "a", true, nil, intp(102), liveOnly, []string{KindDuplicatePin}),
					testRow(102, "Y", "b", "", false, intp(102), nil, pinnedOnly, []string{KindDuplicatePin}),
				},
				Experiments: []ExperimentSummary{
					summary("a", true, 0, 0, []string{KindDuplicatePin}, AliasSummary{Alias: "X", Pinned: 0, Live: 102, VMs: 0}),
					summary("b", false, 0, 0, []string{KindDuplicatePin}, AliasSummary{Alias: "Y", Pinned: 102, Live: 0, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindDuplicatePin, intp(102), "X", []string{"a", "b"}, "", nil, "Also pinned by b (Y)."),
					testIssue(KindDuplicatePin, intp(102), "Y", []string{"b", "a"}, "", nil, "Also used by a (X)."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"a pin on the blacklist": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", false, 0, 0, map[string]int{"X": 100})},
				Minimega:    up([]int{100, 999}),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					{
						ID: 100, Alias: "", Experiment: "", Namespace: "", Running: false, Owned: false,
						States: []string{StateBlacklisted}, Sources: Sources{Phenix: nil, Minimega: intp(100)}, VMs: 0,
						Hosts: []string{}, Issues: []string{KindPinBlacklisted},
					},
					testRow(100, "X", "a", "", false, intp(100), nil, pinnedOnly, []string{KindPinBlacklisted}),
					{
						ID: 999, Alias: "", Experiment: "", Namespace: "", Running: false, Owned: false,
						States: []string{StateBlacklisted}, Sources: Sources{Phenix: nil, Minimega: intp(999)}, VMs: 0,
						Hosts: []string{}, Issues: none,
					},
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 0, 0, []string{KindPinBlacklisted}, AliasSummary{Alias: "X", Pinned: 100, Live: 0, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindPinBlacklisted, intp(100), "X", []string{"a"}, "", nil, "minimega blacklists 100."),
				},
				Blacklist: &[]int{100, 999}, Minimega: upStatus(),
			},
		},
		"a namespace that no experiment has": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", false, 0, 0, map[string]int{"X": 400})},
				Minimega: up(nil, ns("old-demo", map[string]int{"P": 400, "Q": 401},
					VMInterface{VM: "left", Host: "compute1", Interface: "0", Alias: "Q", VLAN: 401}),
					ns("empty", map[string]int{})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					{
						ID: 400, Alias: "P", Experiment: "", Namespace: "old-demo", Running: false, Owned: false,
						States: liveOnly, Sources: Sources{Phenix: nil, Minimega: intp(400)}, VMs: 0, Hosts: []string{},
						Issues: []string{KindDuplicatePin, KindUnowned},
					},
					testRow(400, "X", "a", "", false, intp(400), nil, pinnedOnly, []string{KindDuplicatePin}),
					{
						ID: 401, Alias: "Q", Experiment: "", Namespace: "old-demo", Running: false, Owned: false,
						States: liveOnly, Sources: Sources{Phenix: nil, Minimega: intp(401)}, VMs: 1,
						Hosts: []string{"compute1"}, Issues: []string{KindUnowned},
					},
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 0, 0, []string{KindDuplicatePin}, AliasSummary{Alias: "X", Pinned: 400, Live: 0, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindDuplicatePin, intp(400), "X", []string{"a"}, "old-demo", nil,
						"Also used by the namespace old-demo."),
					testIssue(KindUnowned, nil, "", []string{}, "old-demo", nil, "No experiment has the namespace old-demo."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"minimega gives a running alias another ID than its pin": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", true, 100, 199, map[string]int{"CORP": 104})},
				Minimega:    up(nil, ns("a", map[string]int{"CORP": 110})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(104, "CORP", "a", "a", true, intp(104), intp(110), pinnedOnly, []string{KindIDDiffers}),
					testRow(110, "CORP", "a", "a", true, intp(104), intp(110), liveOnly, []string{KindIDDiffers}),
				},
				Experiments: []ExperimentSummary{
					summary("a", true, 100, 199, []string{KindIDDiffers}, AliasSummary{Alias: "CORP", Pinned: 104, Live: 110, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindIDDiffers, intp(104), "CORP", []string{"a"}, "", nil, "phēnix pins 104. minimega allocated 110."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"minimega has an alias that the spec does not": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", true, 0, 0, map[string]int{"C2": 0})},
				Minimega:    up(nil, ns("a", map[string]int{"C2": 300, "TAP_TEMP": 305})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(300, "C2", "a", "a", true, nil, intp(300), liveOnly, none),
					testRow(305, "TAP_TEMP", "a", "a", true, nil, intp(305), liveOnly, []string{KindMinimegaOnly}),
				},
				Experiments: []ExperimentSummary{
					summary("a", true, 0, 0, []string{KindMinimegaOnly},
						AliasSummary{Alias: "C2", Pinned: 0, Live: 300, VMs: 0},
						AliasSummary{Alias: "TAP_TEMP", Pinned: 0, Live: 305, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindMinimegaOnly, intp(305), "TAP_TEMP", []string{"a"}, "", nil, "Not in the experiment spec."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"two ranges overlap, and a pin is outside its range": {
			in: Inputs{
				Experiments: []ExperimentInput{
					exp("a", false, 100, 199, map[string]int{"X": 250}),
					exp("b", false, 150, 249, map[string]int{}),
					exp("c", false, 300, 0, map[string]int{}),
				},
				Minimega: up(nil),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(250, "X", "a", "", false, intp(250), nil, pinnedOnly, []string{KindOutOfRange}),
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 100, 199, []string{KindRangeOverlap, KindOutOfRange},
						AliasSummary{Alias: "X", Pinned: 250, Live: 0, VMs: 0}),
					summary("b", false, 150, 249, []string{KindRangeOverlap}),
					summary("c", false, 300, 0, none),
				},
				Issues: []Issue{
					testIssue(KindRangeOverlap, nil, "", []string{"a", "b"}, "", &[2]int{150, 199}, "150–199 overlaps b."),
					testIssue(KindRangeOverlap, nil, "", []string{"b", "a"}, "", &[2]int{150, 199}, "150–199 overlaps a."),
					testIssue(KindOutOfRange, intp(250), "X", []string{"a"}, "", &[2]int{100, 199}, "Outside the range 100–199."),
				},
				Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
		"a hidden experiment is another experiment": {
			in: Inputs{
				Experiments: []ExperimentInput{
					exp("a", false, 100, 199, map[string]int{"X": 102, "B": 100}),
					exp("secret", false, 150, 249, map[string]int{"Y": 102}),
				},
				Minimega: up([]int{100}, ns("old-demo", map[string]int{"P": 102})),
			},
			view: seeOnly("a"),
			want: Report{
				VLANs: []Row{
					testRow(100, "B", "a", "", false, intp(100), nil, pinnedOnly, []string{KindPinBlacklisted}),
					testRow(102, "X", "a", "", false, intp(102), nil, pinnedOnly, []string{KindDuplicatePin}),
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 100, 199, []string{KindDuplicatePin, KindPinBlacklisted, KindRangeOverlap},
						AliasSummary{Alias: "B", Pinned: 100, Live: 0, VMs: 0}, AliasSummary{Alias: "X", Pinned: 102, Live: 0, VMs: 0}),
				},
				Issues: []Issue{
					testIssue(KindDuplicatePin, intp(102), "X", []string{"a", HiddenExperiment}, "", nil,
						"Also used by another experiment."),
					testIssue(KindPinBlacklisted, intp(100), "B", []string{"a"}, "", nil, "minimega blacklists 100."),
					testIssue(KindRangeOverlap, nil, "", []string{"a", HiddenExperiment}, "", &[2]int{150, 199},
						"150–199 overlaps another experiment."),
				},
				Blacklist: nil, Minimega: upStatus(),
			},
		},
		"minimega is down": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", true, 0, 0, map[string]int{"X": 104},
					VMInterface{VM: "vm1", Host: "", Interface: "IF0", Alias: "X", VLAN: 0})},
				Minimega: Minimega{
					OK: false, Err: "dial unix /tmp/minimega/minimega: connect: no such file", Read: clusterTestRead,
					Namespaces: nil, Blacklist: nil, BlacklistRemoval: true,
				},
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{{
					ID: 104, Alias: "X", Experiment: "a", Namespace: "", Running: true, Owned: true, States: pinnedOnly,
					Sources: Sources{Phenix: intp(104), Minimega: nil}, VMs: 1, Hosts: []string{}, Issues: none,
				}},
				Experiments: []ExperimentSummary{
					summary("a", true, 0, 0, none, AliasSummary{Alias: "X", Pinned: 104, Live: 0, VMs: 1}),
				},
				Issues: []Issue{}, Blacklist: nil,
				Minimega: MinimegaStatus{
					OK: false, Read: "2026-11-05T15:04:05Z", Error: "dial unix /tmp/minimega/minimega: connect: no such file",
					BlacklistRemoval: false,
				},
			},
		},
		"a stopped experiment's leftover namespace is not compared": {
			in: Inputs{
				Experiments: []ExperimentInput{exp("a", false, 0, 0, map[string]int{"X": 104})},
				Minimega:    up(nil, ns("a", map[string]int{"X": 110, "EXTRA": 111})),
			},
			view: seeAll,
			want: Report{
				VLANs: []Row{
					testRow(104, "X", "a", "a", false, intp(104), intp(110), pinnedOnly, none),
					testRow(110, "X", "a", "a", false, intp(104), intp(110), liveOnly, none),
					testRow(111, "EXTRA", "a", "a", false, nil, intp(111), liveOnly, none),
				},
				Experiments: []ExperimentSummary{
					summary("a", false, 0, 0, none,
						AliasSummary{Alias: "EXTRA", Pinned: 0, Live: 111, VMs: 0},
						AliasSummary{Alias: "X", Pinned: 104, Live: 110, VMs: 0}),
				},
				Issues: []Issue{}, Blacklist: &[]int{}, Minimega: upStatus(),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Merge(tc.in, tc.view)

			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreUnexported(Report{}), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Merge() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Merge gives each row's lists as JSON lists, never null, also with no hosts.
func TestMergeListsAreNotNull(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Merge(Inputs{
		Experiments: []ExperimentInput{exp("a", false, 0, 0, map[string]int{"X": 102})},
		Minimega:    up([]int{999}),
	}, seeAll))
	if err != nil {
		t.Fatalf("json.Marshal(Merge()) error = %v", err)
	}

	var got struct {
		VLANs []map[string]any `json:"vlans"`
	}

	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for _, r := range got.VLANs {
		for _, field := range []string{"states", "hosts", "issues"} {
			if _, ok := r[field].([]any); !ok {
				t.Errorf("row %v %s = %v, want a JSON list", r["id"], field, r[field])
			}
		}
	}
}

// Severity gives each kind its severity.
func TestSeverity(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		KindDuplicatePin: SeverityConflict, KindPinBlacklisted: SeverityConflict, KindUnowned: SeverityConflict,
		KindIDDiffers: SeverityDifference, KindMinimegaOnly: SeverityDifference,
		KindRangeOverlap: SeverityWarning, KindOutOfRange: SeverityWarning,
	}

	for kind, severity := range want {
		if got := Severity(kind); got != severity {
			t.Errorf("Severity(%s) = %s, want %s", kind, got, severity)
		}
	}
}

// joinNames joins one, two and more names as a sentence does.
func TestJoinNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		names []string
		want  string
	}{
		{names: nil, want: ""},
		{names: []string{"a"}, want: "a"},
		{names: []string{"a", "b"}, want: "a and b"},
		{names: []string{"a", "b", "c"}, want: "a, b and c"},
	} {
		if got := joinNames(tc.names); got != tc.want {
			t.Errorf("joinNames(%q) = %q, want %q", tc.names, got, tc.want)
		}
	}
}
