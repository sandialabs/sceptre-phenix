package builder

import (
	"errors"
	"fmt"
	"strings"
)

// LayerSetFileSchemaURI identifies the format of a layer set file: one layer
// set, as the Builder downloads and uploads it.
const LayerSetFileSchemaURI = "https://phenix.sandia.gov/schemas/builder/layersets/v1"

// LayerSetFile is a layer set as a file holds it, YAML or JSON. The Builder
// writes one when it downloads a set, and makes a set of the server's
// library from one when it uploads it.
type LayerSetFile struct {
	LayerSet

	// Schema is [LayerSetFileSchemaURI].
	Schema string `json:"$schema"`
	// Icons are copies of the custom icons that the set and its layers name,
	// by icon name, as a downloaded Builder document carries them. The set
	// can also name an icon that the file does not carry. The icon library
	// of the server is expected to hold that icon.
	Icons map[string]Icon `json:"icons,omitempty"`
}

// LayerSetError lists everything that makes a layer set unusable, as
// [LayerSet.Issues] finds it. It unwraps to [ErrInvalidLayerSet].
type LayerSetError struct {
	Issues []Issue
}

// ErrInvalidLayerSet is what a [LayerSetError] unwraps to.
var ErrInvalidLayerSet = errors.New("invalid layer set")

func (e *LayerSetError) Error() string {
	messages := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		messages[i] = issue.String()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidLayerSet.Error(), strings.Join(messages, "; "))
}

// Unwrap allows [errors.Is](err, ErrInvalidLayerSet).
func (e *LayerSetError) Unwrap() error {
	return ErrInvalidLayerSet
}

// Validate returns nil, or a *[LayerSetError] that lists every issue
// [LayerSet.Issues] finds with paths from the root of the set.
func (s *LayerSet) Validate() error {
	if issues := s.Issues(""); len(issues) > 0 {
		return &LayerSetError{Issues: issues}
	}

	return nil
}
