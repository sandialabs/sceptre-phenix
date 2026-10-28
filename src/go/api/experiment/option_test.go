package experiment

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/common"
)

func TestStopOptions(t *testing.T) {
	t.Run("deletes injection snapshots by default", func(t *testing.T) {
		opts := newStopOptions()

		if opts.keepInjectionSnapshots {
			t.Fatal("expected injection snapshots not to be kept")
		}
	})

	t.Run("keeps injection snapshots when requested", func(t *testing.T) {
		opts := newStopOptions(StopWithKeepInjectionSnapshots(true))

		if !opts.keepInjectionSnapshots {
			t.Fatal("expected injection snapshots to be kept")
		}
	})
}

// newCreateOptions takes the deploy mode and GRE mesh from the global settings,
// puts the experiment under PhenixBase and on the phenix bridge by default, and
// keeps the GRE mesh on when it is on globally.
func TestCreateOptions(t *testing.T) { //nolint:paralleltest // replaces the global settings
	tests := []struct {
		name       string
		globalMode common.DeploymentMode
		globalGRE  bool
		opts       []CreateOption
		want       createOptions
	}{
		{
			name:       "defaults",
			globalMode: common.DeployModeAll,
			globalGRE:  true,
			opts:       []CreateOption{CreateWithName("exp")},
			want: createOptions{
				name: "exp", baseDir: "/base/experiments/exp", deployMode: common.DeployModeAll, useGREMesh: true, defaultBridge: "phenix",
			},
		},
		{
			name:       "options win over the defaults",
			globalMode: common.DeployModeAll,
			opts: []CreateOption{
				CreateWithName("exp"),
				CreateWithBaseDirectory("/elsewhere"),
				CreateWithDeployMode(common.DeployModeOnlyHeadnode),
				CreateWithDefaultBridge("br0"),
				CreateWithGREMesh(true),
			},
			want: createOptions{
				name: "exp", baseDir: "/elsewhere", deployMode: common.DeployModeOnlyHeadnode, useGREMesh: true, defaultBridge: "br0",
			},
		},
		{
			name:       "the GRE mesh stays on when it is on globally",
			globalMode: common.DeployModeNoHeadnode,
			globalGRE:  true,
			opts:       []CreateOption{CreateWithName("exp"), CreateWithGREMesh(false)},
			want: createOptions{
				name: "exp", baseDir: "/base/experiments/exp", deployMode: common.DeployModeNoHeadnode, useGREMesh: true,
				defaultBridge: "phenix",
			},
		},
		{
			name:       "the GRE mesh stays off when it is off globally and in the options",
			globalMode: common.DeployModeNoHeadnode,
			opts:       []CreateOption{CreateWithName("exp"), CreateWithGREMesh(false)},
			want: createOptions{
				name: "exp", baseDir: "/base/experiments/exp", deployMode: common.DeployModeNoHeadnode, defaultBridge: "phenix",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, mode, gre := common.PhenixBase, common.DeployMode, common.UseGREMesh
			common.PhenixBase, common.DeployMode, common.UseGREMesh = "/base", tc.globalMode, tc.globalGRE //nolint:reassign // test settings

			t.Cleanup(func() { common.PhenixBase, common.DeployMode, common.UseGREMesh = base, mode, gre }) //nolint:reassign // restore

			got := newCreateOptions(tc.opts...)
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(createOptions{})); diff != "" {
				t.Errorf("newCreateOptions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
