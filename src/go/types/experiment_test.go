package types

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	ifaces "phenix/types/interfaces"
	"phenix/util/common"
)

func TestExperimentRunningAndDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		noStatus    bool
		startTime   string
		wantRunning bool
		wantDryRun  bool
	}{
		{name: "no status", noStatus: true},
		{name: "stopped", startTime: ""},
		{name: "started", startTime: "2024-01-01T00:00:00Z", wantRunning: true},
		{name: "started as a dry run", startTime: "2024-01-01T00:00:00Z-DRYRUN", wantRunning: true, wantDryRun: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
			exp.Status.SetStartTime(test.startTime)

			if test.noStatus {
				exp.Status = nil
			}

			if got := exp.Running(); got != test.wantRunning {
				t.Errorf("Running() with start time %q = %t, want %t", test.startTime, got, test.wantRunning)
			}

			if got := exp.DryRun(); got != test.wantDryRun {
				t.Errorf("DryRun() with start time %q = %t, want %t", test.startTime, got, test.wantDryRun)
			}
		})
	}
}

// Start records an RFC 3339 time, with -DRYRUN appended for a dry run.
func TestExperimentDryRunMatchesTheStartTimeSuffix(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var (
			at     = time.Unix(rapid.Int64Range(0, 1<<33).Draw(t, "unix"), 0).UTC()
			dryrun = rapid.Bool().Draw(t, "dryrun")
			start  = at.Format(time.RFC3339)
		)

		if dryrun {
			start += "-DRYRUN"
		}

		exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
		exp.Status.SetStartTime(start)

		if got := exp.DryRun(); got != dryrun {
			t.Fatalf("DryRun() with start time %q = %t, want %t", start, got, dryrun)
		}
	})
}

// The pending starts survive the trip through a stored config, VM names as given.
func TestExperimentPendingStartsRoundTrip(t *testing.T) {
	t.Parallel()

	vmName := rapid.StringMatching(`[A-Za-z][A-Za-z0-9_.-]{0,15}`)

	rapid.Check(t, func(t *rapid.T) {
		var (
			timers = rapid.MapOf(vmName, rapid.StringMatching(`20[0-9]{2}-0[1-9]-1[0-9]T0[0-9]:00:00Z`)).Draw(t, "timers")
			c2     = rapid.MapOf(vmName, rapid.MapOfN(vmName, rapid.Bool(), 1, 3)).Draw(t, "c2")
			post   = rapid.Bool().Draw(t, "postStart")
			reason = rapid.String().Draw(t, "interrupted")
		)

		exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
		exp.Status.SetStartTime("2024-01-01T00:00:00Z")
		exp.Status.SetDelayedStarts(timers, c2, post)
		exp.Status.SetInterrupted(reason)

		c := store.Config{
			Version:  "phenix.sandia.gov/v1",
			Kind:     "Experiment",
			Metadata: exp.Metadata,
			Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
			Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
		}

		got, err := DecodeExperimentFromConfig(c)
		if err != nil {
			t.Fatalf("DecodeExperimentFromConfig() error = %v", err)
		}

		if diff := cmp.Diff(timers, got.Status.DelayedTimers(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("DelayedTimers() after a round trip mismatch (-want +got):\n%s", diff)
		}

		if diff := cmp.Diff(c2, got.Status.DelayedC2(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("DelayedC2() after a round trip mismatch (-want +got):\n%s", diff)
		}

		if got.Status.PostStartPending() != post || got.Status.Interrupted() != reason {
			t.Errorf("PostStartPending(), Interrupted() after a round trip = %t, %q; want %t, %q",
				got.Status.PostStartPending(), got.Status.Interrupted(), post, reason)
		}
	})
}

// The start marker survives the trip through a stored config and its JSON, and a zero marker leaves no starting key,
// so a status stored before the marker existed decodes to the zero marker.
func TestExperimentStartMarkerRoundTrip(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var marker ifaces.StartMarker

		if rapid.Bool().Draw(t, "marked") {
			marker = ifaces.StartMarker{
				Since: time.Unix(rapid.Int64Range(0, 1<<33).Draw(t, "since"), 0).UTC().Format(time.RFC3339),
				PID:   rapid.IntRange(1, 1<<22).Draw(t, "pid"),
				Host:  rapid.StringMatching(`[a-z][a-z0-9.-]{0,20}`).Draw(t, "host"),
			}
		}

		exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
		exp.Status.SetStarting(marker)

		status := structs.MapDefaultCase(exp.Status, structs.CASESNAKE)
		if _, ok := status["starting"]; ok == marker.IsZero() {
			t.Fatalf("stored status %v has a starting key: %t, want %t", status, ok, !marker.IsZero())
		}

		data, err := json.Marshal(store.Config{
			Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: exp.Metadata,
			Spec: structs.MapDefaultCase(exp.Spec, structs.CASESNAKE), Status: status,
		})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		var c store.Config
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		got, err := DecodeExperimentFromConfig(c)
		if err != nil {
			t.Fatalf("DecodeExperimentFromConfig() error = %v", err)
		}

		if diff := cmp.Diff(marker, got.Status.Starting()); diff != "" {
			t.Fatalf("Starting() after a round trip mismatch (-want +got):\n%s", diff)
		}
	})
}

// experimentStoredConfig returns a stored v1 Experiment config named name,
// started at startTime when it is not empty.
func experimentStoredConfig(name, startTime string) *store.Config {
	status := map[string]any{}
	if startTime != "" {
		status["startTime"] = startTime
	}

	return &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: name, Annotations: store.Annotations{"stored": "yes"}},
		Spec: map[string]any{
			"experimentName": name,
			"baseDir":        "/stored/" + name,
			"scenario": map[string]any{
				"apps": []map[string]any{{"name": "ntp"}, {"name": "soh"}},
			},
		},
		Status: status,
	}
}

// experimentCreate puts c in the test's store.
func experimentCreate(t *testing.T, c *store.Config) {
	t.Helper()

	if err := store.Create(c); err != nil {
		t.Fatalf("store.Create(%s) error = %v", c.Metadata.Name, err)
	}
}

func TestExperimentReload(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	experimentCreate(t, experimentStoredConfig("exp", "2024-01-01T00:00:00Z"))

	exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
	if err := exp.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if got := exp.Spec.BaseDir(); got != "/stored/exp" {
		t.Errorf("Spec.BaseDir() after Reload() = %q, want %q", got, "/stored/exp")
	}

	if got := exp.Status.StartTime(); got != "2024-01-01T00:00:00Z" {
		t.Errorf("Status.StartTime() after Reload() = %q, want %q", got, "2024-01-01T00:00:00Z")
	}

	if got := exp.Metadata.Annotations["stored"]; got != "yes" {
		t.Errorf("Metadata.Annotations[stored] after Reload() = %q, want %q", got, "yes")
	}

	for _, test := range []struct {
		name, exp, want string
	}{
		{name: "invalid name", exp: "a/b", want: "getting experiment:"},
		{name: "not stored", exp: "missing", want: "getting experiment missing from store"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := NewExperiment(store.ConfigMetadata{Name: test.exp}).Reload()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("Reload() of %q error = %v, want one containing %q", test.exp, err, test.want)
			}
		})
	}
}

func TestExperimentReloadDecodeError(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	ctrl := gomock.NewController(t)
	st := store.NewMockStore(ctrl)
	storetest.Use(t, st)

	st.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		c.Version = "phenix.sandia.gov/v9"

		return nil
	})

	err := NewExperiment(store.ConfigMetadata{Name: "exp"}).Reload()
	if err == nil || !strings.Contains(err.Error(), "decoding experiment exp") {
		t.Errorf("Reload() of an unknown version error = %v, want a decoding error", err)
	}
}

func TestExperimentWriteToStore(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	for _, statusOnly := range []bool{false, true} {
		t.Run("statusOnly "+strconv.FormatBool(statusOnly), func(t *testing.T) {
			storetest.UseBolt(t)
			experimentCreate(t, experimentStoredConfig("exp", ""))

			exp := NewExperiment(store.ConfigMetadata{
				Name:        "exp",
				Annotations: store.Annotations{"written": "yes"},
			})
			exp.Spec.SetBaseDir("/written")
			exp.Status.SetStartTime("2025-01-01T00:00:00Z")

			if err := exp.WriteToStore(statusOnly); err != nil {
				t.Fatalf("WriteToStore(%t) error = %v", statusOnly, err)
			}

			got := NewExperiment(store.ConfigMetadata{Name: "exp"})
			if err := got.Reload(); err != nil {
				t.Fatalf("Reload() after WriteToStore(%t) error = %v", statusOnly, err)
			}

			wantBaseDir := "/written"
			if statusOnly {
				wantBaseDir = "/stored/exp"
			}

			if diff := cmp.Diff(store.Annotations{"written": "yes"}, got.Metadata.Annotations); diff != "" {
				t.Errorf("annotations after WriteToStore(%t) mismatch (-want +got):\n%s", statusOnly, diff)
			}

			if got.Spec.BaseDir() != wantBaseDir || got.Status.StartTime() != "2025-01-01T00:00:00Z" {
				t.Errorf("BaseDir(), StartTime() after WriteToStore(%t) = %q, %q; want %q, %q",
					statusOnly, got.Spec.BaseDir(), got.Status.StartTime(), wantBaseDir, "2025-01-01T00:00:00Z")
			}
		})
	}
}

func TestExperimentWriteToStoreErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	errUpdate := errors.New("update failed")

	for _, test := range []struct {
		name      string
		getErr    error
		updateErr error
		wantErr   error
		want      string
	}{
		{name: "not stored", getErr: store.ErrNotExist, wantErr: store.ErrNotExist, want: "getting experiment exp from store"},
		{name: "update fails", updateErr: errUpdate, wantErr: errUpdate, want: "saving experiment config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := store.NewMockStore(ctrl)
			storetest.Use(t, st)

			st.EXPECT().Get(gomock.Any()).Return(test.getErr)

			if test.getErr == nil {
				st.EXPECT().Update(gomock.Any()).Return(test.updateErr)
			}

			err := NewExperiment(store.ConfigMetadata{Name: "exp"}).WriteToStore(false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("WriteToStore(false) error = %v, want one containing %q", err, test.want)
			}

			if !errors.Is(err, test.wantErr) {
				t.Errorf("WriteToStore(false) error = %v, want it to wrap %v", err, test.wantErr)
			}
		})
	}
}

func TestExperiments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	experimentCreate(t, experimentStoredConfig("running", "2024-01-01T00:00:00Z"))
	experimentCreate(t, experimentStoredConfig("stopped", ""))

	for _, test := range []struct {
		running bool
		want    []string
	}{
		{running: false, want: []string{"running", "stopped"}},
		{running: true, want: []string{"running"}},
	} {
		t.Run("running "+strconv.FormatBool(test.running), func(t *testing.T) {
			exps, err := Experiments(test.running)
			if err != nil {
				t.Fatalf("Experiments(%t) error = %v", test.running, err)
			}

			names := make([]string, 0, len(exps))
			for _, exp := range exps {
				names = append(names, exp.Metadata.Name)
			}

			if diff := cmp.Diff(test.want, names, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("Experiments(%t) names mismatch (-want +got):\n%s", test.running, diff)
			}
		})
	}
}

func TestExperimentsErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	errList := errors.New("list failed")

	bad := *experimentStoredConfig("bad", "")
	bad.Version = "phenix.sandia.gov/v9"

	for _, test := range []struct {
		name    string
		configs store.Configs
		listErr error
		want    string
	}{
		{name: "list fails", listErr: errList, want: "getting list of experiment configs"},
		{name: "unknown version", configs: store.Configs{bad}, want: "decoding experiment bad from config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := store.NewMockStore(ctrl)
			storetest.Use(t, st)

			st.EXPECT().List("Experiment").Return(test.configs, test.listErr)

			exps, err := Experiments(false)
			if err == nil || !strings.Contains(err.Error(), test.want) || exps != nil {
				t.Errorf("Experiments(false) = %v, %v; want nil and an error containing %q", exps, err, test.want)
			}
		})
	}
}

// DecodableExperiments returns each stored experiment that decodes and an error for each stored config that does not.
func TestDecodableExperiments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	experimentCreate(t, experimentStoredConfig("running", "2024-01-01T00:00:00Z"))
	experimentCreate(t, experimentStoredConfig("stopped", ""))

	for _, name := range []string{"bad1", "bad2"} {
		bad := experimentStoredConfig(name, "")
		bad.Version = "phenix.sandia.gov/v9"
		experimentCreate(t, bad)
	}

	for _, test := range []struct {
		running bool
		want    []string
	}{
		{running: false, want: []string{"running", "stopped"}},
		{running: true, want: []string{"running"}},
	} {
		t.Run("running "+strconv.FormatBool(test.running), func(t *testing.T) {
			exps, failed, err := DecodableExperiments(test.running)
			if err != nil {
				t.Fatalf("DecodableExperiments(%t) error = %v", test.running, err)
			}

			names := make([]string, 0, len(exps))
			for _, exp := range exps {
				names = append(names, exp.Metadata.Name)
			}

			failedNames := make([]string, 0, len(failed))
			for _, f := range failed {
				failedNames = append(failedNames, f.Name)
			}

			sorted := cmpopts.SortSlices(func(a, b string) bool { return a < b })

			if diff := cmp.Diff(test.want, names, sorted); diff != "" {
				t.Errorf("DecodableExperiments(%t) names mismatch (-want +got):\n%s", test.running, diff)
			}

			if diff := cmp.Diff([]string{"bad1", "bad2"}, failedNames, sorted); diff != "" {
				t.Errorf("DecodableExperiments(%t) failed names mismatch (-want +got):\n%s", test.running, diff)
			}
		})
	}

	_, err := Experiments(false)

	var decodeErr *ExperimentDecodeError
	if !errors.As(err, &decodeErr) || !strings.HasPrefix(err.Error(), "decoding experiment bad") {
		t.Errorf("Experiments(false) error = %v, want an *ExperimentDecodeError for a bad config", err)
	}
}

func TestDecodableExperimentsListError(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	errList := errors.New("list failed")

	ctrl := gomock.NewController(t)
	st := store.NewMockStore(ctrl)
	storetest.Use(t, st)

	st.EXPECT().List("Experiment").Return(nil, errList)

	exps, failed, err := DecodableExperiments(false)
	if !errors.Is(err, errList) || exps != nil || failed != nil {
		t.Errorf("DecodableExperiments(false) = %v, %v, %v; want nil, nil and an error that wraps %v", exps, failed, err, errList)
	}
}

func TestDecodeExperimentFromConfigErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config func(*store.Config)
		want   string
	}{
		{
			name:   "unknown version",
			config: func(c *store.Config) { c.Version = "phenix.sandia.gov/v9" },
			want:   "getting versioned spec for config",
		},
		{
			name:   "spec of the wrong type",
			config: func(c *store.Config) { c.Spec = map[string]any{"baseDir": []int{1}} },
			want:   "decoding versioned spec for experiment exp",
		},
		{
			name: "spec of another kind",
			config: func(c *store.Config) {
				c.Kind = "Topology"
				c.Spec = map[string]any{}
			},
			want: "invalid spec in config",
		},
		{
			name:   "status of the wrong type",
			config: func(c *store.Config) { c.Status = map[string]any{"startTime": []int{1}} },
			want:   "decoding versioned status",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			c := experimentStoredConfig("exp", "")
			test.config(c)

			exp, err := DecodeExperimentFromConfig(*c)
			if err == nil || !strings.Contains(err.Error(), test.want) || exp != nil {
				t.Errorf("DecodeExperimentFromConfig() = %v, %v; want nil and an error containing %q", exp, err, test.want)
			}
		})
	}
}

// experimentNoScenarioSpec is an experiment spec with no scenario at all.
type experimentNoScenarioSpec struct {
	ifaces.ExperimentSpec
}

func (experimentNoScenarioSpec) Scenario() ifaces.ScenarioSpec { return nil }

func TestExperimentApps(t *testing.T) {
	t.Parallel()

	withApps, err := DecodeExperimentFromConfig(*experimentStoredConfig("exp", ""))
	if err != nil {
		t.Fatalf("DecodeExperimentFromConfig() error = %v", err)
	}

	noApps := NewExperiment(store.ConfigMetadata{Name: "exp"})

	noScenario := NewExperiment(store.ConfigMetadata{Name: "exp"})
	noScenario.SetSpec(experimentNoScenarioSpec{noScenario.Spec})

	tests := []struct {
		name  string
		exp   *Experiment
		apps  []string
		found string // the app App("soh") finds
	}{
		{name: "scenario with apps", exp: withApps, apps: []string{"ntp", "soh"}, found: "soh"},
		{name: "empty scenario", exp: noApps},
		{name: "no scenario", exp: noScenario},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var names []string
			for _, app := range test.exp.Apps() {
				names = append(names, app.Name())
			}

			if diff := cmp.Diff(test.apps, names); diff != "" {
				t.Errorf("Apps() names mismatch (-want +got):\n%s", diff)
			}

			var found string
			if app := test.exp.App("soh"); app != nil {
				found = app.Name()
			}

			if found != test.found {
				t.Errorf("App(soh) name = %q, want %q", found, test.found)
			}

			if app := test.exp.App("missing"); app != nil {
				t.Errorf("App(missing) = %v, want nil", app)
			}
		})
	}
}

func TestExperimentSetSpec(t *testing.T) {
	t.Parallel()

	exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
	other := NewExperiment(store.ConfigMetadata{Name: "other"})

	exp.SetSpec(other.Spec)

	if exp.Spec != other.Spec {
		t.Errorf("Spec after SetSpec(other) = %p, want %p", exp.Spec, other.Spec)
	}
}

func TestExperimentFilesDir(t *testing.T) {
	t.Parallel()

	exp := NewExperiment(store.ConfigMetadata{Name: "exp"})

	if got, want := exp.FilesDir(), filepath.Join(common.PhenixBase, "images", "exp", "files"); got != want {
		t.Errorf("FilesDir() = %q, want %q", got, want)
	}
}
