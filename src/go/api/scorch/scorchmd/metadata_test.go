package scorchmd

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/store"
	"phenix/types"
)

// metadataExperiment is an experiment whose scenario has the given apps.
func metadataExperiment(t *testing.T, apps ...map[string]any) *types.Experiment {
	t.Helper()

	exp, err := types.DecodeExperimentFromConfig(store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"topology":       map[string]any{"nodes": []any{}},
			"scenario":       map[string]any{"apps": apps},
		},
	})
	if err != nil {
		t.Fatalf("types.DecodeExperimentFromConfig() error = %v", err)
	}

	return exp
}

// DecodeMetadata decodes the SCORCH app metadata, indexes the components by
// name, and gives each run and nested loop without a count a count of 1.
func TestDecodeMetadata(t *testing.T) {
	t.Parallel()

	exp := metadataExperiment(t,
		map[string]any{"name": "soh", "metadata": map[string]any{"runs": "not scorch"}},
		map[string]any{"name": "scorch", "metadata": map[string]any{
			"filebeat": map[string]any{"enabled": true, "expNameAsIndexName": true},
			"components": []any{
				map[string]any{"name": "a", "type": "pause", "metadata": map[string]any{"duration": "1s"}},
				map[string]any{"name": "b", "type": "break", "background": true},
			},
			"runs": []any{
				map[string]any{"name": "first", "start": []any{"a"}, "loop": map[string]any{
					"count": 3, "start": []any{"b"}, "loop": map[string]any{"stop": []any{"a"}},
				}},
				map[string]any{"count": 2, "cleanup": []any{"b"}},
			},
		}},
	)

	md, err := DecodeMetadata(exp)
	if err != nil {
		t.Fatalf("DecodeMetadata() error = %v", err)
	}

	a := ComponentSpec{Name: "a", Type: "pause", Metadata: ComponentMetadata{"duration": "1s"}}
	b := ComponentSpec{Name: "b", Type: "break", Background: true}

	want := ScorchMetadata{
		Filebeat:   FilebeatSpec{Enabled: true, ExpAsIndex: true},
		Components: []ComponentSpec{a, b},
		Runs: []*Loop{
			{Name: "first", Count: 1, Start: []string{"a"}, Loop: &Loop{
				Count: 3, Start: []string{"b"}, Loop: &Loop{Count: 1, Stop: []string{"a"}},
			}},
			{Count: 2, Cleanup: []string{"b"}},
		},
		components: ComponentSpecMap{"a": a, "b": b},
	}

	if diff := cmp.Diff(want, md, cmp.AllowUnexported(ScorchMetadata{}), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("DecodeMetadata() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(ComponentSpecMap{"a": a, "b": b}, md.ComponentSpecs(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("ComponentSpecs() mismatch (-want +got):\n%s", diff)
	}
}

// DecodeMetadata fails for an experiment without a SCORCH app, or with SCORCH
// metadata that does not decode.
func TestDecodeMetadataErrors(t *testing.T) {
	t.Parallel()

	_, err := DecodeMetadata(metadataExperiment(t, map[string]any{"name": "soh", "metadata": map[string]any{}}))
	if !errors.Is(err, ErrScorchNotConfigured) {
		t.Errorf("DecodeMetadata(no SCORCH app) error = %v, want %v", err, ErrScorchNotConfigured)
	}

	_, err = DecodeMetadata(metadataExperiment(t))
	if !errors.Is(err, ErrScorchNotConfigured) {
		t.Errorf("DecodeMetadata(no apps) error = %v, want %v", err, ErrScorchNotConfigured)
	}

	_, err = DecodeMetadata(metadataExperiment(t, map[string]any{"name": "scorch", "metadata": map[string]any{"runs": "all"}}))
	if err == nil || errors.Is(err, ErrScorchNotConfigured) {
		t.Errorf("DecodeMetadata(runs that do not decode) error = %v, want a decode error", err)
	}

	emptyRun := map[string]any{"name": "scorch", "metadata": map[string]any{"runs": []any{map[string]any{}, nil}}}

	_, err = DecodeMetadata(metadataExperiment(t, emptyRun))
	if !errors.Is(err, errEmptyRun) {
		t.Errorf("DecodeMetadata(an empty run) error = %v, want %v", err, errEmptyRun)
	}
}

// RunName names a configured run and gives no name for another.
func TestRunName(t *testing.T) {
	t.Parallel()

	md := ScorchMetadata{Runs: []*Loop{{Name: "first"}, {}}}

	for id, want := range map[int]string{0: "first", 1: "", 2: ""} {
		if got := md.RunName(id); got != want {
			t.Errorf("RunName(%d) = %q, want %q", id, got, want)
		}
	}
}

// A run's own Filebeat settings replace the experiment-wide ones.
func TestFilebeatSettings(t *testing.T) {
	t.Parallel()

	global := map[string]any{"logging.level": "info"}
	own := map[string]any{"logging.level": "debug"}

	tests := map[string]struct {
		global  FilebeatSpec
		run     *FilebeatSpec
		enabled bool
		index   bool
		config  map[string]any
	}{
		"off":                     {},
		"global":                  {global: FilebeatSpec{Enabled: true, Config: global}, enabled: true, config: global},
		"global experiment index": {global: FilebeatSpec{Enabled: true, ExpAsIndex: true}, enabled: true, index: true},
		"global index but off":    {global: FilebeatSpec{ExpAsIndex: true}},
		"run turns it off":        {global: FilebeatSpec{Enabled: true, ExpAsIndex: true, Config: global}, run: &FilebeatSpec{}},
		"run turns it on":         {run: &FilebeatSpec{Enabled: true, Config: own}, enabled: true, config: own},
		"run experiment index":    {run: &FilebeatSpec{Enabled: true, ExpAsIndex: true}, enabled: true, index: true},
		"run index but off":       {global: FilebeatSpec{Enabled: true}, run: &FilebeatSpec{ExpAsIndex: true}},
		"run replaces config": {
			global:  FilebeatSpec{Enabled: true, Config: global},
			run:     &FilebeatSpec{Enabled: true, Config: own},
			enabled: true,
			config:  own,
		},
		"run without config has none": {global: FilebeatSpec{Config: global}, run: &FilebeatSpec{Enabled: true}, enabled: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			md := ScorchMetadata{Filebeat: tc.global, Runs: []*Loop{{Filebeat: tc.run}}}

			if got := md.FilebeatEnabled(0); got != tc.enabled {
				t.Errorf("FilebeatEnabled(0) = %t, want %t", got, tc.enabled)
			}

			if got := md.UseExpNameAsIndexName(0); got != tc.index {
				t.Errorf("UseExpNameAsIndexName(0) = %t, want %t", got, tc.index)
			}

			if diff := cmp.Diff(tc.config, md.FilebeatConfig(0)); diff != "" {
				t.Errorf("FilebeatConfig(0) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ContainsComponent finds a component in any stage of a loop or a loop
// nested in it.
func TestContainsComponent(t *testing.T) {
	t.Parallel()

	loop := Loop{
		Configure: []string{"c"}, Start: []string{"s"}, Stop: []string{"p"}, Cleanup: []string{"u"},
		Loop: &Loop{Start: []string{"n"}, Loop: &Loop{Cleanup: []string{"deep"}}},
	}

	for name, want := range map[string]bool{"c": true, "s": true, "p": true, "u": true, "n": true, "deep": true, "x": false} {
		if got := loop.ContainsComponent(name); got != want {
			t.Errorf("ContainsComponent(%q) = %t, want %t", name, got, want)
		}
	}

	if (Loop{}).ContainsComponent("c") {
		t.Errorf("Loop{}.ContainsComponent(c) = true, want false")
	}
}
