package scorch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"phenix/api/scorch/scorchmd"
)

// Progress reports whether any harvester count grew since prev.
func TestFilebeatMetricsProgress(t *testing.T) {
	t.Parallel()

	prev := filebeatMetrics{Started: 2, Open: 2, Closed: 2, Running: 2}

	tests := map[string]struct {
		m    filebeatMetrics
		want bool
	}{
		"no change":      {m: prev},
		"fewer":          {m: filebeatMetrics{Started: 1, Open: 1, Closed: 1, Running: 1}},
		"more started":   {m: filebeatMetrics{Started: 3, Open: 2, Closed: 2, Running: 2}, want: true},
		"more open":      {m: filebeatMetrics{Started: 2, Open: 3, Closed: 2, Running: 2}, want: true},
		"more closed":    {m: filebeatMetrics{Started: 2, Open: 2, Closed: 3, Running: 2}, want: true},
		"more running":   {m: filebeatMetrics{Started: 2, Open: 2, Closed: 2, Running: 3}, want: true},
		"some more, one": {m: filebeatMetrics{Started: 0, Open: 0, Closed: 0, Running: 9}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.m.Progress(prev); got != tc.want {
				t.Errorf("%+v.Progress(%+v) = %t, want %t", tc.m, prev, got, tc.want)
			}
		})
	}
}

// Done reports whether Filebeat started harvesters and closed each one.
func TestFilebeatMetricsDone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		m    filebeatMetrics
		want bool
	}{
		"nothing started":  {m: filebeatMetrics{}},
		"not all closed":   {m: filebeatMetrics{Started: 2, Closed: 1}},
		"files still open": {m: filebeatMetrics{Started: 2, Closed: 2, Open: 1}},
		"still running":    {m: filebeatMetrics{Started: 2, Closed: 2, Running: 1}},
		"all closed":       {m: filebeatMetrics{Started: 2, Closed: 2}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.m.Done(); got != tc.want {
				t.Errorf("%+v.Done() = %t, want %t", tc.m, got, tc.want)
			}
		})
	}
}

// filebeatRunDir is the run directory of the mergeFilebeatConfig tests.
const filebeatRunDir = "/runs/run-0"

// filebeatWant is the config that mergeFilebeatConfig makes from an empty one
// for run 0 of experiment exp in filebeatRunDir, with the given run name and inputs.
func filebeatWant(start time.Time, runName string, inputs []any) map[string]any {
	fields := map[string]any{
		filebeatStartKey:    start.Format(time.RubyDate),
		"scorch.experiment": "exp",
		"scorch.run_id":     0,
		"scorch.runtime":    start.Format(time.RubyDate),
	}

	if runName != "" {
		fields["scorch.run_name"] = runName
	}

	return map[string]any{
		"fields_under_root": true,
		"fields":            fields,
		"processors": []map[string]any{
			{"timestamp": map[string]any{
				"field":        filebeatStartKey,
				"layouts":      []string{"Mon Jan 02 15:04:05 -0700 2006"},
				"target_field": "scorch.start_time",
			}},
			{"drop_fields": map[string]any{"fields": []string{filebeatStartKey}}},
		},
		"logging.level":    "warning",
		"logging.to_files": true,
		"logging.files": map[string]any{
			"path": filepath.Join(filebeatRunDir, "filebeat"), filebeatNameKey: "filebeat.log",
			"keepfiles": "7", "permission": "0644",
		},
		"filebeat.inputs":        inputs,
		"filebeat.registry.path": filepath.Join(filebeatRunDir, "filebeat", "registry"),
	}
}

// filebeatIndexWant are the index settings of a run that uses the experiment
// name as the index name.
func filebeatIndexWant() map[string]any {
	return map[string]any{
		"ilm.enabled": false, "template.name": "filebeat", "template.pattern": "experiment-*", "template.overwrite": false,
	}
}

// filebeatDissect is the dissect processor that mergeFilebeatConfig adds to
// an input of component cmp.
func filebeatDissect(cmp string) map[string]any {
	return map[string]any{"dissect": map[string]any{
		"tokenizer":     filepath.Join(filebeatRunDir, cmp) + "/%{scorch.itername}/%{scorch.output_file}",
		"field":         "log.file.path",
		"target_prefix": "",
	}}
}

// mergeFilebeatConfig adds the SCORCH settings and the Filebeat inputs of the
// components that the run, or a loop in it, uses. It points each input path
// at the component's run directory and keeps the settings the config gives.
func TestMergeFilebeatConfig(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	components := func() []scorchmd.ComponentSpec {
		return []scorchmd.ComponentSpec{
			{Name: "cap", Metadata: scorchmd.ComponentMetadata{"filebeat.inputs": []any{
				map[string]any{
					"type":       "log",
					"paths":      []any{"out/a.json", 7},
					"processors": []any{map[string]any{"drop_event": "x"}},
				},
				map[string]any{"type": "log", "paths": []any{}},
				map[string]any{"type": "udp"},
			}}},
			{Name: "nested", Metadata: scorchmd.ComponentMetadata{"filebeat.inputs": []any{
				map[string]any{"paths": []any{"/x/b.log"}, "close_inactive": "1m"},
			}}},
			{Name: "unused", Metadata: scorchmd.ComponentMetadata{"filebeat.inputs": []any{
				map[string]any{"paths": []any{"c.log"}},
			}}},
			{Name: "quiet"},
		}
	}

	componentInputs := []any{
		map[string]any{
			"type":           "log",
			"paths":          []any{filebeatRunDir + "/cap/loop-*-count-*/a.json", 7},
			"processors":     []any{map[string]any{"drop_event": "x"}, filebeatDissect("cap")},
			"close_inactive": "5s",
			"scan_frequency": "5s",
		},
		map[string]any{"type": "log", "paths": []any{}},
		map[string]any{"type": "udp"},
		map[string]any{
			"paths":          []any{filebeatRunDir + "/nested/loop-*-count-*/b.log"},
			"processors":     []any{filebeatDissect("nested")},
			"close_inactive": "1m",
			"scan_frequency": "5s",
		},
	}

	run := func(name string) []*scorchmd.Loop {
		return []*scorchmd.Loop{{
			Name: name, Start: []string{"cap", "quiet"}, Loop: &scorchmd.Loop{Stop: []string{"nested"}},
		}}
	}

	tests := map[string]struct {
		md     scorchmd.ScorchMetadata
		config map[string]any
		want   func() map[string]any
	}{
		"experiment index": {
			md: scorchmd.ScorchMetadata{
				Filebeat:   scorchmd.FilebeatSpec{Enabled: true, ExpAsIndex: true},
				Runs:       run("attack"),
				Components: components(),
			},
			config: map[string]any{
				"output.elasticsearch": map[string]any{"hosts": []any{"es:9200"}, "index": "old"},
				"setup":                map[string]any{"ilm.enabled": true, "other": 1},
				"logging.level":        "debug",
				"filebeat.inputs":      []any{map[string]any{"type": "stdin"}},
			},
			want: func() map[string]any {
				want := filebeatWant(start, "attack", append([]any{map[string]any{"type": "stdin"}}, componentInputs...))
				want["output.elasticsearch"] = map[string]any{"hosts": []any{"es:9200"}, "index": "experiment-exp"}
				setup := filebeatIndexWant()
				setup["other"] = 1
				want["setup"] = setup
				want["logging.level"] = "debug"

				return want
			},
		},
		"experiment index without settings": {
			md: scorchmd.ScorchMetadata{
				Filebeat: scorchmd.FilebeatSpec{Enabled: true, ExpAsIndex: true},
				Runs:     []*scorchmd.Loop{{}},
			},
			config: map[string]any{},
			want: func() map[string]any {
				want := filebeatWant(start, "", nil)
				want["output.elasticsearch"] = map[string]any{"index": "experiment-exp"}
				want["setup"] = filebeatIndexWant()

				return want
			},
		},
		"own index": {
			md:     scorchmd.ScorchMetadata{Runs: run(""), Components: components()},
			config: map[string]any{},
			want:   func() map[string]any { return filebeatWant(start, "", componentInputs) },
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantInputs := tc.want()["filebeat.inputs"].([]any) //nolint:forcetypeassert // built above

			got := mergeFilebeatConfig(tc.config, tc.md, "exp", 0, filebeatRunDir, start)
			if got != len(wantInputs) {
				t.Errorf("mergeFilebeatConfig() = %d inputs, want %d", got, len(wantInputs))
			}

			if diff := cmp.Diff(tc.want(), tc.config); diff != "" {
				t.Errorf("mergeFilebeatConfig() config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A run that uses the experiment name as the index name gets new index
// settings when the config gives empty or non-map ones.
func TestMergeFilebeatConfigReplacesEmptyIndexSettings(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	md := scorchmd.ScorchMetadata{
		Filebeat: scorchmd.FilebeatSpec{Enabled: true, ExpAsIndex: true},
		Runs:     []*scorchmd.Loop{{}},
	}

	for name, value := range map[string]any{"empty": nil, "nil map": map[string]any(nil), "not a map": "none"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := map[string]any{"output.elasticsearch": value, "setup": value}
			mergeFilebeatConfig(config, md, "exp", 0, filebeatRunDir, start)

			want := filebeatWant(start, "", nil)
			want["output.elasticsearch"] = map[string]any{"index": "experiment-exp"}
			want["setup"] = filebeatIndexWant()

			if diff := cmp.Diff(want, config); diff != "" {
				t.Errorf("mergeFilebeatConfig(%v settings) mismatch (-want +got):\n%s", value, diff)
			}
		})
	}
}

// writeFilebeatConfig writes the config as YAML and makes the registry
// directory; it replaces an old Filebeat directory.
func TestWriteFilebeatConfig(t *testing.T) {
	t.Parallel()

	runDir := t.TempDir()

	old := filepath.Join(runDir, "filebeat", "old.log")
	if err := os.MkdirAll(filepath.Dir(old), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(old, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	config := map[string]any{"logging.level": "info", "filebeat.inputs": []any{map[string]any{"type": "log"}}}

	if err := writeFilebeatConfig(config, runDir); err != nil {
		t.Fatalf("writeFilebeatConfig() error = %v", err)
	}

	body, err := os.ReadFile(filepath.Join(runDir, "filebeat", "filebeat.yml"))
	if err != nil {
		t.Fatalf("reading filebeat.yml: %v", err)
	}

	var got map[string]any
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding filebeat.yml: %v", err)
	}

	if diff := cmp.Diff(config, got); diff != "" {
		t.Errorf("filebeat.yml mismatch (-want +got):\n%s", diff)
	}

	if info, err := os.Stat(filepath.Join(runDir, "filebeat", "registry")); err != nil || !info.IsDir() {
		t.Errorf("registry directory = %v, %v; want a directory", info, err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old Filebeat file stat error = %v, want it removed", err)
	}
}

// filebeatUnmarshalable fails to marshal as YAML.
type filebeatUnmarshalable struct{}

func (filebeatUnmarshalable) MarshalYAML() (any, error) { return nil, errors.New("cannot marshal") }

// writeFilebeatConfig fails when the run directory cannot hold the config.
func TestWriteFilebeatConfigErrors(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeFilebeatConfig(map[string]any{}, file); err == nil {
		t.Errorf("writeFilebeatConfig(run directory under a file) = nil, want an error")
	}

	if err := writeFilebeatConfig(map[string]any{"bad": filebeatUnmarshalable{}}, t.TempDir()); err == nil {
		t.Errorf("writeFilebeatConfig(a value that fails to marshal) = nil, want an error")
	}
}
