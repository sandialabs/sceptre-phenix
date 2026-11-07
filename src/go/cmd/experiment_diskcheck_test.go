package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	dct "phenix/util/diskchain/diskchaintest"
)

// brokenDiskExperiment is the stored experiment name, running when started is not empty, whose VM a boots from a
// snapshot of an image backed by itself, and that image.
func brokenDiskExperiment(t *testing.T, name, started string) (store.Config, string) {
	t.Helper()

	image := t.TempDir() + "/loop.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{Backing: "loop.qc2"})

	snapshot := true

	exp := types.NewExperiment(store.ConfigMetadata{Name: name})
	exp.Spec.SetExperimentName(name)
	exp.Spec.SetBaseDir(t.TempDir())
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: []*v1.Node{{
		GeneralF:  &v1.General{HostnameF: "a", SnapshotF: &snapshot},
		HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: image}}},
	}}})
	exp.Status.SetStartTime(started)

	return store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}, image
}

// runExperimentCmd runs phenix experiment with args against a mock store that holds cfg and allows exactly writes
// writes, which change cfg.
func runExperimentCmd(t *testing.T, cfg *store.Config, writes int, sub *cobra.Command, args ...string) error {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = *cfg

		return nil
	}).AnyTimes()

	if writes > 0 {
		m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
			*cfg = *c

			return nil
		}).Times(writes)
	}

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	store.DefaultStore = m //nolint:reassign // install test double

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	experimentCmd := newExperimentCmd()
	experimentCmd.AddCommand(sub)
	root.AddCommand(experimentCmd)
	root.SetArgs(append([]string{"experiment"}, args...))

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	_, err := root.ExecuteC()

	return err
}

// A start refused for a broken disk prints each VM, disk and problem, not a humanized error. Its only store writes
// add the start marker and remove it, so the experiment stays stopped with no marker.
func TestExperimentStartPrintsBrokenDisks(t *testing.T) { //nolint:paralleltest // replaces the store
	cfg, image := brokenDiskExperiment(t, "foo", "")

	err := runExperimentCmd(t, &cfg, 2, newExperimentStartCmd(), "start", "foo")

	want := "unable to start the foo experiment: qemu cannot open 1 disk on 1 VM: " +
		"1 error occurred:\n\t* VM a: disk " + image + ": backing chain loops back to " + image
	if err == nil || err.Error() != want {
		t.Errorf("experiment start error = %v, want %q", err, want)
	}

	exp, err := types.DecodeExperimentFromConfig(cfg)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig() error = %v", err)
	}

	if exp.Running() || !exp.Status.Starting().IsZero() {
		t.Errorf("stored experiment after a refused start: running %t, start marker %+v; want stopped with no marker",
			exp.Running(), exp.Status.Starting())
	}
}

// A restart whose start would fail on a broken disk is refused before the stop, which would write the store.
func TestExperimentRestartChecksDisksBeforeStopping(t *testing.T) { //nolint:paralleltest // replaces the store
	cfg, image := brokenDiskExperiment(t, "foo", "2024-01-01T00:00:00Z")

	err := runExperimentCmd(t, &cfg, 0, newExperimentRestartCmd(), "restart", "foo")

	want := "unable to restart the foo experiment, which is still running: qemu cannot open 1 disk " +
		"on 1 VM: 1 error occurred:\n\t* VM a: disk " + image + ": backing chain loops back to " + image
	if err == nil || err.Error() != want {
		t.Errorf("experiment restart error = %v, want %q", err, want)
	}
}

// A start error for broken disks keeps the error, so its list prints, and so does a start that another process runs.
// Any other start error stays humanized, pointing to the error logs.
func TestStartErrorHumanizesOtherErrors(t *testing.T) {
	t.Parallel()

	broken := fmt.Errorf("qemu cannot open 1 disk on 1 VM: %w", experiment.ErrBrokenDisks)

	tests := map[string]struct {
		err        error
		wantPrefix string
		wantBroken bool
	}{
		"broken disks": {
			err: broken, wantPrefix: "unable to start the foo experiment: qemu cannot open 1 disk on 1 VM", wantBroken: true,
		},
		"wrapped broken disks": {
			err: fmt.Errorf("starting: %w", broken), wantPrefix: "unable to start the foo experiment: starting: qemu", wantBroken: true,
		},
		"other error": {
			err: errors.New("minimega is down"), wantPrefix: "Unable to start the foo experiment (search error logs for ",
		},
		"starting elsewhere": {
			err: &experiment.StartingError{
				Name: "foo", Marker: ifaces.StartMarker{Since: "2026-11-02T10:00:00Z", PID: 7, Host: "elsewhere"},
			},
			wantPrefix: "unable to start the foo experiment: experiment foo is already starting (process 7 on host elsewhere",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := startError(tc.err, "foo")
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("startError() = %v, want an error that starts with %q", err, tc.wantPrefix)
			}

			if got := errors.Is(err, experiment.ErrBrokenDisks); got != tc.wantBroken {
				t.Errorf("errors.Is(startError(), ErrBrokenDisks) = %t, want %t", got, tc.wantBroken)
			}
		})
	}
}
