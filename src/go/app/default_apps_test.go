package app

import (
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

func TestDefaultAppsEnabled(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]any
		want        bool
	}{
		{name: "annotation omitted", want: true},
		{
			name:        "explicitly enabled",
			annotations: map[string]any{defaultAppsAnnotation: true},
			want:        true,
		},
		{
			name:        "explicitly disabled",
			annotations: map[string]any{defaultAppsAnnotation: false},
			want:        false,
		},
		{
			name:        "invalid value preserves default",
			annotations: map[string]any{defaultAppsAnnotation: "false"},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &v1.Node{
				AnnotationsF: tt.annotations,
			}

			if got := defaultAppsEnabled(node); got != tt.want {
				t.Fatalf("defaultAppsEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestStartupSkipsNodeWithDefaultAppsDisabled(t *testing.T) {
	node := &v1.Node{
		AnnotationsF: map[string]any{defaultAppsAnnotation: false},
		GeneralF: &v1.General{
			HostnameF: "unmanaged-apps",
		},
		HardwareF: &v1.Hardware{
			OSTypeF: "linux",
			DrivesF: []*v1.Drive{{ImageF: "test.qc2"}},
		},
	}
	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "default-apps-disabled"},
		Spec: &v1.ExperimentSpec{
			BaseDirF:  t.TempDir(),
			TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{node}},
		},
	}

	if err := (Startup{}).PreStart(t.Context(), exp); err != nil {
		t.Fatalf("PreStart() applied startup app to disabled node: %v", err)
	}

	if len(node.Injections()) != 0 {
		t.Fatalf("PreStart() added %d injections to disabled node", len(node.Injections()))
	}
}

// TestStartupChecksAlwaysRunForNodeWithDefaultAppsDisabled verifies that
// checks/normalization that should always run (e.g. disk image presence),
// regardless of the default-apps annotation, are not skipped by the
// early bailout for disabled nodes.
func TestStartupChecksAlwaysRunForNodeWithDefaultAppsDisabled(t *testing.T) {
	node := &v1.Node{
		AnnotationsF: map[string]any{defaultAppsAnnotation: false},
		GeneralF: &v1.General{
			HostnameF: "unmanaged-apps-no-drives",
		},
	}
	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "default-apps-disabled-no-drives"},
		Spec: &v1.ExperimentSpec{
			BaseDirF:  t.TempDir(),
			TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{node}},
		},
	}

	err := (Startup{}).PreStart(t.Context(), exp)
	if err == nil {
		t.Fatal("PreStart() did not return an error for disabled node missing drives")
	}
}
