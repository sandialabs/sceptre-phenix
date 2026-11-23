package experiment

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

type recordingClusterFiles struct {
	file.ClusterFiles

	deleted []string
}

func (f *recordingClusterFiles) DeleteFile(path string) error {
	f.deleted = append(f.deleted, path)

	return nil
}

type headnodeMM struct {
	mm.MM

	clearErr error
}

func (headnodeMM) Headnode() string {
	return "headnode"
}

func (m headnodeMM) ClearNamespace(string) error {
	return m.clearErr
}

func TestDeleteSnapshotsDeletesInternalVMSnapshots(t *testing.T) {
	clusterFiles := new(recordingClusterFiles)
	originalClusterFiles := file.DefaultClusterFiles
	originalMM := mm.DefaultMM

	file.DefaultClusterFiles = clusterFiles //nolint:reassign // install test double
	mm.DefaultMM = headnodeMM{}             //nolint:reassign // install test double

	t.Cleanup(func() {
		file.DefaultClusterFiles = originalClusterFiles //nolint:reassign // restore default
		mm.DefaultMM = originalMM                       //nolint:reassign // restore default
	})

	external := true
	snapshot := true
	exp := types.NewExperiment(store.ConfigMetadata{Name: "test-exp"})
	exp.Spec.SetTopology(&v1.TopologySpec{
		NodesF: []*v1.Node{
			{GeneralF: &v1.General{HostnameF: "vm-one"}},
			{
				GeneralF: &v1.General{HostnameF: "vm-two", SnapshotF: &snapshot},
				HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{
					{ImageF: "os.qc2"},
					{ImageF: "data.qc2"},
				}},
			},
			{
				GeneralF: &v1.General{HostnameF: "vm-three"},
				HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{
					{ImageF: "os.qc2"},
					{ImageF: "data.qc2"},
				}},
			},
			{GeneralF: &v1.General{HostnameF: "outside"}, ExternalF: &external},
		},
	})

	if err := deleteSnapshots(exp); err != nil {
		t.Fatalf("deleting snapshots: %v", err)
	}

	want := []string{
		"headnode_test-exp_vm-one_snapshot",
		"headnode_test-exp_vm-two_snapshot",
		"headnode_test-exp_vm-two_snapshot_1",
		"headnode_test-exp_vm-three_snapshot",
		"headnode_test-exp_vm-three_snapshot_1",
	}
	if !slices.Equal(clusterFiles.deleted, want) {
		t.Fatalf("expected deleted snapshots %v, got %v", want, clusterFiles.deleted)
	}
}

// The snapshots of drives removed from a VM since it ran are deleted too, but not those of other VMs.
//
//nolint:paralleltest // replaces package globals and shares the fake minimega
func TestDeleteSnapshotsDeletesSnapshotsOfRemovedDrives(t *testing.T) {
	originalClusterFiles := file.DefaultClusterFiles
	originalMM := mm.DefaultMM

	file.DefaultClusterFiles = file.MMClusterFiles{} //nolint:reassign // use minimega, faked below
	mm.DefaultMM = headnodeMM{}                      //nolint:reassign // install test double

	t.Cleanup(func() {
		file.DefaultClusterFiles = originalClusterFiles //nolint:reassign // restore default
		mm.DefaultMM = originalMM                       //nolint:reassign // restore default
	})

	listed := []string{
		"headnode_test-exp_web_snapshot",
		"headnode_test-exp_web_snapshot_1",
		"headnode_test-exp_web_snapshot_2",
		"headnode_test-exp_web_snapshot_1_snapshot",
	}

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if !strings.HasPrefix(cmd.Base, "file list ") {
			return nil
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"dir", "name", "size"}, rowsOf(listed)...)}
	})

	exp := types.NewExperiment(store.ConfigMetadata{Name: "test-exp"})
	exp.Spec.SetTopology(&v1.TopologySpec{
		NodesF: []*v1.Node{{
			GeneralF:  &v1.General{HostnameF: "web"},
			HardwareF: &v1.Hardware{DrivesF: []*v1.Drive{{ImageF: "os.qc2"}}},
		}},
	})

	if err := deleteSnapshots(exp); err != nil {
		t.Fatalf("deleting snapshots: %v", err)
	}

	var deleted []string

	for _, cmd := range mmtest.Bases(received()) {
		if name, ok := strings.CutPrefix(cmd, "file delete "); ok {
			deleted = append(deleted, name)
		}
	}

	want := []string{
		"headnode_test-exp_web_snapshot",
		"headnode_test-exp_web_snapshot_1",
		"headnode_test-exp_web_snapshot_2",
	}
	if diff := cmp.Diff(want, deleted); diff != "" {
		t.Errorf("deleted snapshots mismatch (-want +got):\n%s", diff)
	}
}

// rowsOf is a `file list` row for each name.
func rowsOf(names []string) [][]string {
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, []string{"", name, "1"})
	}

	return rows
}

func TestStopVMsDoesNotDeleteSnapshotsWhenNamespaceClearFails(t *testing.T) {
	clusterFiles := new(recordingClusterFiles)
	originalClusterFiles := file.DefaultClusterFiles
	originalMM := mm.DefaultMM

	file.DefaultClusterFiles = clusterFiles                         //nolint:reassign // install test double
	mm.DefaultMM = headnodeMM{clearErr: errors.New("clear failed")} //nolint:reassign // install test double

	t.Cleanup(func() {
		file.DefaultClusterFiles = originalClusterFiles //nolint:reassign // restore default
		mm.DefaultMM = originalMM                       //nolint:reassign // restore default
	})

	exp := types.NewExperiment(store.ConfigMetadata{Name: "test-exp"})
	exp.Spec.SetExperimentName("test-exp")
	exp.Spec.SetTopology(&v1.TopologySpec{
		NodesF: []*v1.Node{{GeneralF: &v1.General{HostnameF: "vm-one"}}},
	})

	killed, err := stopVMsAndDeleteSnapshots(exp, false, nil)
	if killed || !errors.Is(err, ErrTeardownFailed) {
		t.Fatalf("stopVMsAndDeleteSnapshots() = %t, %v, want false and an error wrapping ErrTeardownFailed", killed, err)
	}

	if len(clusterFiles.deleted) != 0 {
		t.Fatalf("expected no deleted snapshots, got %v", clusterFiles.deleted)
	}
}
