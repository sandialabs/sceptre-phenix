package version

import (
	"errors"
	"reflect"
	"testing"

	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

func TestGetVersionedSpecForKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind, version string
		want          any
	}{
		{kind: "Topology", version: "v1", want: new(v1.TopologySpec)},
		{kind: "Scenario", version: "v2", want: new(v2.ScenarioSpec)},
		{kind: "Experiment", version: "v1", want: new(v1.ExperimentSpec)},
		{kind: "Node", version: "v1", want: new(v1.Node)},
		{kind: "Ruleset", version: "v1", want: new(v1.Ruleset)},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()

			got, err := GetVersionedSpecForKind(tt.kind, tt.version)
			if err != nil {
				t.Fatalf("GetVersionedSpecForKind(%s, %s) error = %v", tt.kind, tt.version, err)
			}

			if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
				t.Errorf("GetVersionedSpecForKind(%s, %s) = %T, want %T", tt.kind, tt.version, got, tt.want)
			}

			if _, err := GetVersionedSpecForKind(tt.kind, "v9"); err == nil {
				t.Errorf("GetVersionedSpecForKind(%s, v9) error = nil, want an error", tt.kind)
			}

			stored, err := GetStoredSpecForKind(tt.kind)
			if err != nil || reflect.TypeOf(stored) != reflect.TypeOf(tt.want) {
				t.Errorf("GetStoredSpecForKind(%s) = %T, %v; want %T", tt.kind, stored, err, tt.want)
			}
		})
	}

	if got, err := GetVersionedSpecForKind("Unknown", "v1"); err == nil {
		t.Errorf("GetVersionedSpecForKind(Unknown, v1) = %T, want an error", got)
	}

	if got, err := GetStoredSpecForKind("Unknown"); err == nil {
		t.Errorf("GetStoredSpecForKind(Unknown) = %T, want an error", got)
	}
}

func TestGetVersionedStatusForKind(t *testing.T) {
	t.Parallel()

	got, err := GetVersionedStatusForKind("Experiment", "v1")
	if err != nil {
		t.Fatalf("GetVersionedStatusForKind(Experiment, v1) error = %v", err)
	}

	if _, ok := got.(*v1.ExperimentStatus); !ok {
		t.Errorf("GetVersionedStatusForKind(Experiment, v1) = %T, want *v1.ExperimentStatus", got)
	}

	for _, tt := range []struct{ kind, version string }{{"Experiment", "v2"}, {"Topology", "v1"}} {
		if got, err := GetVersionedStatusForKind(tt.kind, tt.version); err == nil {
			t.Errorf("GetVersionedStatusForKind(%s, %s) = %T, want an error", tt.kind, tt.version, got)
		}
	}
}

func TestGetVersionedSchemaForKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind, version string
	}{
		{kind: "Topology", version: "v1"},
		{kind: "topology", version: "v1"},
		{kind: "Experiment", version: "v1"},
		{kind: "Scenario", version: "v2"},
	}

	for _, tt := range tests {
		got, err := GetVersionedSchemaForKind(tt.kind, tt.version)
		if err != nil {
			t.Errorf("GetVersionedSchemaForKind(%s, %s) error = %v", tt.kind, tt.version, err)

			continue
		}

		if len(got) == 0 {
			t.Errorf("GetVersionedSchemaForKind(%s, %s) = an empty schema", tt.kind, tt.version)
		}
	}

	if _, err := GetVersionedSchemaForKind("Unknown", "v1"); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("GetVersionedSchemaForKind(Unknown, v1) error = %v, want %v", err, ErrInvalidKind)
	}

	if _, err := GetVersionedSchemaForKind("Topology", "v9"); err == nil {
		t.Error("GetVersionedSchemaForKind(Topology, v9) error = nil, want an error")
	}
}

func TestGetVersionedValidatorForKind(t *testing.T) {
	t.Parallel()

	got, err := GetVersionedValidatorForKind("Topology", "v1")
	if err != nil || got == nil {
		t.Fatalf("GetVersionedValidatorForKind(Topology, v1) = %v, %v; want a schema", got, err)
	}

	if _, err := GetVersionedValidatorForKind("Unknown", "v1"); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("GetVersionedValidatorForKind(Unknown, v1) error = %v, want %v", err, ErrInvalidKind)
	}

	if _, err := GetVersionedValidatorForKind("Topology", "v9"); err == nil {
		t.Error("GetVersionedValidatorForKind(Topology, v9) error = nil, want an error")
	}
}
