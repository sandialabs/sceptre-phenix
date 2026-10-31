package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/types"
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

// runExperimentCmd runs phenix experiment with args against a mock store that reads cfg and allows no write.
func runExperimentCmd(t *testing.T, cfg store.Config, sub *cobra.Command, args ...string) error {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = cfg

		return nil
	}).AnyTimes()

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

// A start refused for a broken disk prints each VM, disk and problem, not a humanized error.
func TestExperimentStartPrintsBrokenDisks(t *testing.T) { //nolint:paralleltest // replaces the store
	cfg, image := brokenDiskExperiment(t, "foo", "")

	err := runExperimentCmd(t, cfg, newExperimentStartCmd(), "start", "foo")

	want := "unable to start the foo experiment: qemu cannot open 1 disk on 1 VM: " +
		"1 error occurred:\n\t* VM a: disk " + image + ": backing chain loops back to " + image
	if err == nil || err.Error() != want {
		t.Errorf("experiment start error = %v, want %q", err, want)
	}
}

// A restart whose start would fail on a broken disk is refused before the stop, which would write the store.
func TestExperimentRestartChecksDisksBeforeStopping(t *testing.T) { //nolint:paralleltest // replaces the store
	cfg, image := brokenDiskExperiment(t, "foo", "2024-01-01T00:00:00Z")

	err := runExperimentCmd(t, cfg, newExperimentRestartCmd(), "restart", "foo")

	want := "unable to restart the foo experiment, which is still running: qemu cannot open 1 disk " +
		"on 1 VM: 1 error occurred:\n\t* VM a: disk " + image + ": backing chain loops back to " + image
	if err == nil || err.Error() != want {
		t.Errorf("experiment restart error = %v, want %q", err, want)
	}
}

// Any other start error stays humanized, pointing to the error logs.
func TestStartErrorHumanizesOtherErrors(t *testing.T) {
	t.Parallel()

	err := startError(errors.New("minimega is down"), "foo")
	if err == nil || !strings.HasPrefix(err.Error(), "Unable to start the foo experiment (search error logs for ") {
		t.Errorf("startError = %v, want a humanized error", err)
	}
}
