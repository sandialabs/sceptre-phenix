package scorchexe

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
)

func validateInteractive(ctx context.Context, md scorchmd.ScorchMetadata, run int) error {
	if app.IsContextTriggerCLI(ctx) || app.IsContextTriggerUI(ctx) {
		return nil
	}
	for loop := md.Runs[run]; loop != nil; loop = loop.Loop {
		for _, stage := range [][]string{loop.Configure, loop.Start, loop.Stop, loop.Cleanup} {
			for _, name := range stage {
				if md.ComponentSpecs()[name].Type == componentBreak {
					return errors.New("breakpoints require a web or interactive CLI controller")
				}
			}
		}
	}
	return nil
}

const (
	capabilityTimeout = 2 * time.Second
	componentBreak    = "break"
)

// Older component packages remain usable serially. They cannot safely share
// namespace CC settings with managed components, so admission is exclusive.
func legacyComponents(ctx context.Context, md scorchmd.ScorchMetadata, run int) bool {
	checked := make(map[string]bool)
	for loop := md.Runs[run]; loop != nil; loop = loop.Loop {
		for _, stage := range [][]string{loop.Configure, loop.Start, loop.Stop, loop.Cleanup} {
			for _, name := range stage {
				typ := md.ComponentSpecs()[name].Type
				switch typ {
				case componentBreak, "pause", "soh", "tap":
					continue
				}
				if checked[typ] {
					continue
				}
				checked[typ] = true
				probeCtx, cancel := context.WithTimeout(ctx, capabilityTimeout)
				body, err := exec.CommandContext(probeCtx, "phenix-scorch-component-"+typ, "--scorch-capabilities").Output()
				cancel()
				var capabilities struct {
					Protocol int `json:"scorchConcurrencyProtocol"`
				}
				if err != nil || json.Unmarshal(body, &capabilities) != nil || capabilities.Protocol != 1 {
					return true
				}
			}
		}
	}
	return false
}
