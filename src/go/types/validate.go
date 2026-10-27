package types

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"

	"phenix/store"
	"phenix/types/version"
)

var ErrValidationFailed = errors.New("config validation failed")

func init() { //nolint:gochecknoinits // library configuration
	// Keep schema validation errors concise
	openapi3.SchemaErrorDetailsDisabled = true //nolint:reassign // configure library error verbosity
}

// ValidateConfigSpec validates the spec in the given config against the
// component schema for its kind in the schema file of its own apiVersion, so
// a v1 Topology is checked against v1.yaml and a v2 Scenario against v2.yaml.
// A kind with no schema at its apiVersion fails validation, wrapping the
// lookup error. Any validation errors encountered are returned.
func ValidateConfigSpec(c store.Config) error {
	if g := c.APIGroup(); g != store.APIGroup {
		if g == "" {
			return fmt.Errorf(
				"%w: missing API group -- expected %s",
				ErrValidationFailed,
				store.APIGroup,
			)
		}

		return fmt.Errorf(
			"%w: invalid API group %s: expected %s",
			ErrValidationFailed,
			g,
			store.APIGroup,
		)
	}

	if err := ValidateConfig(c); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	err := ValidateSchema(c.Kind, c.APIVersion(), c.Spec)
	if err != nil && !errors.Is(err, ErrValidationFailed) {
		// no schema for the kind at this apiVersion: the config is invalid
		return fmt.Errorf(
			"%w: kind %s is not valid at apiVersion %s: %w",
			ErrValidationFailed,
			c.Kind,
			c.APIVersion(),
			err,
		)
	}

	return err
}

// ValidateSchema validates value against the component schema called name in
// the embedded OpenAPI schema file for version ver. The value is converted to
// JSON types first, so Go ints, typed maps and structs with JSON tags validate
// the same way a decoded JSON document does. A validation failure wraps
// [ErrValidationFailed] together with the kin-openapi error, whose JSON
// pointers are relative to value. Any other error means the schema could not
// be loaded.
func ValidateSchema(name, ver string, value any) error {
	v, err := version.GetVersionedValidatorForKind(name, ver)
	if err != nil {
		return fmt.Errorf("getting validator for config: %w", err)
	}

	// FIXME: using JSON marshal/unmarshal to get Go types converted to JSON
	// types. This is mainly needed for Go int types, since JSON only has float64.
	// There's a better way to do this, but it requires an update to the openapi3
	// package we're using.
	data, _ := json.Marshal(value)

	var doc any

	_ = json.Unmarshal(data, &doc)

	if err := v.VisitJSON(doc); err != nil {
		return fmt.Errorf("%w: %w", ErrValidationFailed, err)
	}

	return nil
}

// configSchema is the Config schema of [OpenAPI], loaded and validated once.
var configSchema = sync.OnceValues(func() (*openapi3.Schema, error) { //nolint:gochecknoglobals // parsed once per process
	t, err := openapi3.NewLoader().LoadFromData(OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("loading OpenAPI schema for configs: %w", err)
	}

	if err := t.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validating OpenAPI schema for configs: %w", err)
	}

	ref, ok := t.Components.Schemas["Config"]
	if !ok {
		return nil, errors.New("no schema definition found for configs")
	}

	return ref.Value, nil
})

func ValidateConfig(c store.Config) error {
	schema, err := configSchema()
	if err != nil {
		return err
	}

	// FIXME: using JSON marshal/unmarshal to get Go types converted to JSON
	// types. This is mainly needed for Go int types, since JSON only has float64.
	// There's a better way to do this, but it requires an update to the openapi3
	// package we're using.
	data, _ := json.Marshal(c)

	var spec any

	_ = json.Unmarshal(data, &spec)

	if err := schema.VisitJSON(spec); err != nil {
		return fmt.Errorf("%w: %w", ErrValidationFailed, err)
	}

	return nil
}
