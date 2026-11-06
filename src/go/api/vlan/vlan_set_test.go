package vlan

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
)

// vlanTestExperiments returns a stopped experiment with a VLAN range and an
// alias, and a running experiment with the VLAN IDs minimega assigned.
func vlanTestExperiments(t *testing.T) (*types.Experiment, *types.Experiment) {
	t.Helper()

	stopped := newExperiment("stopped", map[string]int{"MYVLAN": 105})
	if err := stopped.Spec.SetVLANRange(100, 200, false); err != nil {
		t.Fatalf("SetVLANRange(100, 200) error = %v", err)
	}

	running := newExperiment("running", map[string]int{"A": 0, "B": 0})
	running.Status.SetStartTime("2024-01-01T00:00:00Z")
	running.Status.SetVLANs(map[string]int{"A": 205, "B": 201})

	return stopped, running
}

// useFailingList points the store at a mock that cannot list configs.
func useFailingList(t *testing.T) {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).Return(nil, errors.New("store down"))

	orig := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = orig }) //nolint:reassign // restore after test
}

// Aliases gives the aliases of every experiment, or of one experiment, from
// the spec of a stopped experiment and the status of a running one.
func TestAliases(t *testing.T) {
	stopped, running := vlanTestExperiments(t)
	useExperiments(t, stopped, running)

	all := map[string]map[string]int{
		"stopped": {"MYVLAN": 105},
		"running": {"A": 205, "B": 201},
	}

	tests := []struct {
		name string
		opts []Option
		want map[string]map[string]int
	}{
		{name: "all experiments", want: all},
		{name: "one experiment", opts: []Option{Experiment("running")}, want: map[string]map[string]int{"running": all["running"]}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Aliases(test.opts...)
			if err != nil {
				t.Fatalf("Aliases(%s) error = %v", test.name, err)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Aliases(%s) mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}

	if _, err := Aliases(Experiment("missing")); err == nil {
		t.Error("Aliases(Experiment(missing)) error = nil, want an error")
	}
}

// Aliases and Ranges return the error of a store that cannot list
// experiments.
func TestAliasesRangesListError(t *testing.T) {
	useFailingList(t)

	if _, err := Aliases(); err == nil {
		t.Error("Aliases() error = nil, want an error")
	}

	useFailingList(t)

	if _, err := Ranges(); err == nil {
		t.Error("Ranges() error = nil, want an error")
	}
}

// Ranges gives the spec range of a stopped experiment, and the lowest and
// highest assigned VLAN ID of a running one.
func TestRanges(t *testing.T) {
	stopped, running := vlanTestExperiments(t)
	useExperiments(t, stopped, running)

	tests := []struct {
		name string
		opts []Option
		want map[string][2]int
	}{
		{name: "all experiments", want: map[string][2]int{"stopped": {100, 200}, "running": {201, 205}}},
		{name: "one experiment", opts: []Option{Experiment("stopped")}, want: map[string][2]int{"stopped": {100, 200}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Ranges(test.opts...)
			if err != nil {
				t.Fatalf("Ranges(%s) error = %v", test.name, err)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Ranges(%s) mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}

	if _, err := Ranges(Experiment("missing")); err == nil {
		t.Error("Ranges(Experiment(missing)) error = nil, want an error")
	}
}

// SetAlias saves a new alias, replaces one only when forced, and refuses
// incomplete options or an ID outside the experiment range.
func TestSetAlias(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
		want    map[string]int
	}{
		{name: "new alias", opts: []Option{Experiment("stopped"), Alias("NEW"), ID(150)}, want: map[string]int{"MYVLAN": 105, "NEW": 150}},
		{name: "forced", opts: []Option{Experiment("stopped"), Alias("MYVLAN"), ID(110), Force(true)}, want: map[string]int{"MYVLAN": 110}},
		{name: "exists", opts: []Option{Experiment("stopped"), Alias("MYVLAN"), ID(110)}, wantErr: true},
		{name: "outside range", opts: []Option{Experiment("stopped"), Alias("NEW"), ID(300)}, wantErr: true},
		{name: "missing experiment", opts: []Option{Experiment("missing"), Alias("NEW"), ID(150)}, wantErr: true},
		{name: "no experiment", opts: []Option{Alias("NEW"), ID(150)}, wantErr: true},
		{name: "no alias", opts: []Option{Experiment("stopped"), ID(150)}, wantErr: true},
		{name: "no ID", opts: []Option{Experiment("stopped"), Alias("NEW")}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stopped, _ := vlanTestExperiments(t)
			useExperiments(t, stopped)

			err := SetAlias(test.opts...)
			if test.wantErr {
				if err == nil {
					t.Errorf("SetAlias(%s) error = nil, want an error", test.name)
				}

				return
			}

			if err != nil {
				t.Fatalf("SetAlias(%s) error = %v", test.name, err)
			}

			got, err := Aliases(Experiment("stopped"))
			if err != nil {
				t.Fatalf("Aliases(stopped) error = %v", err)
			}

			if diff := cmp.Diff(test.want, got["stopped"]); diff != "" {
				t.Errorf("aliases after SetAlias(%s) mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}
}

// SetRange saves a new range, replaces one only when forced, and refuses
// incomplete options, a reversed range or a range that leaves out an alias.
func TestSetRange(t *testing.T) {
	tests := []struct {
		name    string
		exp     func(t *testing.T) *types.Experiment
		opts    []Option
		wantErr bool
		want    [2]int
	}{
		{
			name: "new range",
			exp:  func(*testing.T) *types.Experiment { return newExperiment("stopped", nil) },
			opts: []Option{Experiment("stopped"), Min(10), Max(20)},
			want: [2]int{10, 20},
		},
		{
			name: "unassigned alias",
			exp: func(*testing.T) *types.Experiment {
				return newExperiment("stopped", map[string]int{"NOID": 0, "MYVLAN": 150})
			},
			opts: []Option{Experiment("stopped"), Min(100), Max(200)},
			want: [2]int{100, 200},
		},
		{name: "forced", opts: []Option{Experiment("stopped"), Min(101), Max(110), Force(true)}, want: [2]int{101, 110}},
		{name: "exists", opts: []Option{Experiment("stopped"), Min(101), Max(110)}, wantErr: true},
		{name: "leaves out an alias", opts: []Option{Experiment("stopped"), Min(110), Max(120), Force(true)}, wantErr: true},
		{name: "reversed", opts: []Option{Experiment("stopped"), Min(20), Max(10)}, wantErr: true},
		{name: "missing experiment", opts: []Option{Experiment("missing"), Min(10), Max(20)}, wantErr: true},
		{name: "no experiment", opts: []Option{Min(10), Max(20)}, wantErr: true},
		{name: "no min", opts: []Option{Experiment("stopped"), Max(20)}, wantErr: true},
		{name: "no max", opts: []Option{Experiment("stopped"), Min(10)}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exp, _ := vlanTestExperiments(t)
			if test.exp != nil {
				exp = test.exp(t)
			}

			useExperiments(t, exp)

			err := SetRange(test.opts...)
			if test.wantErr {
				if err == nil {
					t.Errorf("SetRange(%s) error = nil, want an error", test.name)
				}

				return
			}

			if err != nil {
				t.Fatalf("SetRange(%s) error = %v", test.name, err)
			}

			got, err := Ranges(Experiment("stopped"))
			if err != nil {
				t.Fatalf("Ranges(stopped) error = %v", err)
			}

			if diff := cmp.Diff(test.want, got["stopped"]); diff != "" {
				t.Errorf("range after SetRange(%s) mismatch (-want +got):\n%s", test.name, diff)
			}
		})
	}
}
