package scorch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/scorch/scorchmd"
	"phenix/store/storetest"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
)

// Each stage of a SoH component writes its results to its own file, so a later
// stage of the same loop and count does not replace them.
//
//nolint:paralleltest // replaces common.PhenixBase
func TestSOHWritesTheResultsOfEachStage(t *testing.T) {
	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the experiment files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	exp := builtinExperiment("soh")
	exp.Status.SetAppStatus("soh", map[string]any{"initialized": true})

	storetest.UseBolt(t)
	builtinStore(t, exp)

	var s SOH
	_ = s.Init(Experiment(*exp), Name("health"), Type("soh"), RunID(2), CurrentLoop(1), LoopCount(3))

	for _, run := range []func(context.Context) error{s.Start, s.Stop} {
		if err := run(t.Context()); err != nil {
			t.Fatalf("SOH stage error = %v, want nil without failOnError", err)
		}
	}

	dir := filepath.Join(base, "images", "exp", "files", "scorch", "run-2", "health", "loop-1-count-3")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%s) error = %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if diff := cmp.Diff([]string{"soh-start.json", "soh-stop.json"}, names); diff != "" {
		t.Errorf("SOH stages results files mismatch (-want +got):\n%s", diff)
	}
}

// sohRunExperiment stores experiment exp, its SoH app initialized and set up
// by md, in a store and files directory of the test's own.
func sohRunExperiment(t *testing.T, md map[string]any) (*types.Experiment, string) {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the experiment files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	exp := builtinExperiment()
	exp.Spec.(*v1.ExperimentSpec).TopologyF = &v1.TopologySpec{}
	exp.Spec.Scenario().(*v2.ScenarioSpec).AppsF = []*v2.ScenarioApp{{NameF: "soh", MetadataF: md}}
	exp.Status.SetAppStatus("soh", map[string]any{"initialized": true})

	storetest.UseBolt(t)
	builtinStore(t, exp)

	return exp, filepath.Join(base, "images", "exp", "files", "scorch", "run-2", "health", "loop-1-count-3")
}

// A SoH component runs the checks its metadata names, with its C2 timeout,
// writes the results, and fails for failed checks only with failOnError.
//
//nolint:paralleltest // replaces common.PhenixBase
func TestSOHRunsTheChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		appMD    map[string]any // nil fails the checks
		meta     scorchmd.ComponentMetadata
		wantErr  bool
		wantFile bool
	}{
		"checks pass": {
			appMD:    map[string]any{"c2Timeout": "1m"},
			meta:     scorchmd.ComponentMetadata{"checks": []string{"files"}, "c2Timeout": "1s", "failOnError": true},
			wantFile: true,
		},
		"checks fail":                  {wantFile: true},
		"checks fail with failOnError": {meta: scorchmd.ComponentMetadata{"failOnError": true}, wantErr: true, wantFile: true},
	} {
		t.Run(name, func(t *testing.T) {
			exp, dir := sohRunExperiment(t, tc.appMD)

			var s SOH
			_ = s.Init(Experiment(*exp), Name("health"), Type("soh"), RunID(2), CurrentLoop(1), LoopCount(3), Metadata(tc.meta))

			if err := s.Start(t.Context()); (err != nil) != tc.wantErr {
				t.Errorf("SOH.Start(%s) error = %v, want error %t", name, err, tc.wantErr)
			}

			body, err := os.ReadFile(filepath.Join(dir, "soh-start.json"))
			if err != nil {
				t.Fatalf("reading the SoH results: %v", err)
			}

			var results map[string]any
			if err := json.Unmarshal(body, &results); err != nil || results["initialized"] != true {
				t.Errorf("SoH results = %s, %v; want the SoH app status", body, err)
			}
		})
	}
}

// A SoH component that cannot write its results file reports it in its output,
// and does not fail.
//
//nolint:paralleltest // replaces common.PhenixBase
func TestSOHResultsFileErrors(t *testing.T) {
	for name, block := range map[string]func(dir string) error{
		"no directory": func(dir string) error {
			if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
				return err
			}

			return os.WriteFile(dir, nil, 0o600)
		},
		"no file": func(dir string) error { return os.MkdirAll(filepath.Join(dir, "soh-start.json"), 0o750) },
	} {
		t.Run(name, func(t *testing.T) {
			exp, dir := sohRunExperiment(t, map[string]any{})

			if err := block(dir); err != nil {
				t.Fatalf("blocking the SoH results file: %v", err)
			}

			var s SOH
			_ = s.Init(Experiment(*exp), Name("health"), Type("soh"), RunID(2), CurrentLoop(1), LoopCount(3),
				Metadata(scorchmd.ComponentMetadata{"checks": []string{"files"}}))

			if err := s.Start(t.Context()); err != nil {
				t.Errorf("SOH.Start(%s) error = %v, want nil", name, err)
			}

			if info, err := os.Stat(filepath.Join(dir, "soh-start.json")); err == nil && info.Mode().IsRegular() {
				t.Errorf("SOH.Start(%s) wrote the results file", name)
			}
		})
	}
}
