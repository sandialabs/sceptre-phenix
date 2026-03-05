package scorchmd

import (
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"

	"phenix/types"
	"phenix/util"
)

var ErrScorchNotConfigured = errors.New("scorch not configured for experiment")

func DecodeMetadata(exp *types.Experiment) (ScorchMetadata, error) {
	var (
		ms map[string]any
		md ScorchMetadata
	)

	for _, app := range exp.Apps() {
		if app.Name() == "scorch" {
			ms = util.CopyableMap(app.Metadata()).DeepCopy()

			break
		}
	}

	if ms == nil {
		return md, ErrScorchNotConfigured
	}

	err := mapstructure.Decode(ms, &md)
	if err != nil {
		return md, fmt.Errorf("decoding app metadata: %w", err)
	}

	md.components = make(ComponentSpecMap)

	for _, c := range md.Components {
		md.components[c.Name] = c
	}

	for _, run := range md.Runs {
		ensureCount(run)
	}

	return md, nil
}

// Ensure missing run loop counts default to 1.
func ensureCount(run *Loop) {
	if run.Count == 0 {
		run.Count = 1
	}

	if run.Loop != nil {
		ensureCount(run.Loop)
	}
}
