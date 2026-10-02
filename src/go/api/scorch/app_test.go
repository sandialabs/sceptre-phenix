package scorch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/api/scorch/scorchexe"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	"phenix/util/mm/mmtest"
)

// recorder records which lifecycle stages it was called for.
type recorder struct {
	calls *[]Action
}

func (recorder) Init(...Option) error { return nil }
func (recorder) Type() string         { return "test-recorder" }

func (r recorder) Configure(context.Context) error {
	*r.calls = append(*r.calls, ActionConfigure)

	return nil
}

func (r recorder) Start(context.Context) error {
	*r.calls = append(*r.calls, ActionStart)

	return nil
}

func (r recorder) Stop(context.Context) error {
	*r.calls = append(*r.calls, ActionStop)

	return nil
}

func (r recorder) Cleanup(context.Context) error {
	*r.calls = append(*r.calls, ActionCleanup)

	return nil
}

// useMinimega points mmcli at a fake minimega answering `version` (failing
// while busy is set), with minimega's version not yet known, and returns the
// commands it received.
func useMinimega(t *testing.T, busy *atomic.Bool) func() []mmtest.Command {
	t.Helper()

	forgetVersion := func() {
		mmVersionCache.mu.Lock()
		mmVersionCache.version = ""
		mmVersionCache.mu.Unlock()
	}

	forgetVersion()
	t.Cleanup(forgetVersion)

	return mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "version" && busy != nil && busy.Load() {
			return []*minicli.Response{{Host: "head", Error: "minimega busy"}}
		}

		return []*minicli.Response{mmtest.Text("head", "minimega 2.9")}
	})
}

// scorchExperiment is an experiment, with its files under a temporary
// PhenixBase, whose one Scorch run uses a recorder component at every stage.
func scorchExperiment(t *testing.T) (*types.Experiment, *[]Action) {
	t.Helper()

	original := common.PhenixBase
	common.PhenixBase = t.TempDir() //nolint:reassign // experiment files go to a temporary tree

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	calls := new([]Action)

	components["test-recorder"] = recorder{calls: calls}
	t.Cleanup(func() { delete(components, "test-recorder") })

	stage := []any{"rec"}

	exp := &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "scorch-test"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "scorch-test",
			ScenarioF: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{
				NameF: "scorch",
				MetadataF: map[string]any{
					"components": []any{map[string]any{"name": "rec", "type": "test-recorder"}},
					"runs": []any{map[string]any{
						"configure": stage, "start": stage, "stop": stage, "cleanup": stage,
					}},
				},
			}}},
		},
	}

	return exp, calls
}

func TestRunningRunsEveryStageOrOnlyCleanup(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name        string
		cleanupOnly bool
		want        []Action
	}{
		{"a whole run", false, []Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup}},
		{"cleanup only", true, []Action{ActionCleanup}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			received := useMinimega(t, nil)
			exp, calls := scorchExperiment(t)

			runDir := filepath.Join(exp.FilesDir(), "scorch", "run-0")
			earlier := filepath.Join(runDir, "earlier.log")

			if err := os.MkdirAll(runDir, 0o750); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(earlier, []byte("collected"), 0o600); err != nil {
				t.Fatal(err)
			}

			ctx := scorchexe.SetRunID(context.Background(), 0)
			if tc.cleanupOnly {
				ctx = scorchexe.SetCleanupOnly(ctx)
			}

			if err := newScorch().Running(ctx, exp); err != nil {
				t.Fatalf("Running: %v", err)
			}

			if !slices.Equal(*calls, tc.want) {
				t.Fatalf("stages run = %v, want %v", *calls, tc.want)
			}

			infos, _ := filepath.Glob(filepath.Join(runDir, "scorch-test-scorch-run-0-*.json"))
			archives, _ := filepath.Glob(filepath.Join(exp.FilesDir(), "scorch-run-0_*.tgz"))
			_, statErr := os.Stat(earlier)

			if tc.cleanupOnly {
				// the data an earlier run collected is left as it is
				if statErr != nil || len(infos) != 0 || len(archives) != 0 || len(received()) != 0 {
					t.Fatalf("earlier data: %v; recorded %v and archived %v, asking minimega %q; want nothing done",
						statErr, infos, archives, mmtest.Bases(received()))
				}

				return
			}

			// a whole run starts afresh, then records and archives what it did
			if statErr == nil {
				t.Error("kept the data of an earlier run")
			}

			if len(infos) != 1 || len(archives) != 1 {
				t.Fatalf("recorded %v and archived %v, want one of each", infos, archives)
			}

			if info, err := os.ReadFile(infos[0]); err != nil || !strings.Contains(string(info), `"minimega 2.9"`) {
				t.Errorf("run info %s (%v) does not record minimega's version", info, err)
			}
		})
	}
}

func TestMinimegaVersionAskedUntilItAnswers(t *testing.T) { //nolint:paralleltest // replaces package state
	var busy atomic.Bool

	busy.Store(true)

	received := useMinimega(t, &busy)

	if _, err := minimegaVersion(); err == nil {
		t.Fatal("first call: want the error minimega gave")
	}

	busy.Store(false)

	for range 3 {
		v, err := minimegaVersion()
		if err != nil || v != "minimega 2.9" {
			t.Fatalf("got %q, %v; want the version", v, err)
		}
	}

	if n := mmtest.Count(received(), "version"); n != 2 {
		t.Errorf("minimega asked %d times, want 2 (one failure, then cached)", n)
	}
}
