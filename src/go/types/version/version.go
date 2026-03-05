package version

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	v0 "phenix/types/version/v0"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

var ErrInvalidKind = errors.New("invalid kind")

// StoredVersion tracks the latest stored version of each config kind.
var StoredVersion = map[string]string{ //nolint:gochecknoglobals // global registry
	"Topology":   "v1",
	"Scenario":   "v2",
	"Experiment": "v1",
	"Image":      "v1",
	"User":       "v1",
	"Role":       "v1",
	"Node":       "v1",
	"Ruleset":    "v1",
}

const LATEST_VERSION = "v2" //nolint:staticcheck // constant name is part of API

// GetStoredSpecForKind looks up the current stored version for the given kind
// and returns the versioned spec. Internally it calls `GetVersionedSpecForKind`.
func GetStoredSpecForKind(kind string) (any, error) {
	version, ok := StoredVersion[kind]
	if !ok {
		return nil, fmt.Errorf("unknown kind %s", kind)
	}

	return GetVersionedSpecForKind(kind, version)
}

// GetVersionedSpecForKind returns an initialized spec for the given kind and
// version.
func GetVersionedSpecForKind(kind, version string) (any, error) {
	switch kind {
	case "Topology":
		switch version {
		case "v0":
			return new(v0.TopologySpec), nil
		case "v1":
			return new(v1.TopologySpec), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	case "Scenario":
		switch version {
		case "v1":
			return new(v1.ScenarioSpec), nil
		case "v2":
			return new(v2.ScenarioSpec), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	case "Experiment":
		switch version {
		case "v1":
			return new(v1.ExperimentSpec), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	case "Node":
		switch version {
		case "v1":
			return new(v1.Node), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	case "Ruleset":
		switch version {
		case "v1":
			return new(v1.Ruleset), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	default:
		return nil, fmt.Errorf("unknown kind %s", kind)
	}
}

// GetVersionedStatusForKind returns an initialized status for the given kind
// and version.
func GetVersionedStatusForKind(kind, version string) (any, error) {
	switch kind {
	case "Experiment":
		switch version {
		case "v1":
			return new(v1.ExperimentStatus), nil
		default:
			return nil, fmt.Errorf("unknown version %s for %s", version, kind)
		}
	default:
		return nil, fmt.Errorf("unknown kind %s", kind)
	}
}

// GetVersionedSchemaForKind returns a generic map (map[string]interface{}) of
// the schema for the given kind and version.
func GetVersionedSchemaForKind(kind, version string) (map[string]any, error) {
	var api struct {
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}

	kind = strings.ToUpper(kind[:1]) + kind[1:]

	switch version {
	case "v1":
		err := yaml.Unmarshal(v1.OpenAPI, &api)
		if err != nil {
			return nil, fmt.Errorf("parsing v1 OpenAPI schema: %w", err)
		}
	case "v2":
		err := yaml.Unmarshal(v2.OpenAPI, &api)
		if err != nil {
			return nil, fmt.Errorf("parsing v2 OpenAPI schema: %w", err)
		}
	}

	schema, ok := api.Components.Schemas[kind]
	if !ok {
		return nil, fmt.Errorf("a schema for version %s of %s is not defined", version, kind)
	}

	return schema, nil
}

// GetVersionedValidatorForKind returns a pointer to the `openapi3.Schema`
// validator corresponding to the given kind and version.
func GetVersionedValidatorForKind(kind, version string) (*openapi3.Schema, error) {
	var t *openapi3.T

	switch version {
	case "v0":
		var err error

		t, err = openapi3.NewLoader().LoadFromData(v0.OpenAPI)
		if err != nil {
			return nil, fmt.Errorf("loading OpenAPI schema for version %s: %w", version, err)
		}
	case "v1":
		var err error

		t, err = openapi3.NewLoader().LoadFromData(v1.OpenAPI)
		if err != nil {
			return nil, fmt.Errorf("loading OpenAPI schema for version %s: %w", version, err)
		}
	case "v2":
		var err error

		t, err = openapi3.NewLoader().LoadFromData(v2.OpenAPI)
		if err != nil {
			return nil, fmt.Errorf("loading OpenAPI schema for version %s: %w", version, err)
		}
	default:
		return nil, fmt.Errorf("unknown version %s", version)
	}

	err := t.Validate(context.Background())
	if err != nil {
		return nil, fmt.Errorf("validating OpenAPI schema for version %s: %w", version, err)
	}

	ref, ok := t.Components.Schemas[kind]
	if !ok {
		return nil, fmt.Errorf(
			"%w: no schema definition found for version %s of %s",
			ErrInvalidKind,
			version,
			kind,
		)
	}

	return ref.Value, nil
}
