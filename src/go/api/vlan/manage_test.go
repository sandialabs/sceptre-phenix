package vlan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// fakeCluster is what the test's fake minimega holds.
type fakeCluster struct {
	namespaces map[string]map[string]string // namespace, alias, VLAN ID
	vms        map[string][][]string        // namespace: name and vlan columns of each VM
	blacklist  []string
	removable  bool
	refuse     string // the error of `clear vlans blacklist`, if any
	down       string // the error of every read, if any
}

// useCluster points minimega at a fake that answers from c, and drops the
// cached minimega side, until the test ends. It returns the commands received.
func useCluster(t *testing.T, c *fakeCluster) func() []mmtest.Command {
	t.Helper()

	Forget()
	t.Cleanup(Forget)

	// minimega's answers kept from earlier tests, such as whether it can remove a blacklisted ID
	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	original := mm.DefaultMM
	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	return mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch base := cmd.Base; {
		case c.down != "":
			return []*minicli.Response{mmtest.Error("head", c.down)}
		case base == "namespace":
			rows := make([][]string, 0, len(c.namespaces))
			for name := range c.namespaces {
				rows = append(rows, []string{name, "", "false"})
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"namespace", "vlans", "active"}, rows...)}
		case base == "vlans":
			var rows [][]string
			for alias, id := range c.namespaces[cmd.Namespace] {
				rows = append(rows, []string{alias, id})
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"alias", "vlan"}, rows...)}
		case base == "vm info":
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "vlan"}, c.vms[cmd.Namespace]...)}
		case base == "vlans blacklist":
			var rows [][]string
			for _, id := range c.blacklist {
				rows = append(rows, []string{id})
			}

			return []*minicli.Response{mmtest.Tabular("head", []string{"vlan"}, rows...)}
		case base == "help clear vlans":
			if c.removable {
				return []*minicli.Response{mmtest.Text("head", helpClearVLANsNew)}
			}

			return []*minicli.Response{mmtest.Text("head", helpClearVLANsOld)}
		case strings.HasPrefix(base, "clear vlans blacklist ") && c.refuse != "":
			return []*minicli.Response{mmtest.Error("head", c.refuse)}
		}

		return nil
	})
}

// helpClearVLANsNew and helpClearVLANsOld are minimega's help for `clear
// vlans` with and without blacklist removal.
const (
	helpClearVLANsNew = "Usage:\n\tclear vlans [prefix]\n\tclear vlans blacklist <vlan>\n\nClear aliases."
	helpClearVLANsOld = "Usage:\n\tclear vlans [prefix]\n\nClear aliases, including blacklisted VLANS."
)

// sentBases lists the commands that change minimega.
func sentChanges(cmds []mmtest.Command) []string {
	var out []string

	for _, c := range cmds {
		if strings.HasPrefix(c.Base, "clear vlans") || strings.HasPrefix(c.Base, "vlans blacklist ") {
			out = append(out, c.Namespace+": "+c.Base)
		}
	}

	return out
}

// storedPins reads back an experiment's pins and range.
func storedPins(t *testing.T, name string) (map[string]int, int, int) {
	t.Helper()

	exp, err := experiment.Get(name)
	if err != nil {
		t.Fatalf("experiment.Get(%s) error = %v", name, err)
	}

	v := exp.Spec.VLANs()

	return v.Aliases(), v.Min(), v.Max()
}

// manageTestExperiments: stopped a pins X 102 in 100–199 with Y unpinned;
// running b; stopped c pins Z 150.
func useManageTestExperiments(t *testing.T) {
	t.Helper()

	a := newExperiment("a", map[string]int{"X": 102, "Y": 0})
	if err := a.Spec.SetVLANRange(100, 199, false); err != nil {
		t.Fatal(err)
	}

	b := newExperiment("b", map[string]int{"P": 0, "Q": 0})
	b.Status.SetStartTime("2024-01-01T00:00:00Z")

	c := newExperiment("c", map[string]int{"Z": 150})
	if err := c.Spec.SetVLANRange(150, 249, false); err != nil {
		t.Fatal(err)
	}

	useExperiments(t, a, b, c)
}

// PinAlias pins an alias of a stopped experiment, refuses a running one, an
// invalid ID and, unless forced, a conflict or a minimega that is down.
func TestPinAlias(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	for name, tc := range map[string]struct {
		exp, alias string
		id         int
		force      bool
		down       string
		wantErr    error
		conflict   bool
		wantPins   map[string]int
	}{
		"minimega down":                   {exp: "a", alias: "Y", id: 120, down: "minimega is down", wantErr: ErrMinimegaDown},
		"minimega down, forced":           {exp: "a", alias: "Y", id: 120, down: "minimega is down", force: true, wantPins: map[string]int{"X": 102, "Y": 120}},
		"a free ID":                       {exp: "a", alias: "Y", id: 120, wantPins: map[string]int{"X": 102, "Y": 120}},
		"another's live ID":               {exp: "a", alias: "Y", id: 110, conflict: true},
		"another's live ID, forced":       {exp: "a", alias: "Y", id: 110, force: true, wantPins: map[string]int{"X": 102, "Y": 110}},
		"a blacklisted ID":                {exp: "a", alias: "Y", id: 103, conflict: true},
		"an ID outside the range":         {exp: "a", alias: "Y", id: 250, wantErr: ErrInvalid},
		"an ID outside 1–4094":            {exp: "a", alias: "Y", id: 4095, wantErr: ErrInvalid},
		"an alias the spec does not have": {exp: "a", alias: "NONE", id: 120, wantErr: ErrAliasNotFound},
		"a running experiment":            {exp: "b", alias: "P", id: 120, wantErr: ErrRunning},
	} {
		t.Run(name, func(t *testing.T) {
			useManageTestExperiments(t)
			useCluster(t, &fakeCluster{
				namespaces: map[string]map[string]string{"b": {"P": "101", "Q": "110"}},
				blacklist:  []string{"103"},
				down:       tc.down,
			})

			err := PinAlias(context.Background(), seeAll, tc.exp, tc.alias, tc.id, tc.force)

			var conflict *ConflictError

			switch {
			case tc.conflict:
				if !errors.As(err, &conflict) || len(conflict.Issues) == 0 {
					t.Fatalf("PinAlias(%s, %s, %d) error = %v, want a conflict with issues", tc.exp, tc.alias, tc.id, err)
				}
			case !errors.Is(err, tc.wantErr):
				t.Fatalf("PinAlias(%s, %s, %d) error = %v, want %v", tc.exp, tc.alias, tc.id, err, tc.wantErr)
			}

			if tc.wantPins == nil {
				return
			}

			got, _, _ := storedPins(t, tc.exp)
			if diff := cmp.Diff(tc.wantPins, got); diff != "" {
				t.Errorf("PinAlias(%s, %s, %d) stored pins mismatch (-want +got):\n%s", tc.exp, tc.alias, tc.id, diff)
			}
		})
	}
}

// UnpinAlias, ChangeRange and ClearRange change only a stopped experiment's
// spec; ChangeRange refuses a range that leaves out a pin, or overlaps
// another's unless forced.
func TestRangeAndUnpin(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	type want struct {
		Pins     map[string]int
		Min, Max int
	}

	for name, tc := range map[string]struct {
		change   func() error
		wantErr  error
		conflict bool
		want     *want
	}{
		"unpin": {
			change: func() error { return UnpinAlias("a", "X") },
			want:   &want{Pins: map[string]int{"X": 0, "Y": 0}, Min: 100, Max: 199},
		},
		"unpin of a running experiment": {change: func() error { return UnpinAlias("b", "P") }, wantErr: ErrRunning},
		"unpin of an unknown alias":     {change: func() error { return UnpinAlias("a", "NONE") }, wantErr: ErrAliasNotFound},
		"a free range": {
			change: func() error { return ChangeRange(context.Background(), seeAll, "a", 100, 120, false) },
			want:   &want{Pins: map[string]int{"X": 102, "Y": 0}, Min: 100, Max: 120},
		},
		"a range that overlaps": {
			change:   func() error { return ChangeRange(context.Background(), seeAll, "a", 100, 160, false) },
			conflict: true,
		},
		"a range that overlaps, forced": {
			change: func() error { return ChangeRange(context.Background(), seeAll, "a", 100, 160, true) },
			want:   &want{Pins: map[string]int{"X": 102, "Y": 0}, Min: 100, Max: 160},
		},
		"a range without a pin": {
			change:  func() error { return ChangeRange(context.Background(), seeAll, "a", 103, 120, false) },
			wantErr: ErrInvalid,
		},
		"a range lowest last": {
			change:  func() error { return ChangeRange(context.Background(), seeAll, "a", 120, 100, false) },
			wantErr: ErrInvalid,
		},
		"a range of a running experiment": {
			change:  func() error { return ChangeRange(context.Background(), seeAll, "b", 100, 120, false) },
			wantErr: ErrRunning,
		},
		"clear the range": {
			change: func() error { return ClearRange("a") },
			want:   &want{Pins: map[string]int{"X": 102, "Y": 0}, Min: 0, Max: 0},
		},
		"clear a running experiment's range": {change: func() error { return ClearRange("b") }, wantErr: ErrRunning},
	} {
		t.Run(name, func(t *testing.T) {
			useManageTestExperiments(t)
			useCluster(t, &fakeCluster{})

			err := tc.change()

			var conflict *ConflictError

			switch {
			case tc.conflict:
				if !errors.As(err, &conflict) {
					t.Fatalf("error = %v, want a conflict", err)
				}
			case !errors.Is(err, tc.wantErr):
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			if tc.want == nil {
				return
			}

			pins, lo, hi := storedPins(t, "a")
			if diff := cmp.Diff(*tc.want, want{Pins: pins, Min: lo, Max: hi}); diff != "" {
				t.Errorf("stored spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// PinLive copies minimega's IDs into a running experiment's spec, and
// refuses a stopped experiment.
func TestPinLive(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useManageTestExperiments(t)
	useCluster(t, &fakeCluster{namespaces: map[string]map[string]string{"b": {"P": "101", "Q": "110", "TAP": "111"}}})

	live, err := PinLive(context.Background(), "b")
	if err != nil {
		t.Fatalf("PinLive(b) error = %v", err)
	}

	want := map[string]int{"P": 101, "Q": 110, "TAP": 111}
	if diff := cmp.Diff(want, live); diff != "" {
		t.Errorf("PinLive(b) mismatch (-want +got):\n%s", diff)
	}

	if pins, _, _ := storedPins(t, "b"); !cmp.Equal(want, pins) {
		t.Errorf("PinLive(b) stored pins = %v, want %v", pins, want)
	}

	if _, err := PinLive(context.Background(), "a"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("PinLive(a) error = %v, want %v", err, ErrNotRunning)
	}
}

// The blacklist changes send minimega's commands only when they apply, and
// report why they cannot.
func TestBlacklistChanges(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	for name, tc := range map[string]struct {
		cluster fakeCluster
		change  func() error
		wantErr error
		sent    []string
	}{
		"add": {
			change: func() error { return AddToBlacklist(context.Background(), 100) },
			sent:   []string{": vlans blacklist 100"},
		},
		"add an ID on the blacklist": {
			cluster: fakeCluster{blacklist: []string{"100"}},
			change:  func() error { return AddToBlacklist(context.Background(), 100) },
		},
		"add an allocated ID": {
			cluster: fakeCluster{namespaces: map[string]map[string]string{"b": {"P": "100"}}},
			change:  func() error { return AddToBlacklist(context.Background(), 100) },
			wantErr: ErrInUse,
		},
		"add an ID outside 1–4094": {
			change: func() error { return AddToBlacklist(context.Background(), 0) }, wantErr: ErrInvalid,
		},
		"remove": {
			cluster: fakeCluster{blacklist: []string{"100"}, removable: true},
			change:  func() error { return RemoveFromBlacklist(context.Background(), 100) },
			sent:    []string{": clear vlans blacklist 100"},
		},
		"remove without minimega support": {
			cluster: fakeCluster{blacklist: []string{"100"}},
			change:  func() error { return RemoveFromBlacklist(context.Background(), 100) },
			wantErr: ErrUnsupported,
		},
		"remove an ID not on the blacklist": {
			cluster: fakeCluster{removable: true},
			change:  func() error { return RemoveFromBlacklist(context.Background(), 100) },
			wantErr: ErrNotFound,
		},
		"remove an ID that minimega no longer blacklists": {
			cluster: fakeCluster{blacklist: []string{"100"}, removable: true, refuse: "VLAN 100 is not blacklisted"},
			change:  func() error { return RemoveFromBlacklist(context.Background(), 100) },
			wantErr: ErrNotFound,
			sent:    []string{": clear vlans blacklist 100"},
		},
		"remove an ID that a VM uses": {
			cluster: fakeCluster{blacklist: []string{"100"}, removable: true, refuse: "VLAN 100 is in use by vm1"},
			change:  func() error { return RemoveFromBlacklist(context.Background(), 100) },
			wantErr: ErrInUse,
			sent:    []string{": clear vlans blacklist 100"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useManageTestExperiments(t)
			received := useCluster(t, &tc.cluster)

			if err := tc.change(); !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.sent, sentChanges(received())); diff != "" {
				t.Errorf("sent changes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Release clears an unowned namespace's aliases, and refuses an experiment's
// namespace, an unknown one and one whose VMs are on its IDs.
func TestRelease(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	cluster := fakeCluster{
		namespaces: map[string]map[string]string{
			"old-demo": {"P": "400"}, "busy": {"Q": "401"}, "b": {"P": "101"},
		},
		vms: map[string][][]string{"busy": {{"vm1", "[Q (401)]"}}},
	}

	for name, tc := range map[string]struct {
		namespace string
		wantErr   error
		sent      []string
	}{
		"an unowned namespace":       {namespace: "old-demo", sent: []string{"old-demo: clear vlans"}},
		"an experiment's namespace":  {namespace: "b", wantErr: ErrInUse},
		"an unknown namespace":       {namespace: "none", wantErr: ErrNotFound},
		"a namespace whose VMs stay": {namespace: "busy", wantErr: ErrInUse},
	} {
		t.Run(name, func(t *testing.T) {
			useManageTestExperiments(t)
			received := useCluster(t, &cluster)

			if err := Release(context.Background(), tc.namespace); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Release(%s) error = %v, want %v", tc.namespace, err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.sent, sentChanges(received())); diff != "" {
				t.Errorf("Release(%s) sent changes mismatch (-want +got):\n%s", tc.namespace, diff)
			}
		})
	}
}

// Read merges the store's experiments with minimega's side, reuses that side
// while the same experiments run, and reports minimega down as not OK.
func TestRead(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	useManageTestExperiments(t)
	received := useCluster(t, &fakeCluster{
		namespaces: map[string]map[string]string{"b": {"P": "101"}},
		vms:        map[string][][]string{"b": {{"vm1", "[P (101)]"}}},
		blacklist:  []string{"999"},
		removable:  true,
	})

	in, observed, err := Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if !in.Minimega.OK || !observed.Known {
		t.Fatalf("Read() minimega = %+v, observed %+v, want it read", in.Minimega, observed)
	}

	wantNS := Namespace{
		Name: "b", Aliases: map[string]int{"P": 101},
		VMs: []VMInterface{{VM: "vm1", Host: "head", Interface: "0", Alias: "P", VLAN: 101}},
	}
	if diff := cmp.Diff(wantNS, in.Minimega.Namespaces["b"]); diff != "" {
		t.Errorf("Read() namespace b mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]int{999}, in.Minimega.Blacklist); diff != "" || !in.Minimega.BlacklistRemoval {
		t.Errorf("Read() blacklist = %v, removal %t, want [999] and true", in.Minimega.Blacklist, in.Minimega.BlacklistRemoval)
	}

	if got := len(in.Experiments); got != 3 {
		t.Errorf("Read() = %d experiments, want 3", got)
	}

	before := len(received())

	if _, _, err := Read(context.Background()); err != nil {
		t.Fatalf("second Read() error = %v", err)
	}

	if after := len(received()); after != before {
		t.Errorf("second Read() sent %d commands, want none (cached)", after-before)
	}

	t.Run("minimega down", func(t *testing.T) {
		Forget()
		mmtest.Use(t, func(mmtest.Command) []*minicli.Response { return mmtest.Disconnect() })

		in, observed, err := Read(context.Background())
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}

		if in.Minimega.OK || in.Minimega.Err == "" || observed.Known {
			t.Errorf("Read() minimega = %+v, observed %+v, want not OK with an error", in.Minimega, observed)
		}
	})
}
