package experiment

import (
	"errors"
	"slices"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util/file"
	"phenix/util/mm"
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
	exp := types.NewExperiment(store.ConfigMetadata{Name: "test-exp"})
	exp.Spec.SetTopology(&v1.TopologySpec{
		NodesF: []*v1.Node{
			{GeneralF: &v1.General{HostnameF: "vm-one"}},
			{GeneralF: &v1.General{HostnameF: "outside"}, ExternalF: &external},
		},
	})

	if err := deleteSnapshots(exp); err != nil {
		t.Fatalf("deleting snapshots: %v", err)
	}

	want := []string{"headnode_test-exp_vm-one_snapshot"}
	if !slices.Equal(clusterFiles.deleted, want) {
		t.Fatalf("expected deleted snapshots %v, got %v", want, clusterFiles.deleted)
	}
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

	err := stopVMsAndDeleteSnapshots(exp, false)
	if err == nil {
		t.Fatal("expected namespace clear error")
	}

	if len(clusterFiles.deleted) != 0 {
		t.Fatalf("expected no deleted snapshots, got %v", clusterFiles.deleted)
	}
}
