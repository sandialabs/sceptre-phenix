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

// ValidateDocument validates a whole workflow config, parsed after
// ${BRANCH_NAME} expansion, against the Workflow schema. nil is an empty
// document. A failure wraps [types.ErrValidationFailed], with JSON pointers
// relative to the document root. Any other error means the schema did not load.
func ValidateDocument(doc map[string]any) error {
	if doc == nil {
		doc = map[string]any{}
	}

	if err := types.ValidateSchema(schemaName, schemaVersion, doc); err != nil {
		return fmt.Errorf("validating workflow config: %w", err)
	}

	return nil
}
