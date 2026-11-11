package experiment

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/store/storetest"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
)

// noTopologySpec is an experiment spec without any topology, not even an empty one.
type noTopologySpec struct {
	*v1.ExperimentSpec
}

func (noTopologySpec) Topology() ifaces.TopologySpec {
	return nil
}

// An experiment without a topology saves no VM settings.
func TestRecordVMDefaultsWithoutATopology(t *testing.T) {
	t.Parallel()

	c := &store.Config{}
	recordVMDefaults(c, noTopologySpec{new(v1.ExperimentSpec)})

	if diff := cmp.Diff(store.Annotations{VMDefaultsAnnotation: "{}"}, c.Metadata.Annotations); diff != "" {
		t.Errorf("recordVMDefaults(no topology) annotations mismatch (-want +got):\n%s", diff)
	}
}

// The update hook gives the config the saved VM settings of c.Stored, the
// config the store holds, and drops given settings when c.Stored has none or
// is nil.
func TestUpdateHookKeepsTheStoredVMSettings(t *testing.T) { //nolint:paralleltest // replaces package globals
	useBridgeSettings(t, common.BridgeModeManual, false)

	saved := `{"vm":{"cpus":2}}`

	tests := []struct {
		name   string
		stored *store.Config
		given  store.Annotations
		want   store.Annotations
	}{
		{
			name:  "no stored config",
			given: store.Annotations{VMDefaultsAnnotation: `{"vm":{"cpus":64}}`, "keep": "x"},
			want:  store.Annotations{"keep": "x"},
		},
		{
			name:   "a stored config without saved settings",
			stored: &store.Config{},
			given:  store.Annotations{VMDefaultsAnnotation: `{"vm":{"cpus":64}}`},
			want:   store.Annotations{},
		},
		{
			name: "a stored config with saved settings",
			stored: &store.Config{
				Metadata: store.ConfigMetadata{Annotations: store.Annotations{VMDefaultsAnnotation: saved}},
			},
			want: store.Annotations{VMDefaultsAnnotation: saved},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			c := hookConfig("exp", map[string]any{"experimentName": "exp", "baseDir": t.TempDir()}, map[string]any{})
			c.Metadata.Annotations = tc.given
			c.Stored = tc.stored

			if err := experimentConfigHook("update", c); err != nil {
				t.Fatalf("experimentConfigHook(update) error = %v", err)
			}

			if diff := cmp.Diff(tc.want, c.Metadata.Annotations); diff != "" {
				t.Errorf("experimentConfigHook(update) annotations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
