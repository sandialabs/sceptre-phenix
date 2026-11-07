package scorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"gopkg.in/yaml.v3"

	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/util/shell"
	scorchweb "phenix/web/scorch"
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

	components["test-recorder"] = func() Component { return recorder{calls: calls} }
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

			if err := newScorch(scorchweb.UpdatePipeline).Running(ctx, exp); err != nil {
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

// failingRecorder is a recorder whose start stage fails.
type failingRecorder struct {
	recorder
}

func (r failingRecorder) Start(ctx context.Context) error {
	_ = r.recorder.Start(ctx)

	return errors.New("component failed")
}

// recordDoneUpdates returns a pipeline update function recording the done stage statuses a run reports, and the
// statuses it recorded.
func recordDoneUpdates() (func(scorchweb.ComponentUpdate) error, *[]string) {
	statuses := new([]string)

	return func(update scorchweb.ComponentUpdate) error {
		if update.Stage == string(ActionDone) {
			*statuses = append(*statuses, update.Status)
		}

		return nil
	}, statuses
}

func TestRunningReportsHowTheRunEnded(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name    string
		failing bool
		want    []string
	}{
		{"a run that worked", false, []string{statusRunning, statusSuccess}},
		{"a run whose component failed", true, []string{statusRunning, statusFailure}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useMinimega(t, nil)
			exp, calls := scorchExperiment(t)
			update, statuses := recordDoneUpdates()

			if tc.failing {
				components["test-recorder"] = func() Component { return failingRecorder{recorder{calls: calls}} }
			}

			err := newScorch(update).Running(scorchexe.SetRunID(context.Background(), 0), exp)
			if (err != nil) != tc.failing {
				t.Errorf("Running(failing %t) error = %v, want an error %t", tc.failing, err, tc.failing)
			}

			if diff := cmp.Diff(tc.want, *statuses); diff != "" {
				t.Errorf("Running(failing %t) done statuses mismatch (-want +got):\n%s", tc.failing, diff)
			}
		})
	}
}

// A run's new archive shows in the experiment's file listing at once, rather
// than once the cached listing expires.
func TestRunningArchiveRefreshesTheFileListing(t *testing.T) { //nolint:paralleltest // replaces package state
	received := useMinimega(t, nil)
	exp, _ := scorchExperiment(t)

	listings := func() int {
		t.Helper()

		if _, err := file.GetExperimentFiles(exp.Metadata.Name, ""); err != nil {
			t.Fatalf("GetExperimentFiles(%q) error = %v", exp.Metadata.Name, err)
		}

		return mmtest.Count(received(), "file list")
	}

	first := listings()
	if again := listings(); again != first {
		t.Fatalf("headnode file listings after listing twice = %d, want %d (the cached one)", again, first)
	}

	if err := newScorch(scorchweb.UpdatePipeline).Running(scorchexe.SetRunID(context.Background(), 0), exp); err != nil {
		t.Fatalf("Running(run 0) error = %v", err)
	}

	if got := listings(); got != first+1 {
		t.Errorf("headnode file listings after a run archived its data = %d, want %d", got, first+1)
	}
}

// appComponent records each stage it runs as "<name> <stage>", with
// " background" for a background run, and fails the stages in fail.
type appComponent struct {
	calls   []string
	fail    map[string]bool
	current Options
}

func (c *appComponent) Init(opts ...Option) error { c.current = NewOptions(opts...); return nil }
func (*appComponent) Type() string                { return "test-app" }

func (c *appComponent) run(stage Action) error {
	call := c.current.Name + " " + string(stage)
	if c.current.Background {
		call += " background"
	}

	c.calls = append(c.calls, call)

	if c.fail[c.current.Name+" "+string(stage)] {
		return errors.New(call + " failed")
	}

	return nil
}

func (c *appComponent) Configure(context.Context) error { return c.run(ActionConfigure) }
func (c *appComponent) Start(context.Context) error     { return c.run(ActionStart) }
func (c *appComponent) Stop(context.Context) error      { return c.run(ActionStop) }
func (c *appComponent) Cleanup(context.Context) error   { return c.run(ActionCleanup) }

// appUseComponent registers a new appComponent that fails the stages in fail
// as the test-app component type until the test ends.
func appUseComponent(t *testing.T, fail ...string) *appComponent {
	t.Helper()

	c := &appComponent{fail: map[string]bool{}}
	for _, f := range fail {
		c.fail[f] = true
	}

	components["test-app"] = func() Component { return c }
	t.Cleanup(func() { delete(components, "test-app") })

	return c
}

// executor runs the configure, start, loop, stop and cleanup stages in order,
// stops configure and start at the first failure but runs stop and cleanup
// whole, and runs cleanup after a configure or start failure.
func TestExecutor(t *testing.T) { //nolint:paralleltest // replaces package state
	specs := scorchmd.ComponentSpecMap{
		"a":  {Name: "a", Type: "test-app"},
		"b":  {Name: "b", Type: "test-app"},
		"bg": {Name: "bg", Type: "test-app", Background: true},
	}

	all := func(names ...string) *scorchmd.Loop {
		return &scorchmd.Loop{Count: 1, Configure: names, Start: names, Stop: names, Cleanup: names}
	}

	tests := []struct {
		name    string
		loop    *scorchmd.Loop
		fail    []string
		opts    []Option
		want    []string
		wantErr []string
	}{
		{
			name: "every stage", loop: all("a", "b"),
			want: []string{
				"a configure", "b configure", "a start", "b start", "a stop", "b stop", "a cleanup", "b cleanup",
			},
		},
		{name: "no stages", loop: &scorchmd.Loop{Count: 1}},
		{
			name: "a configure failure skips start and stop but not cleanup", loop: all("a", "b"),
			fail: []string{"a configure"}, want: []string{"a configure", "a cleanup", "b cleanup"},
			wantErr: []string{"configure component a"},
		},
		{
			name: "a cleanup failure after a configure failure is reported too", loop: all("a"),
			fail: []string{"a configure", "a cleanup"}, want: []string{"a configure", "a cleanup"},
			wantErr: []string{"configure component a", "cleanup component a"},
		},
		{
			name: "a start failure skips the rest of start but runs stop and cleanup", loop: all("a", "b"),
			fail: []string{"a start", "a stop", "a cleanup"},
			want: []string{
				"a configure", "b configure", "a start", "a stop", "b stop", "a cleanup", "b cleanup",
			},
			wantErr: []string{"start component a", "stop component a", "cleanup component a"},
		},
		{
			name: "stop and cleanup failures do not stop their stage", loop: all("a", "b"),
			fail: []string{"a stop", "a cleanup"},
			want: []string{
				"a configure", "b configure", "a start", "b start", "a stop", "b stop", "a cleanup", "b cleanup",
			},
			wantErr: []string{"stop component a", "cleanup component a"},
		},
		{
			name: "a background component runs in the background only at configure and start", loop: all("bg"),
			want: []string{"bg configure background", "bg start background", "bg stop", "bg cleanup"},
		},
		{
			name: "a nested loop runs its count of times, before stop",
			loop: &scorchmd.Loop{Count: 1, Start: []string{"a"}, Stop: []string{"a"}, Loop: &scorchmd.Loop{
				Count: 2, Start: []string{"b"},
			}},
			want: []string{"a start", "b start", "b start", "a stop"},
		},
		{
			name: "a nested loop stops at its first failing count",
			loop: &scorchmd.Loop{Count: 1, Stop: []string{"a"}, Loop: &scorchmd.Loop{Count: 3, Start: []string{"b"}}},
			fail: []string{"b start"}, want: []string{"b start", "a stop"},
			wantErr: []string{"start component b"},
		},
		{
			name: "cleanup only", loop: all("a"), opts: []Option{CleanupOnly()},
			want: []string{"a cleanup"},
		},
		{
			name:    "a replacement that does not resolve",
			loop:    &scorchmd.Loop{Count: 1, Start: []string{"a"}, Replace: map[string]any{"k": []any{}}},
			wantErr: []string{"resolving replacements"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exp, _ := scorchExperiment(t)
			c := appUseComponent(t, tc.fail...)

			opts := append([]Option{Experiment(*exp), RunID(0)}, tc.opts...)
			err := executor(context.Background(), specs, tc.loop, opts...)

			if diff := cmp.Diff(tc.want, c.calls); diff != "" {
				t.Errorf("executor() stages mismatch (-want +got):\n%s", diff)
			}

			if (err != nil) != (len(tc.wantErr) > 0) {
				t.Fatalf("executor() error = %v, want errors %q", err, tc.wantErr)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("executor() error = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// The scorch app registers itself, and its hooks other than Configure and
// Running do nothing.
func TestScorchApp(t *testing.T) { //nolint:paralleltest // sets PATH
	t.Setenv("PATH", t.TempDir())

	exp, _ := scorchExperiment(t)
	ctx := context.Background()

	// the name the experiment's scenario gives the SCORCH app
	want := exp.Apps()[0].Name()

	registered := app.GetApp(want)

	got, ok := registered.(*Scorch)
	if !ok {
		t.Fatalf("GetApp(%s) = %T, want *Scorch", want, registered)
	}

	if got.updatePipeline == nil {
		t.Errorf("GetApp(%s) has no pipeline update function", want)
	}

	if name := got.Name(); name != want {
		t.Errorf("Name() = %q, want %q", name, want)
	}

	for name, hook := range map[string]func() error{
		"Init":      func() error { return got.Init() },
		"PreStart":  func() error { return got.PreStart(ctx, exp) },
		"PostStart": func() error { return got.PostStart(ctx, exp) },
		"Cleanup":   func() error { return got.Cleanup(ctx, exp) },
	} {
		if err := hook(); err != nil {
			t.Errorf("%s() error = %v, want nil", name, err)
		}
	}
}

// Configure gives each component without a type its name as its type, and
// refuses metadata that does not decode.
func TestScorchConfigure(t *testing.T) { //nolint:paralleltest // replaces package state
	exp, _ := scorchExperiment(t)
	other := &v2.ScenarioApp{NameF: "other", MetadataF: map[string]any{"components": []any{"x"}}}

	spec, _ := exp.Spec.(*v1.ExperimentSpec)
	spec.ScenarioF.AppsF = append([]*v2.ScenarioApp{other}, spec.ScenarioF.AppsF...)

	scorchApp := spec.ScenarioF.AppsF[1]
	scorchApp.SetMetadata(map[string]any{
		"components": []any{map[string]any{"name": "a"}, map[string]any{"name": "b", "type": "tap"}},
		"runs":       []any{map[string]any{"start": []any{"a", "b"}}},
	})

	if err := (Scorch{}).Configure(context.Background(), exp); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	md, err := scorchmd.DecodeMetadata(exp)
	if err != nil {
		t.Fatalf("DecodeMetadata() after Configure() error = %v", err)
	}

	want := []scorchmd.ComponentSpec{{Name: "a", Type: "a"}, {Name: "b", Type: "tap"}}
	if diff := cmp.Diff(want, md.Components); diff != "" {
		t.Errorf("components after Configure() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"components": []any{"x"}}, other.Metadata()); diff != "" {
		t.Errorf("metadata of another app after Configure() mismatch (-want +got):\n%s", diff)
	}

	scorchApp.SetMetadata(map[string]any{"components": "none"})

	if err := (Scorch{}).Configure(context.Background(), exp); err == nil || !strings.HasPrefix(err.Error(), "decoding app metadata") {
		t.Errorf("Configure() of metadata that does not decode error = %v, want a decoding error", err)
	}
}

// appNoWrite makes dir read-only until the test ends, skipping the test for
// root, which writes there anyway.
func appNoWrite(t *testing.T, dir string) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
}

// Running fails for an experiment without SCORCH or for a run it does not
// have, and reports a failed run when it cannot set up, record or archive it.
func TestRunningErrors(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name   string
		runID  int
		setup  func(t *testing.T, exp *types.Experiment, busy *atomic.Bool)
		want   string
		failed bool // the done stage reports a failure
	}{
		{
			name: "no scorch app", setup: func(_ *testing.T, exp *types.Experiment, _ *atomic.Bool) {
				spec, _ := exp.Spec.(*v1.ExperimentSpec)
				spec.ScenarioF.AppsF = nil
			},
			want: scorchmd.ErrScorchNotConfigured.Error(),
		},
		{name: "a run past the last", runID: 1, want: "invalid Scorch run ID for experiment scorch-test"},
		{name: "a negative run", runID: -1, want: "invalid Scorch run ID for experiment scorch-test"},
		{
			name: "a run directory that cannot be removed", setup: func(t *testing.T, exp *types.Experiment, _ *atomic.Bool) {
				t.Helper()
				dir := filepath.Join(exp.FilesDir(), "scorch")
				if err := os.MkdirAll(filepath.Join(dir, "run-0"), 0o750); err != nil {
					t.Fatalf("creating the run directory: %v", err)
				}
				appNoWrite(t, dir)
			},
			want: "removing existing contents of run directory",
		},
		{
			name: "a Filebeat config that cannot be written", setup: func(t *testing.T, exp *types.Experiment, _ *atomic.Bool) {
				t.Helper()
				appFilebeat(exp, map[string]any{})
				appNoWrite(t, filepath.Join(exp.FilesDir(), "scorch"))
			},
			want: "creating Filebeat config",
		},
		{
			name: "minimega does not give its version", setup: func(_ *testing.T, _ *types.Experiment, busy *atomic.Bool) {
				busy.Store(true)
			},
			want: "getting minimega version", failed: true,
		},
		{
			name: "an archive that cannot be written", setup: func(t *testing.T, exp *types.Experiment, _ *atomic.Bool) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(exp.FilesDir(), "scorch"), 0o750); err != nil {
					t.Fatalf("creating the scorch directory: %v", err)
				}
				appNoWrite(t, exp.FilesDir())
			},
			want: "archiving data generated for run 0", failed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var busy atomic.Bool

			useMinimega(t, &busy)
			exp, _ := scorchExperiment(t)
			update, statuses := recordDoneUpdates()

			if tc.setup != nil {
				tc.setup(t, exp, &busy)
			}

			err := newScorch(update).Running(scorchexe.SetRunID(context.Background(), tc.runID), exp)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Running() error = %v, want one naming %q", err, tc.want)
			}

			var want []string
			if tc.failed {
				want = []string{statusRunning, statusFailure}
			}

			if diff := cmp.Diff(want, *statuses); diff != "" {
				t.Errorf("Running() done statuses mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// appFilebeat enables Filebeat with config, or with no config key for nil,
// for the experiment's SCORCH runs.
func appFilebeat(exp *types.Experiment, config map[string]any) {
	filebeat := map[string]any{"enabled": true}
	if config != nil {
		filebeat["config"] = config
	}

	for _, a := range exp.Apps() {
		if a.Name() == (Scorch{}).Name() {
			md := a.Metadata()
			md["filebeat"] = filebeat
			a.SetMetadata(md)
		}
	}
}

// A run with Filebeat on but no inputs to harvest writes the Filebeat config
// and runs without starting Filebeat.
func TestRunningWithFilebeatButNoInputs(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimega(t, nil)
	exp, calls := scorchExperiment(t)
	appFilebeat(exp, map[string]any{"logging.level": "info"})

	if err := newScorch(scorchweb.UpdatePipeline).Running(scorchexe.SetRunID(context.Background(), 0), exp); err != nil {
		t.Fatalf("Running() error = %v", err)
	}

	if diff := cmp.Diff([]Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup}, *calls); diff != "" {
		t.Errorf("Running() stages mismatch (-want +got):\n%s", diff)
	}

	cfg, err := os.ReadFile(filepath.Join(exp.FilesDir(), "scorch", "run-0", "filebeat", "filebeat.yml"))
	if err != nil || !strings.Contains(string(cfg), "logging.level: info") {
		t.Errorf("Filebeat config = %q, %v, want one with logging.level: info", cfg, err)
	}
}

// A run with Filebeat on and no Filebeat config, or an empty one, writes a
// config with only the Scorch settings and runs every stage.
func TestRunningWithFilebeatAndNoConfig(t *testing.T) { //nolint:paralleltest // replaces package state
	tests := []struct {
		name   string
		config map[string]any
	}{
		{"no config key", nil},
		{"empty config", map[string]any{}},
	}

	for _, tt := range tests { //nolint:paralleltest // replaces package state
		t.Run(tt.name, func(t *testing.T) {
			useMinimega(t, nil)
			exp, calls := scorchExperiment(t)
			appFilebeat(exp, tt.config)

			if err := newScorch(scorchweb.UpdatePipeline).Running(scorchexe.SetRunID(context.Background(), 0), exp); err != nil {
				t.Fatalf("Running() error = %v", err)
			}

			if diff := cmp.Diff([]Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup}, *calls); diff != "" {
				t.Errorf("Running() stages mismatch (-want +got):\n%s", diff)
			}

			body, err := os.ReadFile(filepath.Join(exp.FilesDir(), "scorch", "run-0", "filebeat", "filebeat.yml"))
			if err != nil {
				t.Fatalf("reading the Filebeat config: %v", err)
			}

			var cfg map[string]any
			if err := yaml.Unmarshal(body, &cfg); err != nil {
				t.Fatalf("decoding the Filebeat config: %v", err)
			}

			got := map[string]any{
				"fields_under_root": cfg["fields_under_root"],
				"logging.level":     cfg["logging.level"],
				"logging.to_files":  cfg["logging.to_files"],
			}
			want := map[string]any{"fields_under_root": true, "logging.level": "warning", "logging.to_files": true}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Filebeat config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// recordInfo writes the run's experiment, run, times and versions to a JSON
// file in the run directory, and fails when it cannot.
func TestRecordInfo(t *testing.T) { //nolint:paralleltest // replaces package state
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	md := store.ConfigMetadata{Name: "exp", Annotations: map[string]string{"phenix.workflow/tags": "a,b"}}
	s := Scorch{md: scorchmd.ScorchMetadata{Runs: []*scorchmd.Loop{{Name: "first"}}}}

	t.Run("written", func(t *testing.T) {
		useMinimega(t, nil)

		runDir := filepath.Join(t.TempDir(), "run-0")
		if err := s.recordInfo(0, runDir, md, start); err != nil {
			t.Fatalf("recordInfo() error = %v", err)
		}

		body, err := os.ReadFile(filepath.Join(runDir, "exp-scorch-run-0-2026-01-02T03-04-05Z.json"))
		if err != nil {
			t.Fatalf("reading the run info: %v", err)
		}

		var info map[string]any
		if err := json.Unmarshal(body, &info); err != nil {
			t.Fatalf("decoding the run info: %v", err)
		}

		if _, ok := info["end"].(string); !ok {
			t.Errorf("run info end = %v, want a time", info["end"])
		}

		delete(info, "end")
		delete(info, "phenix_version")

		want := map[string]any{
			"experiment":       map[string]any{"name": "exp", "tags": "a,b"},
			"run":              map[string]any{"name": "first", "index": float64(0)},
			"start":            "2026-01-02T03:04:05Z",
			"minimega_version": "minimega 2.9",
		}
		if diff := cmp.Diff(want, info); diff != "" {
			t.Errorf("run info mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("a run directory under a file", func(t *testing.T) {
		useMinimega(t, nil)

		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatalf("writing %s: %v", blocker, err)
		}

		err := s.recordInfo(0, filepath.Join(blocker, "run-0"), md, start)
		if err == nil || !strings.HasPrefix(err.Error(), "creating ") {
			t.Errorf("recordInfo() error = %v, want a directory error", err)
		}
	})

	t.Run("a read-only run directory", func(t *testing.T) {
		useMinimega(t, nil)

		runDir := t.TempDir()
		appNoWrite(t, runDir)

		err := s.recordInfo(0, runDir, md, start)
		if err == nil || !strings.HasPrefix(err.Error(), "writing scorch information file") {
			t.Errorf("recordInfo() error = %v, want a write error", err)
		}
	})
}

// archiveRun archives nothing for a run that wrote nothing.
func TestArchiveRunWithoutData(t *testing.T) { //nolint:paralleltest // replaces package state
	exp, _ := scorchExperiment(t)

	if err := archiveRun(exp, 0, filepath.Join(exp.FilesDir(), "scorch", "run-0"), time.Now()); err != nil {
		t.Fatalf("archiveRun() error = %v", err)
	}

	if archives, _ := filepath.Glob(filepath.Join(exp.FilesDir(), "*.tgz")); len(archives) != 0 {
		t.Errorf("archiveRun() of a run without data wrote %q, want nothing", archives)
	}
}

// startFilebeat starts nothing when Filebeat is off or not installed, and
// stopFilebeat has nothing to stop then, so it checks no process.
func TestFilebeatNotStarted(t *testing.T) { //nolint:paralleltest // sets PATH
	t.Setenv("PATH", t.TempDir())

	for name, enabled := range map[string]bool{"Filebeat off": false, "Filebeat not installed": true} {
		t.Run(name, func(t *testing.T) {
			fake := appUseShell(t)
			s := Scorch{md: scorchmd.ScorchMetadata{Filebeat: scorchmd.FilebeatSpec{Enabled: enabled}}}

			cmd, port, err := s.startFilebeat(context.Background(), t.TempDir())
			if cmd != nil || port != 0 || err != nil {
				t.Errorf("startFilebeat() = %v, %d, %v, want nil, 0, nil", cmd, port, err)
			}

			polltest.Within(t, "stopFilebeat(nil)", func() { s.stopFilebeat(context.Background(), cmd, port) })

			if fake.asked != 0 {
				t.Errorf("ProcessExists asked %d times, want 0", fake.asked)
			}
		})
	}
}

// appShell is the default shell with ProcessExists answering from exists in
// order, and false after the last answer.
type appShell struct {
	shell.Shell

	exists []bool
	asked  int
}

func (s *appShell) ProcessExists(int) bool {
	s.asked++

	if s.asked > len(s.exists) {
		return false
	}

	return s.exists[s.asked-1]
}

// appUseShell replaces the default shell with an appShell for the test.
func appUseShell(t *testing.T, exists ...bool) *appShell {
	t.Helper()

	original := shell.DefaultShell
	fake := &appShell{Shell: original, exists: exists}
	shell.DefaultShell = fake //nolint:reassign // the test's shell

	t.Cleanup(func() { shell.DefaultShell = original }) //nolint:reassign // restore

	return fake
}

// appFakeFilebeat puts a filebeat script first on PATH. The script records its
// arguments, one per line, in the returned file and then runs body. It moves
// the file into place in one step, so a reader never sees a partial file.
func appFakeFilebeat(t *testing.T, shebang, body string) string {
	t.Helper()

	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := fmt.Sprintf(
		"%s\nprintf '%%s\\n' \"$@\" > '%s.tmp'\nmv '%s.tmp' '%s'\n%s\n",
		shebang, args, args, args, body,
	)

	if err := os.WriteFile(filepath.Join(dir, "filebeat"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake filebeat: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return args
}

// appFilebeatArgs returns the arguments the fake filebeat recorded in file. It
// waits for the file, because a busy machine can start the script late.
func appFilebeatArgs(t *testing.T, file string) []string {
	t.Helper()

	var (
		body []byte
		err  error
	)

	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if body, err = os.ReadFile(file); err == nil || time.Now().After(deadline) {
			break
		}
	}

	if err != nil {
		t.Fatalf("reading the fake filebeat arguments: %v", err)
	}

	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// appWantFilebeatArgs are the arguments startFilebeat gives Filebeat.
func appWantFilebeatArgs(runDir string, port int) []string {
	return []string{
		"-c", filepath.Join(runDir, "filebeat", "filebeat.yml"),
		"--path.data", filepath.Join(runDir, "filebeat", "data"),
		"--httpprof", fmt.Sprintf("127.0.0.1:%d", port),
	}
}

// startFilebeat starts Filebeat with the run's config and data directories
// and a free httpprof port, and returns the running process.
func TestFilebeatStarted(t *testing.T) { //nolint:paralleltest // sets PATH
	args := appFakeFilebeat(t, "#!/bin/sh", "exec sleep 30")
	s := Scorch{md: scorchmd.ScorchMetadata{Filebeat: scorchmd.FilebeatSpec{Enabled: true}}}
	runDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())

	cmd, port, err := s.startFilebeat(ctx, runDir)
	if err != nil || cmd == nil {
		cancel()
		t.Fatalf("startFilebeat() = %v, %d, %v, want a process", cmd, port, err)
	}

	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	if port <= 0 {
		t.Errorf("startFilebeat() port = %d, want a free port", port)
	}

	if diff := cmp.Diff(appWantFilebeatArgs(runDir, port), appFilebeatArgs(t, args)); diff != "" {
		t.Errorf("Filebeat arguments mismatch (-want +got):\n%s", diff)
	}
}

// startFilebeat returns the error when Filebeat does not start, or when it
// exits with an error before its startup delay ends.
func TestFilebeatStartErrors(t *testing.T) { //nolint:paralleltest // sets PATH
	s := Scorch{md: scorchmd.ScorchMetadata{Filebeat: scorchmd.FilebeatSpec{Enabled: true}}}

	t.Run("an interpreter that does not exist", func(t *testing.T) {
		appFakeFilebeat(t, "#!/nonexistent/sh", "")

		cmd, port, err := s.startFilebeat(context.Background(), t.TempDir())
		if cmd != nil || port != 0 || err == nil {
			t.Errorf("startFilebeat() = %v, %d, %v, want nil, 0 and an error", cmd, port, err)
		}
	})

	t.Run("an early exit", func(t *testing.T) {
		appFakeFilebeat(t, "#!/bin/sh", "exit 3")
		fake := appUseShell(t, false)

		cmd, port, err := s.startFilebeat(context.Background(), t.TempDir())

		var exit *exec.ExitError
		if cmd != nil || port != 0 || !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Errorf("startFilebeat() = %v, %d, %v, want nil, 0 and exit status 3", cmd, port, err)
		}

		if fake.asked != 1 {
			t.Errorf("ProcessExists asked %d times, want 1", fake.asked)
		}
	})
}

// stopFilebeat waits for a Filebeat that has already exited and returns
// without waiting for it to harvest.
func TestFilebeatStopAfterExit(t *testing.T) { //nolint:paralleltest // replaces the default shell
	appUseShell(t, false)

	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exit 3")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %v: %v", cmd.Args, err)
	}

	polltest.Within(t, "stopFilebeat() of an exited Filebeat", func() {
		Scorch{}.stopFilebeat(context.Background(), cmd, 1)
	})

	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3 {
		t.Errorf("stopFilebeat() left process state %v, want exit status 3", cmd.ProcessState)
	}
}

// A run with Filebeat inputs starts Filebeat before its stages and stops it
// after them.
func TestRunningWithFilebeatStarted(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimega(t, nil)
	exp, calls := scorchExperiment(t)
	appFilebeat(exp, map[string]any{"filebeat.inputs": []any{map[string]any{"type": "log"}}})
	args := appFakeFilebeat(t, "#!/bin/sh", "exit 0")
	fake := appUseShell(t, true, false)

	if err := newScorch(scorchweb.UpdatePipeline).Running(scorchexe.SetRunID(context.Background(), 0), exp); err != nil {
		t.Fatalf("Running() error = %v", err)
	}

	if diff := cmp.Diff([]Action{ActionConfigure, ActionStart, ActionStop, ActionCleanup}, *calls); diff != "" {
		t.Errorf("Running() stages mismatch (-want +got):\n%s", diff)
	}

	cfg := filepath.Join(exp.FilesDir(), "scorch", "run-0", "filebeat", "filebeat.yml")
	if got := appFilebeatArgs(t, args); len(got) != 6 || got[1] != cfg {
		t.Errorf("Filebeat arguments = %q, want the run's Filebeat config %s", got, cfg)
	}

	if fake.asked != 2 {
		t.Errorf("ProcessExists asked %d times, want 2 (start and stop)", fake.asked)
	}
}

// A run whose Filebeat does not start fails before any stage runs.
func TestRunningWithFilebeatStartError(t *testing.T) { //nolint:paralleltest // replaces package state
	useMinimega(t, nil)
	exp, calls := scorchExperiment(t)
	appFilebeat(exp, map[string]any{"filebeat.inputs": []any{map[string]any{"type": "log"}}})
	appFakeFilebeat(t, "#!/nonexistent/sh", "")

	err := newScorch(scorchweb.UpdatePipeline).Running(scorchexe.SetRunID(context.Background(), 0), exp)
	if err == nil || !strings.HasPrefix(err.Error(), "starting Filebeat: ") {
		t.Errorf("Running() error = %v, want a Filebeat start error", err)
	}

	if len(*calls) != 0 {
		t.Errorf("Running() ran stages %v, want none", *calls)
	}
}
