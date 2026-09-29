package app

import (
	"context"

	"phenix/types"
)

// ManagedRunning delegates lifetime and activity state to an application's
// execution controller instead of the generic app-wide running flag.
type ManagedRunning interface {
	RunManaged(context.Context, *types.Experiment) error
}

var drainers = make(map[string]func(context.Context, string) error) //nolint:gochecknoglobals // startup-only registrations

func RegisterDrainer(name string, drain func(context.Context, string) error) {
	drainers[name] = drain
}

func Drain(ctx context.Context, exp *types.Experiment) error {
	for _, configured := range exp.Apps() {
		if drain := drainers[configured.Name()]; drain != nil {
			if err := drain(ctx, exp.Metadata.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
