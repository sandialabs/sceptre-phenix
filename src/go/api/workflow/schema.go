package workflow

import (
	"fmt"

	"phenix/types"
)

// The Workflow schema is a component of the embedded v0 OpenAPI schema file.
// It is not a config kind: workflow configs are validated but never stored.
const (
	schemaName    = "Workflow"
	schemaVersion = "v0"
)

// ValidateDocument validates a whole workflow config, apiVersion and kind
// included, against the Workflow schema. doc is the config as parsed after
// ${VAR} expansion, so the schema sees every top-level key; nil is validated
// as an empty document. A validation failure wraps [types.ErrValidationFailed]
// and its JSON pointers are relative to the document root, so they match the
// source file. Any other error means the schema could not be loaded.
func ValidateDocument(doc map[string]any) error {
	if doc == nil {
		doc = map[string]any{}
	}

	if err := types.ValidateSchema(schemaName, schemaVersion, doc); err != nil {
		return fmt.Errorf("validating workflow config: %w", err)
	}

	return nil
}
