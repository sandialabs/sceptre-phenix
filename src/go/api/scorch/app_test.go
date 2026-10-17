package scorch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

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

// useMinimega fakes minimega's version (failing while busy is set), forgets the cached version, and
// returns the commands received.
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
				t.Fatalf("creating the run directory: %v", err)
			}

			if err := os.WriteFile(earlier, []byte("collected"), 0o600); err != nil {
				t.Fatalf("writing an earlier run's data: %v", err)
			}

			ctx := scorchexe.SetRunID(context.Background(), 0)
			if tc.cleanupOnly {
				ctx = scorchexe.SetCleanupOnly(ctx)
			}

			if err := newScorch().Running(ctx, exp); err != nil {
				t.Fatalf("Running(run 0, cleanup only %t) error = %v", tc.cleanupOnly, err)
			}

			if diff := cmp.Diff(tc.want, *calls); diff != "" {
				t.Errorf("Running(run 0, cleanup only %t) stages mismatch (-want +got):\n%s", tc.cleanupOnly, diff)
			}

			infos, _ := filepath.Glob(filepath.Join(runDir, "scorch-test-scorch-run-0-*.json"))
			archives, _ := filepath.Glob(filepath.Join(exp.FilesDir(), "scorch-run-0_*.tgz"))
			_, statErr := os.Stat(earlier)

			if tc.cleanupOnly {
				// the data an earlier run collected is left as it is
				if statErr != nil {
					t.Errorf("os.Stat(earlier run's data) after a cleanup-only Running() error = %v, want it kept", statErr)
				}

				if len(infos) != 0 || len(archives) != 0 {
					t.Errorf("cleanup-only Running() wrote run info %q and archives %q, want none", infos, archives)
				}

				if bases := mmtest.Bases(received()); len(bases) != 0 {
					t.Errorf("cleanup-only Running() sent minimega %q, want nothing", bases)
				}

				return
			}

			// a whole run starts afresh, then records and archives what it did
			if statErr == nil {
				t.Error("os.Stat(earlier run's data) after a whole Running() error = nil, want it removed")
			}

			if len(infos) != 1 || len(archives) != 1 {
				t.Fatalf("whole Running() wrote run info %q and archives %q, want one of each", infos, archives)
			}

			info, err := os.ReadFile(infos[0])
			if err != nil {
				t.Fatalf("reading run info: %v", err)
			}

			if !strings.Contains(string(info), `"minimega 2.9"`) {
				t.Errorf("run info = %s, want it to record minimega's version %q", info, "minimega 2.9")
			}
		})
	}
}

func TestMinimegaVersionAskedUntilItAnswers(t *testing.T) { //nolint:paralleltest // replaces package state
	var busy atomic.Bool

	busy.Store(true)

	received := useMinimega(t, &busy)

	if v, err := minimegaVersion(); err == nil {
		t.Errorf("minimegaVersion() while minimega is busy = %q, nil; want the error minimega gave", v)
	}

	busy.Store(false)

	for i := range 3 {
		if v, err := minimegaVersion(); err != nil || v != "minimega 2.9" {
			t.Errorf("minimegaVersion() call %d after minimega answers = %q, %v; want %q, nil", i+1, v, err, "minimega 2.9")
		}
	}

	// one failure, then cached
	if n := mmtest.Count(received(), "version"); n != 2 {
		t.Errorf("minimega version commands received = %d, want 2", n)
	}
}
