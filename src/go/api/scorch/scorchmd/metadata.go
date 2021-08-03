package scorchmd

import (
	"fmt"

	"phenix/types"
	"phenix/util"

	"github.com/mitchellh/mapstructure"
)

var ErrScorchNotConfigured = fmt.Errorf("scorch not configured for experiment")

func DecodeMetadata(exp *types.Experiment) (ScorchMetadata, error) {
	var (
		ms map[string]interface{}
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

	if err := mapstructure.Decode(ms, &md); err != nil {
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
