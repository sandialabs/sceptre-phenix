package version

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	v0 "phenix/types/version/v0"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

//go:embed schemas/*.yaml
var openAPISchemas embed.FS

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
	"Setting":    "v2",
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
	if kind == "" {
		return nil, fmt.Errorf("%w: kind cannot be empty", ErrInvalidKind)
	}

	var api struct {
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}

	schemaText, err := ReadSchemaFile(version)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(schemaText, &api)
	if err != nil {
		return nil, fmt.Errorf("parsing %s OpenAPI schema: %w", version, err)
	}

	kind = strings.ToUpper(kind[:1]) + kind[1:]

	schema, ok := api.Components.Schemas[kind]
	if !ok {
		return nil, fmt.Errorf(
			"%w: no schema definition found for version %s of %s",
			ErrInvalidKind,
			version,
			kind,
		)
	}

	return schema, nil
}

// GetVersionedValidatorForKind returns a pointer to the `openapi3.Schema`
// validator corresponding to the given kind and version.
func GetVersionedValidatorForKind(kind, version string) (*openapi3.Schema, error) {
	schemaText, err := ReadSchemaFile(version)
	if err != nil {
		return nil, err
	}

	t, err := openapi3.NewLoader().LoadFromData(schemaText)
	if err != nil {
		return nil, fmt.Errorf("loading OpenAPI schema for version %s: %w", version, err)
	}

	err = t.Validate(context.Background())
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

// ReadSchemaFile reads the embedded OpenAPI schema YAML file for the given version.
func ReadSchemaFile(version string) ([]byte, error) {
	filename := fmt.Sprintf("schemas/%s.yaml", version)

	schemaText, err := openAPISchemas.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading embedded OpenAPI schema %s: %w", filename, err)
	}

	return schemaText, nil
}
