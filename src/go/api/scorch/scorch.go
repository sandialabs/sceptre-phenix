package scorch

import (
	"context"
	"fmt"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	"phenix/web/scorch"
)

var backgrounded = map[Action]map[string]context.CancelFunc{
	ACTIONCONFIG: make(map[string]context.CancelFunc),
	ACTIONSTART:  make(map[string]context.CancelFunc),
}

func background(ctx context.Context, stage Action, options Options) context.Context {
	if options.Background {
		var (
			cancel context.CancelFunc
			name   = fmt.Sprintf("%s/%s/%s", options.Exp.Spec.ExperimentName(), stage, options.Name)
		)

		ctx, cancel = context.WithCancel(ctx)
		backgrounded[stage][name] = cancel
	}

	return ctx
}

func handleBackgrounded(stage Action, options Options) bool {
	var bgStage Action

	switch stage {
	case ACTIONSTOP:
		bgStage = ACTIONSTART
	case ACTIONCLEANUP:
		bgStage = ACTIONCONFIG
	default:
		return false
	}

	name := fmt.Sprintf("%s/%s/%s", options.Exp.Spec.ExperimentName(), bgStage, options.Name)

	if cancel, ok := backgrounded[bgStage][name]; ok {
		cancel()

		update := scorch.ComponentUpdate{
			Exp:     options.Exp.Spec.ExperimentName(),
			Run:     options.Run,
			Loop:    options.Loop,
			Count:   options.Count,
			Stage:   string(bgStage),
			CmpType: options.Type,
			CmpName: options.Name,
			Status:  "success",
		}

		scorch.UpdatePipeline(update)
		scorch.UpdateComponent(update)
		delete(backgrounded[bgStage], name)

		return true
	}

	return false
}

func init() {
	config.RegisterConfigHook("Experiment", func(stage string, c *store.Config) error {
		exp, err := types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return fmt.Errorf("decoding experiment from config: %w", err)
		}

		switch stage {
		case "update", "delete":
			scorch.DeletePipeline(exp.Spec.ExperimentName(), -1, -1, false)
		}

		return nil
	})
}
