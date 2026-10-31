package experiment

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/go-multierror"

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

	readErr, clearErr error
	cleared           []string
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

	step, err := afterLaunch(context.Background(), exp, false, pendingSteps{ccScript: "exp-cc.mm", postStart: true})

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

// A dry run starts an experiment with a broken disk and reports the problem as a warning.
func TestStartDryRunWarnsOfBrokenDisks(t *testing.T) { //nolint:paralleltest // replaces package globals
	image := t.TempDir() + "/a.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{Backing: "a.qc2"})

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

	want := []string{"VM a: disk " + image + ": backing chain loops back to " + image}
	if diff := cmp.Diff(want, warnings); diff != "" {
		t.Errorf("Start(dry run) warnings mismatch (-want +got):\n%s", diff)
	}
}

// The startup app marks a VM whose first disk image is missing do not boot; the check still reports the image.
func TestStartChecksVMTheStartupAppStops(t *testing.T) { //nolint:paralleltest // replaces package globals
	image := t.TempDir() + "/gone.qc2"

	storeDiskExperiment(t, image)
	useStartMM(t, &startMM{})

	ctx := notes.Context(t.Context(), false)

	if err := Start(ctx, StartWithName("exp"), StartWithDryRun(true)); err != nil {
		t.Fatalf("Start(dry run) error = %v, want none", err)
	}

	want := "VM a: disk " + image + ": disk image " + image + " is missing"
	for _, w := range notes.Warnings(ctx, true) {
		if strings.HasPrefix(w.Error(), want) {
			return
		}
	}

	t.Errorf("Start(dry run) warnings = %v, want one starting with %q", notes.Warnings(ctx, true), want)
}
