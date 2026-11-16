package experiment

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm"
)

// scheduleMM gives the cluster hosts the schedulers place VMs on; other mm.MM methods panic.
type scheduleMM struct {
	mm.MM

	hosts mm.Hosts
}

func (m scheduleMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	return m.hosts, nil
}

// useMM makes fake the minimega client until the test ends.
func useMM(t *testing.T, fake mm.MM) {
	t.Helper()

	original := mm.DefaultMM
	mm.DefaultMM = fake //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore default
}

// storeValidExperiment stores experiment name, with the given status and one VM, vm1, in a form config
// validation takes, and returns its base directory.
func storeValidExperiment(t *testing.T, name string, status map[string]any, spec map[string]any) string {
	t.Helper()

	base := t.TempDir() + "/" + name

	full := map[string]any{
		"experimentName": name,
		"baseDir":        base,
		"deployMode":     "all",
		"vlans":          map[string]any{"aliases": map[string]any{}},
		"schedules":      map[string]any{},
		"topology":       map[string]any{"nodes": []any{launchNode("vm1", "a.qc2", nil)}},
	}

	maps.Copy(full, spec)

	if err := store.Create(hookConfig(name, full, status)); err != nil {
		t.Fatalf("store.Create(experiment %s) error = %v", name, err)
	}

	return base
}

// gotErr is err's text, "" for none.
func gotErr(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// Schedule places the VMs of a stopped experiment with the algorithm it is given, stores where and returns the
// scheduler's warnings; it refuses an experiment it cannot find or that is running, and reports a scheduler or store
// that fails.
func TestSchedule(t *testing.T) { //nolint:paralleltest // replaces package globals
	twoVMs := []any{launchNode("vm1", "a.qc2", nil), launchNode("vm2", "a.qc2", nil)}

	tests := []struct {
		name         string
		exp          string
		status       map[string]any
		nodes        []any
		schedules    map[string]any
		spec         map[string]any
		updateErr    error
		wantErr      string
		wantSched    map[string]string
		wantWarnings []string
	}{
		{name: "places each VM", exp: "exp", wantSched: map[string]string{"vm1": "node1"}},
		{
			name: "keeps a VM placed by hand", exp: "exp",
			nodes: twoVMs, schedules: map[string]any{"vm1": "node2"},
			wantSched: map[string]string{"vm1": "node2", "vm2": "node1"},
		},
		{
			name: "warns of a host that is not in the cluster", exp: "exp",
			nodes: twoVMs, schedules: map[string]any{"vm1": "gone"},
			wantSched:    map[string]string{"vm1": "gone", "vm2": "node1"},
			wantWarnings: []string{"VM vm1 stays on host gone, which is not a schedulable cluster host"},
		},
		{name: "an experiment not in the store", exp: "missing", wantErr: "getting experiment missing from store: "},
		{
			name: "a running experiment", exp: "exp", status: map[string]any{"startTime": stopTestStart},
			wantErr: "experiment already running (started at: " + stopTestStart + ")",
		},
		{
			name: "an experiment that does not decode", exp: "exp", spec: map[string]any{"experimentName": []any{1}},
			wantErr: "decoding experiment from config: ",
		},
		{
			name: "an experiment with no VMs", exp: "exp", nodes: []any{},
			wantErr: "running scheduler algorithm: no VMs defined for experiment",
		},
		{
			name: "a store that cannot record the schedule", exp: "exp", updateErr: errors.New("store is read-only"),
			wantErr: "updating experiment config: store is read-only",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := useRecoverStore(t)
			useMM(t, scheduleMM{hosts: mm.Hosts{{Name: "node1"}, {Name: "node2"}}})

			spec := map[string]any{}
			if tc.nodes != nil {
				spec["topology"] = map[string]any{"nodes": tc.nodes}
			}

			if tc.schedules != nil {
				spec["schedules"] = tc.schedules
			}

			maps.Copy(spec, tc.spec)

			storeValidExperiment(t, "exp", tc.status, spec)

			s.updateErr = tc.updateErr

			scheduled, warnings, err := Schedule(t.Context(), ScheduleForName(tc.exp), ScheduleWithAlgorithm("round-robin"))
			if got := gotErr(err); !strings.HasPrefix(got, tc.wantErr) || (tc.wantErr == "") != (got == "") {
				t.Fatalf("Schedule(%s) error = %q, want one starting with %q", tc.exp, got, tc.wantErr)
			}

			if tc.status != nil && !errors.Is(err, ErrExperimentRunning) {
				t.Errorf("Schedule(%s) error = %v, want it to wrap ErrExperimentRunning", tc.exp, err)
			}

			if tc.wantErr != "" {
				if warnings != nil {
					t.Errorf("Schedule(%s) failed with warnings %q, want none", tc.exp, warnings)
				}

				return
			}

			if diff := cmp.Diff(tc.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Schedule(%s) warnings mismatch (-want +got):\n%s", tc.exp, diff)
			}

			stored := experimentNamed(t, "exp")

			if diff := cmp.Diff(tc.wantSched, stored.Spec.Schedules()); diff != "" {
				t.Errorf("stored schedules after Schedule(%s) mismatch (-want +got):\n%s", tc.exp, diff)
			}

			if diff := cmp.Diff(tc.wantSched, scheduled.Spec.Schedules()); diff != "" {
				t.Errorf("Schedule(%s) returned schedules mismatch (-want +got):\n%s", tc.exp, diff)
			}

			if scheduled.Metadata.Revision != stored.Metadata.Revision {
				t.Errorf("Schedule(%s) returned revision %d, want the stored %d", tc.exp, scheduled.Metadata.Revision,
					stored.Metadata.Revision)
			}
		})
	}
}

// changingMM gives the cluster hosts after it runs change, as a VM edit or a
// start made while the scheduler reads the hosts.
type changingMM struct {
	mm.MM

	change func()
}

func (m changingMM) GetClusterHosts(bool, bool) (mm.Hosts, error) {
	m.change()

	return mm.Hosts{{Name: "node1"}, {Name: "node2"}}, nil
}

// Schedule stores nothing over a change or a start made while it schedules: it fails with store.ErrConflict and the
// stored experiment keeps that change. It refuses an experiment that a live process starts before it reads the hosts.
func TestScheduleRevisionConflict(t *testing.T) { //nolint:paralleltest // replaces package globals
	host, _ := os.Hostname()
	live := map[string]any{"starting": map[string]any{"since": "2026-01-02T03:04:05Z", "pid": os.Getpid(), "host": host}}

	tests := []struct {
		name    string
		status  map[string]any
		change  func(t *testing.T)
		wantErr error
		check   func(t *testing.T, exp *types.Experiment)
	}{
		{
			name: "a VM edit while scheduling",
			change: func(t *testing.T) {
				t.Helper()

				c, _ := store.NewConfig("experiment/exp")
				if err := store.Get(c); err != nil {
					t.Fatalf("store.Get(experiment/exp) error = %v", err)
				}

				c.Spec["schedules"] = map[string]any{"vm1": "edited"}

				if err := store.Update(c); err != nil {
					t.Fatalf("store.Update(experiment/exp) error = %v", err)
				}
			},
			wantErr: store.ErrConflict,
			check: func(t *testing.T, exp *types.Experiment) {
				t.Helper()

				if diff := cmp.Diff(map[string]string{"vm1": "edited"}, exp.Spec.Schedules()); diff != "" {
					t.Errorf("stored schedules after a conflict mismatch (-edit +stored):\n%s", diff)
				}
			},
		},
		{
			name: "a start while scheduling",
			change: func(t *testing.T) {
				t.Helper()

				if _, _, _, err := claimStart("exp"); err != nil {
					t.Fatalf("claimStart(exp) error = %v", err)
				}
			},
			wantErr: store.ErrConflict,
			check: func(t *testing.T, exp *types.Experiment) {
				t.Helper()

				if exp.Status.Starting().IsZero() {
					t.Error("stored start marker after a conflict is gone, want the start's marker kept")
				}

				if diff := cmp.Diff(map[string]string{}, exp.Spec.Schedules(), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("stored schedules after a conflict mismatch (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:    "a live start before scheduling",
			status:  live,
			change:  func(t *testing.T) { t.Helper(); t.Error("Schedule read the cluster hosts of a starting experiment") },
			wantErr: ErrExperimentStarting,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useRecoverStore(t)
			useMM(t, changingMM{change: func() { tc.change(t) }})
			storeValidExperiment(t, "exp", tc.status, nil)

			got, _, err := Schedule(t.Context(), ScheduleForName("exp"), ScheduleWithAlgorithm("round-robin"))
			if !errors.Is(err, tc.wantErr) || got != nil {
				t.Fatalf("Schedule(exp) = %v, %v; want nil, %v", got, err, tc.wantErr)
			}

			if tc.check != nil {
				tc.check(t, experimentNamed(t, "exp"))
			}
		})
	}
}

// Save stores the spec it is given in place of the experiment's spec, and refuses an experiment it is not given or
// cannot find.
func TestSave(t *testing.T) { //nolint:paralleltest // replaces the package store
	s := useRecoverStore(t)
	storeExperiment(t, "exp", map[string]any{})

	if err := Save(); gotErr(err) != "experiment name required" {
		t.Errorf("Save() error = %v, want %q", err, "experiment name required")
	}

	if err := Save(SaveWithName("missing")); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("Save(missing) error = %v, want one wrapping store.ErrNotExist", err)
	}

	exp := experimentNamed(t, "exp")
	exp.Spec.SetBaseDir("/data/exp")

	if err := Save(SaveWithName("exp"), SaveWithSpec(exp.Spec)); err != nil {
		t.Fatalf("Save(exp, new spec) error = %v", err)
	}

	if got := experimentNamed(t, "exp").Spec.BaseDir(); got != "/data/exp" {
		t.Errorf("stored base directory after Save(exp) = %q, want %q", got, "/data/exp")
	}

	// with no spec, the stored spec is written back as it is
	if err := Save(SaveWithName("exp")); err != nil {
		t.Fatalf("Save(exp) error = %v", err)
	}

	if got := experimentNamed(t, "exp").Spec.BaseDir(); got != "/data/exp" {
		t.Errorf("stored base directory after Save(exp) with no spec = %q, want %q", got, "/data/exp")
	}

	s.updateErr = errors.New("store is read-only")

	if err := Save(SaveWithName("exp")); gotErr(err) != "saving experiment config: store is read-only" {
		t.Errorf("Save(exp) to a read-only store error = %v, want %q", err, "saving experiment config: store is read-only")
	}
}

// Reconfigure runs the configure stage of the apps of a stopped experiment and stores it through the config update,
// so the update hook runs; it refuses an experiment it cannot find or that is running.
func TestReconfigure(t *testing.T) { //nolint:paralleltest // replaces package globals
	long := strings.Repeat("x", MaxBridgeNameLength+1)

	tests := []struct {
		name       string
		exp        string
		status     map[string]any
		mode       common.BridgingMode
		wantErr    string
		wantBridge string
	}{
		{name: "a stopped experiment", exp: "exp", wantBridge: "exp", mode: common.BridgeModeAuto},
		{name: "an experiment not in the store", exp: "missing", wantErr: "getting experiment missing from store: "},
		{name: "a running experiment", exp: "exp", status: map[string]any{"startTime": stopTestStart}, wantErr: "experiment is running"},
		{
			name: "a config the update hook refuses", exp: long, mode: common.BridgeModeAuto,
			wantErr: "updating experiment config: calling config hook: experiment name must be 15 characters or less",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			useBridgeSettings(t, tc.mode, false)

			name := tc.exp
			if name == "missing" {
				name = "exp"
			}

			storeValidExperiment(t, name, tc.status, map[string]any{"defaultBridge": "br1"})

			err := Reconfigure(t.Context(), tc.exp)
			if got := gotErr(err); !strings.HasPrefix(got, tc.wantErr) || (tc.wantErr == "") != (got == "") {
				t.Fatalf("Reconfigure(%s) error = %q, want one starting with %q", tc.exp, got, tc.wantErr)
			}

			want := tc.wantBridge
			if want == "" {
				want = "br1"
			}

			if got := experimentNamed(t, name).Spec.DefaultBridge(); got != want {
				t.Errorf("stored default bridge after Reconfigure(%s) = %q, want %q", tc.exp, got, want)
			}
		})
	}
}

// Delete deletes a stopped experiment's config, C2 responses, snapshots and base directory once each, and runs the
// delete hooks; it refuses an experiment it cannot find or that is running.
func TestDelete(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name        string
		exp         string
		status      map[string]any
		deleteErr   error
		wantErr     []string // parts of the error
		wantDeleted []string
		wantStored  bool
		wantHooks   int
	}{
		{
			name: "a stopped experiment", exp: "exp",
			wantDeleted: []string{"exp/miniccc_responses", "head_exp_vm1_snapshot"}, wantHooks: 1,
		},
		{
			name: "files that cannot be deleted", exp: "exp", deleteErr: errors.New("mesh unreachable"),
			wantErr:     []string{"deleting experiment exp: ", "mesh unreachable"},
			wantDeleted: []string{"exp/miniccc_responses"}, wantHooks: 1,
		},
		{
			name: "a running experiment", exp: "exp", status: map[string]any{"startTime": stopTestStart},
			wantErr: []string{"cannot delete a running experiment"}, wantStored: true,
		},
		{name: "an experiment not in the store", exp: "missing", wantErr: []string{"getting experiment missing: "}, wantStored: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			useStartMM(t, &startMM{})

			files := useDeletedFiles(t)
			files.err = tc.deleteErr
			deleted := countHooks(t, "delete")

			base := storeValidExperiment(t, "exp", tc.status, nil)
			if err := os.MkdirAll(base, 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", base, err)
			}

			err := Delete(tc.exp)

			if (err == nil) != (len(tc.wantErr) == 0) {
				t.Errorf("Delete(%s) error = %v, want one containing %q", tc.exp, err, tc.wantErr)
			}

			for _, part := range tc.wantErr {
				if !strings.Contains(gotErr(err), part) {
					t.Errorf("Delete(%s) error = %v, want it to contain %q", tc.exp, err, part)
				}
			}

			if diff := cmp.Diff(tc.wantDeleted, files.files(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Delete(%s) deleted files mismatch (-want +got):\n%s", tc.exp, diff)
			}

			c, _ := store.NewConfig("experiment/exp")
			if stored := store.Get(c) == nil; stored != tc.wantStored {
				t.Errorf("experiment exp stored after Delete(%s) = %t, want %t", tc.exp, stored, tc.wantStored)
			}

			if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) == tc.wantStored {
				t.Errorf("os.Stat(base directory) after Delete(%s) error = %v, want it gone %t", tc.exp, err, !tc.wantStored)
			}

			if *deleted != tc.wantHooks {
				t.Errorf("Delete(%s) ran %d delete hooks, want %d", tc.exp, *deleted, tc.wantHooks)
			}
		})
	}
}

// List, Get and Running skip or refuse an experiment config that cannot be decoded.
func TestUndecodableExperiment(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)
	storeExperiment(t, "good", map[string]any{"startTime": stopTestStart})

	if err := store.Create(hookConfig("bad", map[string]any{"topology": "not a topology"}, nil)); err != nil {
		t.Fatalf("store.Create(experiment bad) error = %v", err)
	}

	exps, err := List()

	names := make([]string, 0, len(exps))
	for _, exp := range exps {
		names = append(names, exp.Metadata.Name)
	}

	if diff := cmp.Diff([]string{"good"}, names); diff != "" {
		t.Errorf("List() experiments mismatch (-want +got):\n%s", diff)
	}

	if !strings.Contains(gotErr(err), "decoding experiment bad from config: ") {
		t.Errorf("List() error = %v, want one naming experiment bad", err)
	}

	if _, err := Get("bad"); !strings.HasPrefix(gotErr(err), "decoding experiment from config: ") {
		t.Errorf("Get(bad) error = %v, want a decoding error", err)
	}

	if _, err := Get(""); gotErr(err) != "no experiment name provided" {
		t.Errorf("Get(\"\") error = %v, want %q", err, "no experiment name provided")
	}

	for name, want := range map[string]bool{"good": true, "bad": false, "missing": false} {
		if got := Running(name); got != want {
			t.Errorf("Running(%s) = %t, want %t", name, got, want)
		}
	}
}

// Trigger stores the experiment after a stage other than running, and reports a store that cannot; the running stage
// refuses a stopped experiment.
func TestTriggerStoresTheExperiment(t *testing.T) { //nolint:paralleltest // replaces the package store
	s := useRecoverStore(t)
	storeExperiment(t, "exp", map[string]any{})

	if err := Trigger(context.Background(), "exp", app.ActionConfigure); err != nil {
		t.Fatalf("Trigger(exp, configure) error = %v", err)
	}

	if got := s.updates.Load(); got != 1 {
		t.Errorf("Trigger(exp, configure) store updates = %d, want 1", got)
	}

	s.updateErr = errors.New("store is read-only")

	want := "saving experiment after triggering configure stage: "
	if err := Trigger(context.Background(), "exp", app.ActionConfigure); !strings.HasPrefix(gotErr(err), want) ||
		!errors.Is(err, s.updateErr) {
		t.Errorf("Trigger(exp, configure) to a read-only store error = %v, want one starting with %q", err, want)
	}

	if err := Trigger(context.Background(), "exp", app.ActionRunning); gotErr(err) != "experiment is not running" {
		t.Errorf("Trigger(exp, running) error = %v, want %q", err, "experiment is not running")
	}
}
