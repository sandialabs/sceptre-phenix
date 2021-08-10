package types

import (
	"context"
	"encoding/json"
	"fmt"

	"phenix/store"
	"phenix/types/version"

	"github.com/getkin/kin-openapi/openapi3"
)

var ErrValidationFailed = fmt.Errorf("config validation failed")

// ValidateConfigSpec validates the spec in the given config using the
// appropriate `openapi3.Schema` validator. Any validation errors encountered
// are returned.
func ValidateConfigSpec(c store.Config) error {
	if g := c.APIGroup(); g != store.API_GROUP {
		if g == "" {
			return fmt.Errorf("%w: missing API group -- expected %s", ErrValidationFailed, store.API_GROUP)
		}

		return fmt.Errorf("%w: invalid API group %s: expected %s", ErrValidationFailed, g, store.API_GROUP)
	}

	if err := ValidateConfig(c); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	v, err := version.GetVersionedValidatorForKind(c.Kind, version.LATEST_VERSION)
	if err != nil {
		return fmt.Errorf("getting validator for config: %w", err)
	}

	// FIXME: using JSON marshal/unmarshal to get Go types converted to JSON
	// types. This is mainly needed for Go int types, since JSON only has float64.
	// There's a better way to do this, but it requires an update to the openapi3
	// package we're using.
	data, _ := json.Marshal(c.Spec)
	var spec interface{}
	json.Unmarshal(data, &spec)

	if err := v.VisitJSON(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	return nil
}

func ValidateConfig(c store.Config) error {
	t, err := openapi3.NewLoader().LoadFromData(OpenAPI)
	if err != nil {
		return fmt.Errorf("loading OpenAPI schema for configs: %w", err)
	}

	if err := t.Validate(context.Background()); err != nil {
		return fmt.Errorf("validating OpenAPI schema for configs: %w", err)
	}

	ref, ok := t.Components.Schemas["Config"]
	if !ok {
		return fmt.Errorf("no schema definition found for configs")
	}

	// FIXME: using JSON marshal/unmarshal to get Go types converted to JSON
	// types. This is mainly needed for Go int types, since JSON only has float64.
	// There's a better way to do this, but it requires an update to the openapi3
	// package we're using.
	data, _ := json.Marshal(c)
	var spec interface{}
	json.Unmarshal(data, &spec)

	if err := ref.Value.VisitJSON(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	return nil
}
