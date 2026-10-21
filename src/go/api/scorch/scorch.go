package scorch

import (
	"fmt"

	"phenix/api/scorch/scorchmd"
	"phenix/types"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
	"phenix/web/scorch"
)

func init() { //nolint:gochecknoinits // config hook
	config.RegisterConfigHook("Experiment", func(stage string, c *store.Config) error {
		switch stage {
		case "update", "delete":
			current, err := experiment.Get(c.Metadata.Name)
			if err == nil {
				status, err := scorchmd.Status(current)
				if err != nil {
					return err
				}
				for _, e := range status.Executions {
					if e.Active() || e.State == scorchmd.StateInterrupted {
						return fmt.Errorf("Scorch run %d must finish or recover before experiment configuration changes", e.Run)
					}
				}
			}
			scorch.DeletePipeline(c.Metadata.Name, -1, -1, false)
		}

		return nil
	})

	experiment.RegisterHook("start", func(_ string, name string) { // clear SCORCH pipeline on experiment start
		scorch.DeletePipeline(name, -1, -1, false)
		_ = scorchmd.Mutate(name, func(_ *types.Experiment, s *scorchmd.ScorchStatus) error {
			s.Stopping = false
			return nil
		})
	})
}
