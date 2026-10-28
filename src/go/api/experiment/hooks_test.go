package experiment

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	"phenix/util/common"
)

// hookConfig is a config of experiment name, as a config hook gets it.
func hookConfig(name string, spec, status map[string]any) *store.Config {
	return &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name},
		Spec:     spec,
		Status:   status,
	}
}

// useBridgeSettings sets the bridge mode and the GRE mesh setting until the test ends.
func useBridgeSettings(t *testing.T, mode common.BridgingMode, gre bool) {
	t.Helper()

	originalMode, originalGRE := common.BridgeMode, common.UseGREMesh
	common.BridgeMode, common.UseGREMesh = mode, gre //nolint:reassign // test settings

	t.Cleanup(func() { common.BridgeMode, common.UseGREMesh = originalMode, originalGRE }) //nolint:reassign // restore
}

// storeBridgedExperiment stores experiment name, stopped, using default bridge bridge.
func storeBridgedExperiment(t *testing.T, name, bridge string) {
	t.Helper()

	err := store.Create(hookConfig(name, map[string]any{
		"experimentName": name, "baseDir": t.TempDir(), "defaultBridge": bridge,
	}, map[string]any{}))
	if err != nil {
		t.Fatalf("store.Create(experiment %s) error = %v", name, err)
	}
}

// hookedSpec is the part of an experiment spec the create and update hooks set.
type hookedSpec struct {
	Name, BaseDir, Bridge string
	GRE                   bool
}

func decodeHooked(t *testing.T, c *store.Config) hookedSpec {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig(%s) error = %v", c.Metadata.Name, err)
	}

	return hookedSpec{
		Name:    exp.Spec.ExperimentName(),
		BaseDir: exp.Spec.BaseDir(),
		Bridge:  exp.Spec.DefaultBridge(),
		GRE:     exp.Spec.UseGREMesh(),
	}
}

// The startup hook gives an experiment stored before default bridges existed the phenix bridge, and keeps a bridge
// an experiment has.
func TestStartupHookSetsAMissingDefaultBridge(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	tests := []struct {
		name   string
		bridge string
		want   string
	}{
		{name: "no bridge", want: defaultBridgeName},
		{name: "a bridge of its own", bridge: "br1", want: "br1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			spec := map[string]any{"experimentName": "exp", "baseDir": base}

			if tc.bridge != "" {
				spec["defaultBridge"] = tc.bridge
			}

			c := hookConfig("exp", spec, nil)

			if err := experimentConfigHook("startup", c); err != nil {
				t.Fatalf("experimentConfigHook(startup) error = %v", err)
			}

			want := hookedSpec{Name: "exp", BaseDir: base, Bridge: tc.want}
			if diff := cmp.Diff(want, decodeHooked(t, c)); diff != "" {
				t.Errorf("experimentConfigHook(startup) spec mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A config hook refuses a config it cannot decode, and leaves a config alone at a stage it does not handle.
func TestConfigHookDecodesAndIgnoresOtherStages(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	bad := hookConfig("exp", map[string]any{"topology": "not a topology"}, nil)
	if err := experimentConfigHook("create", bad); err == nil ||
		!strings.HasPrefix(err.Error(), "decoding experiment from config: ") {
		t.Errorf("experimentConfigHook(create, bad topology) error = %v, want a decoding error", err)
	}

	spec := map[string]any{"experimentName": "exp", "baseDir": "/elsewhere"}
	c := hookConfig("new", spec, map[string]any{"startTime": stopTestStart})

	if err := experimentConfigHook("rename", c); err != nil {
		t.Errorf("experimentConfigHook(rename) error = %v, want nil", err)
	}

	if diff := cmp.Diff(map[string]any{"experimentName": "exp", "baseDir": "/elsewhere"}, c.Spec); diff != "" {
		t.Errorf("experimentConfigHook(rename) spec mismatch (-want +got):\n%s", diff)
	}
}

// The create and update hooks set the bridge auto bridge mode wants and the GRE mesh phenix is set to use, and refuse
// a bridge name too long or another stopped experiment already uses.
func TestCreateAndUpdateHooksCheckTheBridge(t *testing.T) { //nolint:paralleltest // replaces package globals
	long := strings.Repeat("x", MaxBridgeNameLength+1)

	tests := []struct {
		name    string
		mode    common.BridgingMode
		gre     bool
		expName string
		bridge  string // the experiment's default bridge, "" for none
		other   string // the default bridge of another stored experiment, "" for none
		want    string // the bridge set, "" when refused
		wantErr string
	}{
		{name: "the bridge of the config", expName: "exp", bridge: "br1", want: "br1"},
		{name: "the phenix bridge by default", expName: "exp", want: defaultBridgeName},
		{name: "the experiment name in auto mode", mode: common.BridgeModeAuto, expName: "exp", bridge: "br1", want: "exp"},
		{name: "the GRE mesh phenix is set to use", gre: true, expName: "exp", bridge: "br1", want: "br1"},
		{
			name: "an experiment name too long in auto mode", mode: common.BridgeModeAuto, expName: long,
			wantErr: "experiment name must be 15 characters or less when using auto bridge mode",
		},
		{
			name: "a bridge name too long", expName: "exp", bridge: long,
			wantErr: "default bridge name must be 15 characters or less",
		},
		{
			name: "a bridge another experiment uses", expName: "exp", bridge: "br1", other: "br1",
			wantErr: "experiment other already using default bridge br1",
		},
		{name: "the phenix bridge another experiment uses", expName: "exp", other: defaultBridgeName, want: defaultBridgeName},
	}

	for _, stage := range []string{"create", "update"} {
		for _, tc := range tests {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				storetest.Use(t)
				useBridgeSettings(t, tc.mode, tc.gre)

				if tc.other != "" {
					storeBridgedExperiment(t, "other", tc.other)
				}

				base := t.TempDir()
				spec := map[string]any{"experimentName": tc.expName, "baseDir": base}

				if tc.bridge != "" {
					spec["defaultBridge"] = tc.bridge
				}

				c := hookConfig(tc.expName, spec, map[string]any{})

				err := experimentConfigHook(stage, c)

				var gotErr string
				if err != nil {
					gotErr = err.Error()
				}

				if gotErr != tc.wantErr {
					t.Fatalf("experimentConfigHook(%s) error = %q, want %q", stage, gotErr, tc.wantErr)
				}

				if tc.wantErr != "" {
					return
				}

				want := hookedSpec{Name: tc.expName, BaseDir: base, Bridge: tc.want, GRE: tc.gre}
				if diff := cmp.Diff(want, decodeHooked(t, c)); diff != "" {
					t.Errorf("experimentConfigHook(%s) spec mismatch (-want +got):\n%s", stage, diff)
				}
			})
		}
	}
}

// A stored experiment config that does not decode does not stop the create and update hooks from checking the default
// bridge of the other stored experiments, and does not refuse the experiment alone.
func TestBridgeCheckSkipsAConfigThatDoesNotDecode(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		other   string // the default bridge of another stored experiment, "" for none
		wantErr string
	}{
		{name: "a bridge another experiment uses", other: "br1", wantErr: "experiment other already using default bridge br1"},
		{name: "no other experiment"},
	}

	for _, stage := range []string{"create", "update"} {
		for _, tc := range tests {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				storetest.Use(t)
				useBridgeSettings(t, common.BridgeModeManual, false)

				broken := hookConfig("broken", map[string]any{"experimentName": "broken"}, map[string]any{})
				broken.Version = "phenix.sandia.gov/v9"

				if err := store.Create(broken); err != nil {
					t.Fatalf("store.Create(experiment broken) error = %v", err)
				}

				if tc.other != "" {
					storeBridgedExperiment(t, "other", tc.other)
				}

				c := hookConfig("exp", map[string]any{
					"experimentName": "exp", "baseDir": t.TempDir(), "defaultBridge": "br1",
				}, map[string]any{})

				var gotErr string
				if err := experimentConfigHook(stage, c); err != nil {
					gotErr = err.Error()
				}

				if gotErr != tc.wantErr {
					t.Errorf("experimentConfigHook(%s) error = %q, want %q", stage, gotErr, tc.wantErr)
				}
			})
		}
	}
}

// The create and update hooks refuse the experiment when the store cannot list the experiments for the bridge check.
func TestBridgeCheckRefusesWhenTheStoreCannotList(t *testing.T) { //nolint:paralleltest // replaces package globals
	errList := errors.New("list failed")

	for _, stage := range []string{"create", "update"} {
		t.Run(stage, func(t *testing.T) {
			st := store.NewMockStore(gomock.NewController(t))
			storetest.Use(t, st)
			useBridgeSettings(t, common.BridgeModeManual, false)

			st.EXPECT().List("Experiment").Return(nil, errList)

			c := hookConfig("exp", map[string]any{
				"experimentName": "exp", "baseDir": t.TempDir(), "defaultBridge": "br1",
			}, map[string]any{})

			if err := experimentConfigHook(stage, c); !errors.Is(err, errList) {
				t.Errorf("experimentConfigHook(%s) error = %v, want one that wraps %v", stage, err, errList)
			}
		})
	}
}

// The update hook refuses to change a running experiment.
func TestUpdateHookRefusesARunningExperiment(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	c := hookConfig("exp", map[string]any{"experimentName": "exp"}, map[string]any{"startTime": stopTestStart})

	if err := experimentConfigHook("update", c); err == nil || err.Error() != "cannot update running experiment" {
		t.Errorf("experimentConfigHook(update, running) error = %v, want %q", err, "cannot update running experiment")
	}
}

// When an update renames an experiment, the update hook sets the new name and replaces the old name in the path of
// the base directory, only where a part of the path is the old name.
func TestUpdateHookRenamesTheBaseDirectory(t *testing.T) { //nolint:paralleltest // replaces the package store
	tests := []struct {
		name, oldName, newName, baseDir, want string
	}{
		{name: "the last part", oldName: "exp", newName: "lab", baseDir: "/phenix/experiments/exp", want: "/phenix/experiments/lab"},
		{name: "a middle part", oldName: "exp", newName: "lab", baseDir: "/data/exp/run", want: "/data/lab/run"},
		{name: "every part", oldName: "exp", newName: "lab", baseDir: "/exp/exp", want: "/lab/lab"},
		{name: "a name in other parts", oldName: "e", newName: "b", baseDir: "/phenix/experiments/e", want: "/phenix/experiments/b"},
		{name: "a path without the name", oldName: "exp", newName: "lab", baseDir: "/data/runs", want: "/data/runs"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			c := hookConfig(tc.newName, map[string]any{"experimentName": tc.oldName, "baseDir": tc.baseDir}, map[string]any{})

			if err := experimentConfigHook("update", c); err != nil {
				t.Fatalf("experimentConfigHook(update, %s to %s) error = %v", tc.oldName, tc.newName, err)
			}

			want := hookedSpec{Name: tc.newName, BaseDir: tc.want, Bridge: defaultBridgeName}
			if diff := cmp.Diff(want, decodeHooked(t, c)); diff != "" {
				t.Errorf("experimentConfigHook(update, %s to %s) spec mismatch (-want +got):\n%s", tc.oldName, tc.newName, diff)
			}
		})
	}
}

// An update that renames an experiment with a bridge of its own does not find its own stored config using that bridge.
func TestRenameKeepsTheBridgeOfTheExperiment(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	base := t.TempDir() + "/old"
	if err := store.Create(hookConfig("old", map[string]any{
		"experimentName": "old", "baseDir": base, "defaultBridge": "br1", "deployMode": "all",
		"topology": map[string]any{"nodes": []any{launchNode("vm1", "a.qc2", nil)}},
	}, map[string]any{})); err != nil {
		t.Fatalf("store.Create(experiment old) error = %v", err)
	}

	c, err := config.Get("experiment/old")
	if err != nil {
		t.Fatalf("config.Get(experiment/old) error = %v", err)
	}

	c.Metadata.Name = "new"

	if err := config.Update("experiment/old", c); err != nil {
		t.Fatalf("config.Update(experiment/old renamed new) error = %v", err)
	}

	got, err := config.Get("experiment/new")
	if err != nil {
		t.Fatalf("config.Get(experiment/new) error = %v", err)
	}

	want := hookedSpec{Name: "new", BaseDir: strings.TrimSuffix(base, "/old") + "/new", Bridge: "br1"}
	if diff := cmp.Diff(want, decodeHooked(t, got)); diff != "" {
		t.Errorf("experiment new after the rename mismatch (-want +got):\n%s", diff)
	}

	if _, err := config.Get("experiment/old"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(experiment/old) after the rename error = %v, want store.ErrNotExist", err)
	}
}

// The delete hook deletes the experiment's C2 responses, snapshots and base directory, and reports a failure to
// delete the files after it deletes the base directory.
func TestDeleteHookDeletesTheExperimentFiles(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		err     error
		wantErr []string
	}{
		{name: "files deleted"},
		{
			name: "files that cannot be deleted",
			err:  errors.New("mesh unreachable"),
			wantErr: []string{
				"deleting experiment snapshots and CC responses: deleting CC responses for experiment exp: mesh unreachable",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			useStartMM(t, &startMM{})

			files := useDeletedFiles(t)
			files.err = tc.err

			base := t.TempDir() + "/exp"
			if err := os.MkdirAll(base+"/mm_files", 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", base, err)
			}

			c := hookConfig("exp", map[string]any{
				"experimentName": "exp", "baseDir": base,
				"topology": map[string]any{"nodes": []any{map[string]any{"general": map[string]any{"hostname": "a"}}}},
			}, nil)

			err := experimentConfigHook("delete", c)

			var gotErr []string
			if err != nil {
				gotErr = errorTexts(err)
			}

			if diff := cmp.Diff(tc.wantErr, gotErr, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("experimentConfigHook(delete) errors mismatch (-want +got):\n%s", diff)
			}

			wantDeleted := []string{"exp/miniccc_responses", "head_exp_a_snapshot"}
			if tc.err != nil {
				wantDeleted = wantDeleted[:1]
			}

			if diff := cmp.Diff(wantDeleted, files.files()); diff != "" {
				t.Errorf("experimentConfigHook(delete) deleted files mismatch (-want +got):\n%s", diff)
			}

			if _, err := os.Stat(base); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("os.Stat(base directory) after experimentConfigHook(delete) error = %v, want it gone", err)
			}
		})
	}
}

// The create hook refuses the reserved experiment names in any letter case, as
// Create does: start and trigger take all for every experiment, minimega and
// __phenix__ are minimega namespaces, and . and .. name folders.
func TestCreateHookRefusesTheReservedName(t *testing.T) { //nolint:paralleltest // replaces the package store
	storetest.Use(t)

	for _, name := range []string{"all", "ALL", "All", "minimega", "MiniMega", "__phenix__", "__PHENIX__", ".", ".."} {
		c := hookConfig(name, map[string]any{"experimentName": name, "baseDir": t.TempDir()}, nil)

		if err := experimentConfigHook("create", c); !errors.Is(err, ErrReservedName) {
			t.Errorf("experimentConfigHook(create, %s) error = %v, want %v", name, err, ErrReservedName)
		}
	}

	c := hookConfig("allowed", map[string]any{"experimentName": "allowed", "baseDir": t.TempDir()}, nil)
	if err := experimentConfigHook("create", c); err != nil {
		t.Errorf("experimentConfigHook(create, allowed) error = %v, want nil", err)
	}
}
