package scorch

import (
	"context"
	"fmt"

	"phenix/api/config"
	"phenix/store"
	"phenix/web/scorch"
)

var backgrounded = map[Action]map[string]context.CancelFunc{ //nolint:gochecknoglobals // global state
	ActionConfigure: make(map[string]context.CancelFunc),
	ActionStart:     make(map[string]context.CancelFunc),
	ActionStop:      make(map[string]context.CancelFunc),
	ActionCleanup:   make(map[string]context.CancelFunc),
	ActionDone:      make(map[string]context.CancelFunc),
	ActionLoop:      make(map[string]context.CancelFunc),
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
	case ActionStop:
		bgStage = ActionStart
	case ActionCleanup:
		bgStage = ActionConfigure
	case ActionConfigure, ActionStart, ActionDone, ActionLoop:
		return false
	default:
		return false
	}

	name := fmt.Sprintf("%s/%s/%s", options.Exp.Spec.ExperimentName(), bgStage, options.Name)

	if cancel, ok := backgrounded[bgStage][name]; ok {
		cancel()

		update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
			Exp:     options.Exp.Spec.ExperimentName(),
			Run:     options.Run,
			Loop:    options.Loop,
			Count:   options.Count,
			Stage:   string(bgStage),
			CmpType: options.Type,
			CmpName: options.Name,
			Status:  "success",
		}

		_ = scorch.UpdatePipeline(update)
		scorch.UpdateComponent(update)
		delete(backgrounded[bgStage], name)

		return true
	}

	return false
}

func init() { //nolint:gochecknoinits // config hook
	config.RegisterConfigHook("Experiment", func(stage string, c *store.Config) error {
		switch stage {
		case "update", "delete":
			scorch.DeletePipeline(c.Metadata.Name, -1, -1, false)
		}

		return nil
	})
}
