package experiment

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/go-multierror"

	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/mm"
	"phenix/util/notes"
)

// startMM fakes the minimega calls Start makes after launching VMs, and the head node's name, recording
// each namespace it clears. Any other mm.MM method panics.
type startMM struct {
	mm.MM

	readErr, clearErr, destroyErr error
	cleared                       []string
	destroyed                     []destroyCall
}

// destroyCall is one call of DestroyBridge.
type destroyCall struct {
	Bridge string
	Hosts  []string
}

func (m *startMM) DestroyBridge(bridge string, hosts ...string) error {
	m.destroyed = append(m.destroyed, destroyCall{Bridge: bridge, Hosts: hosts})

	return m.destroyErr
}

func (m *startMM) ReadScriptFromFile(string, string) error {
	return m.readErr
}

// Headnode names the head node, which a snapshot's name holds.
func (*startMM) Headnode() string {
	return "head"
}

func (m *startMM) ClearNamespace(ns string) error {
	m.cleared = append(m.cleared, ns)

	return m.clearErr
}

func useStartMM(t *testing.T, fake *startMM) *startMM {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore default

	mm.DefaultMM = fake //nolint:reassign // install test double

	return fake
}

// errorTexts lists the text of each error err holds.
func errorTexts(err error) []string {
	merr := &multierror.Error{}
	if !errors.As(err, &merr) {
		return []string{err.Error()}
	}

	texts := make([]string, 0, len(merr.Errors))
	for _, e := range merr.Errors {
		texts = append(texts, e.Error())
	}

	return texts
}

func TestMMStepFailed(t *testing.T) { //nolint:paralleltest // replaces package globals
	failed := errors.New("vm launch failed")

	tests := []struct {
		name         string
		asWarn       bool
		err          error
		wantErr      string   // empty for none
		wantWarnings []string // the warnings recorded on the context
		wantCleared  []string
	}{
		{
			name: "does nothing for a step that worked",
		},
		{
			name:        "clears the namespace and returns the step's error",
			err:         failed,
			wantErr:     "launching experiment VMs: vm launch failed",
			wantCleared: []string{"exp"},
		},
		{
			name:         "records the error as a warning instead",
			asWarn:       true,
			err:          failed,
			wantWarnings: []string{"vm launch failed"},
		},
		{
			name:         "records each error of a multierror as a warning",
			asWarn:       true,
			err:          multierror.Append(nil, errors.New("vm a failed"), errors.New("vm b failed")),
			wantWarnings: []string{"vm a failed", "vm b failed"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useStartMM(t, &startMM{})
			ctx := notes.Context(context.Background(), false)

			err := mmStepFailed(ctx, "exp", test.asWarn, "launching experiment VMs", test.err)

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != test.wantErr {
				t.Errorf("mmStepFailed error = %q, want %q", gotErr, test.wantErr)
			}

			if test.err != nil && test.wantErr != "" && !errors.Is(err, test.err) {
				t.Errorf("mmStepFailed error = %v, want it to wrap %v", err, test.err)
			}

			got := notes.Warnings(ctx, true)
			warnings := make([]string, 0, len(got))

			for _, w := range got {
				warnings = append(warnings, w.Error())
			}

			if diff := cmp.Diff(test.wantWarnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("mmStepFailed warnings mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(test.wantCleared, fake.cleared, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("mmStepFailed cleared namespaces mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAfterLaunchStopsAtTheCCScript(t *testing.T) { //nolint:paralleltest // replaces package globals
	readErr := errors.New("bad cc script")
	useStartMM(t, &startMM{readErr: readErr})

	exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
	exp.Spec.SetExperimentName("exp")
	exp.Spec.SetTopology(&v1.TopologySpec{
		NodesF: []*v1.Node{{GeneralF: &v1.General{HostnameF: "vm1"}, CommandsF: []string{"cc exec ls"}}},
	})

	step, err := afterLaunch(context.Background(), exp, false, pendingSteps{ccScript: "exp-cc.mm", postStart: true}, nil, nil)

	if step != stepC2Script {
		t.Errorf("afterLaunch step = %d, want %d (the cc script)", step, stepC2Script)
	}

	if !errors.Is(err, readErr) || err.Error() != "reading minimega cc script: bad cc script" {
		t.Errorf("afterLaunch error = %v, want the wrapped cc script error", err)
	}
}

func TestAbortStart(t *testing.T) { //nolint:paralleltest // replaces package globals
	failed := errors.New("reading minimega cc script: bad cc script")

	tests := []struct {
		name     string
		step     startStep
		clearErr error
		want     []string
	}{
		{
			name: "clears the namespace and returns the step's error",
			step: stepC2Script,
			want: []string{failed.Error()},
		},
		{
			name:     "adds a failure clearing the namespace",
			step:     stepDelayedVMs,
			clearErr: errors.New("namespace busy"),
			want:     []string{failed.Error(), "killing experiment VMs: namespace busy"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := useStartMM(t, &startMM{clearErr: test.clearErr})

			exp := types.NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Spec.SetExperimentName("exp")

			err := abortStart(exp, false, test.step, failed)
			if err == nil {
				t.Fatal("abortStart error = nil, want the step's error")
			}

			if diff := cmp.Diff(test.want, errorTexts(err)); diff != "" {
				t.Errorf("abortStart errors mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"exp"}, fake.cleared); diff != "" {
				t.Errorf("abortStart cleared namespaces mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// storeDiskExperiment stores experiment exp, stopped, with VM a booting from a snapshot of image, and returns its
// base directory.
func storeDiskExperiment(t *testing.T, image string) string {
	t.Helper()

	storetest.Use(t)

	base := t.TempDir()

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"baseDir":        base,
			"topology": map[string]any{"nodes": []any{map[string]any{
				"general":  map[string]any{"hostname": "a", "snapshot": true},
				"hardware": map[string]any{"drives": []any{map[string]any{"image": image}}},
			}}},
		},
		Status: map[string]any{},
	})
	if err != nil {
		t.Fatalf("store.Create(experiment exp) error = %v", err)
	}

	return base
}

// Start refuses an experiment with a broken disk before it tells minimega anything: it writes no minimega script,
// reads none, clears no namespace, and the experiment stays stopped.
func TestStartRefusesBrokenDisks(t *testing.T) { //nolint:paralleltest // replaces package globals
	image := t.TempDir() + "/a.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{Backing: "a.qc2"})

	base := storeDiskExperiment(t, image)
	fake := useStartMM(t, &startMM{readErr: errors.New("minimega got the script")})

	err := Start(notes.Context(t.Context(), false), StartWithName("exp"))

	want := "qemu cannot open 1 disk on 1 VM: 1 error occurred:\n\t* VM a: disk " + image +
		": backing chain loops back to " + image
	if err == nil || err.Error() != want || !errors.Is(err, ErrBrokenDisks) {
		t.Fatalf("Start error = %v, want %q matching ErrBrokenDisks", err, want)
	}

	if len(fake.cleared) != 0 {
		t.Errorf("Start cleared namespaces %q, want none", fake.cleared)
	}

	if _, err := os.Stat(base + "/mm_files/exp.mm"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("os.Stat(minimega script) error = %v, want no script", err)
	}

	if experimentNamed(t, "exp").Running() {
		t.Error("experiment is running, want it stopped")
	}
}

// A dry run starts an experiment with a broken disk and reports the problem as a warning, also for a VM the startup
// app marks do not boot because its first disk image is missing.
func TestStartDryRunWarnsOfBrokenDisks(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name  string
		setup func(t *testing.T, image string)
		want  func(image string) string // the start of the warning
		exact bool                      // whether it is the only warning, all of it
	}{
		{
			name: "a backing chain that loops",
			setup: func(t *testing.T, image string) {
				t.Helper()
				dct.WriteQcow2(t, image, dct.Qcow2{Backing: "a.qc2"})
			},
			want:  func(image string) string { return "VM a: disk " + image + ": backing chain loops back to " + image },
			exact: true,
		},
		{
			name:  "a missing image the startup app stops the VM for",
			setup: func(*testing.T, string) {},
			want:  func(image string) string { return "VM a: disk " + image + ": disk image " + image + " is missing" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			image := t.TempDir() + "/a.qc2"
			tc.setup(t, image)

			storeDiskExperiment(t, image)
			useStartMM(t, &startMM{})

			ctx := notes.Context(t.Context(), false)

			if err := Start(ctx, StartWithName("exp"), StartWithDryRun(true)); err != nil {
				t.Fatalf("Start(dry run) error = %v, want none", err)
			}

			got := notes.Warnings(ctx, true)
			warnings := make([]string, 0, len(got))

			for _, w := range got {
				warnings = append(warnings, w.Error())
			}

			want := tc.want(image)

			if tc.exact {
				if diff := cmp.Diff([]string{want}, warnings); diff != "" {
					t.Errorf("Start(dry run) warnings mismatch (-want +got):\n%s", diff)
				}

				return
			}

			if !slices.ContainsFunc(warnings, func(w string) bool { return strings.HasPrefix(w, want) }) {
				t.Errorf("Start(dry run) warnings = %q, want one starting with %q", warnings, want)
			}
		})
	}
}

// Start refuses an experiment it cannot find or that is already running before it runs an app or tells minimega
// anything.
func TestStartRefusesBeforeTheApps(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name  string
		exp   string
		start string
		want  string
	}{
		{name: "an experiment not in the store", exp: "missing", want: "getting experiment missing from store"},
		{
			name: "a running experiment", exp: "exp", start: stopTestStart,
			want: "experiment already running (started at: " + stopTestStart + ")",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			storeExperiment(t, "exp", map[string]any{"startTime": tc.start})

			fake := useStartMM(t, &startMM{readErr: errors.New("minimega got the script")})

			err := Start(notes.Context(t.Context(), false), StartWithName(tc.exp))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Start(%s) error = %v, want one containing %q", tc.exp, err, tc.want)
			}

			if len(fake.cleared) != 0 {
				t.Errorf("Start(%s) cleared namespaces %q, want none", tc.exp, fake.cleared)
			}

			if got := experimentNamed(t, "exp").Status.StartTime(); got != tc.start {
				t.Errorf("stored start time after Start(%s) = %q, want %q", tc.exp, got, tc.start)
			}
		})
	}
}

// cleanupApp is a scenario app whose cleanup stage fails with err and counts its calls; its other stages do nothing.
type cleanupApp struct {
	name     string
	err      error
	cleanups *int
}

func (a *cleanupApp) Init(...app.Option) error                           { return nil }
func (a *cleanupApp) Name() string                                       { return a.name }
func (a *cleanupApp) Configure(context.Context, *types.Experiment) error { return nil }
func (a *cleanupApp) PreStart(context.Context, *types.Experiment) error  { return nil }
func (a *cleanupApp) PostStart(context.Context, *types.Experiment) error { return nil }
func (a *cleanupApp) Running(context.Context, *types.Experiment) error   { return nil }

func (a *cleanupApp) Cleanup(context.Context, *types.Experiment) error {
	*a.cleanups++

	return a.err
}

// After the post-start apps fail, abortStart runs the apps' cleanup stage before it clears the namespace, and adds
// a cleanup failure to the errors; after an earlier step it runs no cleanup.
func TestAbortStartCleansUpAfterThePostStartApps(t *testing.T) { //nolint:paralleltest // replaces package globals
	failed := errors.New("applying apps to experiment: app failed")

	// the registry keeps each name, so each run (-count) registers a new one
	name := "test-abort-cleanup-" + strings.ToLower(rand.Text())
	cleanups := new(int)

	if err := app.RegisterUserApp(name, func() app.App {
		return &cleanupApp{name: name, err: errors.New("cleanup failed"), cleanups: cleanups}
	}); err != nil {
		t.Fatalf("app.RegisterUserApp(%s) error = %v", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"scenario":       map[string]any{"apps": []any{map[string]any{"name": name}}},
		},
	})
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(exp) error = %v", err)
	}

	tests := []struct {
		name         string
		step         startStep
		want         []string
		wantCleanups int
	}{
		{name: "an earlier step", step: stepDelayedVMs, want: []string{failed.Error()}},
		{
			name: "the post-start apps", step: stepPostStartApps, wantCleanups: 1,
			want: []string{
				failed.Error(),
				"cleaning up app experiments: applying user app " + name + " for action cleanup: cleanup failed",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			*cleanups = 0
			fake := useStartMM(t, &startMM{})

			got := errorTexts(abortStart(exp, false, tc.step, failed))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("abortStart(step %d) errors mismatch (-want +got):\n%s", tc.step, diff)
			}

			if *cleanups != tc.wantCleanups {
				t.Errorf("abortStart(step %d) ran the cleanup stage %d times, want %d", tc.step, *cleanups, tc.wantCleanups)
			}

			if diff := cmp.Diff([]string{"exp"}, fake.cleared); diff != "" {
				t.Errorf("abortStart(step %d) cleared namespaces mismatch (-want +got):\n%s", tc.step, diff)
			}
		})
	}
}
