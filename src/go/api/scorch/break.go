package scorch

import (
	"context"
	"fmt"

	"phenix/app"
	"phenix/util"
	"phenix/web/scorch"

	"github.com/mitchellh/mapstructure"
)

type BreakMetadata struct {
	Tap *TapMetadata `mapstructure:"tap"`
}

func (this *BreakMetadata) Validate() error {
	return this.Tap.Validate()
}

type Break struct {
	options Options
}

func (this *Break) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (Break) Type() string {
	return "break"
}

func (this Break) Configure(ctx context.Context) error {
	return this.breakPoint(ctx, ACTIONCONFIG)
}

func (this Break) Start(ctx context.Context) error {
	return this.breakPoint(ctx, ACTIONSTART)
}

func (this Break) Stop(ctx context.Context) error {
	return this.breakPoint(ctx, ACTIONSTOP)
}

func (this Break) Cleanup(ctx context.Context) error {
	return this.breakPoint(ctx, ACTIONCLEANUP)
}

func (this Break) breakPoint(ctx context.Context, stage Action) error {
	exp := this.options.Exp.Spec.ExperimentName()

	var md BreakMetadata

	if err := mapstructure.Decode(this.options.Meta, &md); err != nil {
		return fmt.Errorf("decoding 'break' component metadata: %w", err)
	}

	if err := md.Validate(); err != nil {
		return fmt.Errorf("validating break component metadata: %w", err)
	}

	if md.Tap != nil {
		// tap names cannot be longer than 15 characters
		// (dictated by max length of Linux interface names)
		tapName := fmt.Sprintf("break_tap_%s", util.RandomString(5))

		routed, err := getDefaultInterface()
		if err != nil {
			return fmt.Errorf("getting interface for default route: %w", err)
		}

		if err := setupTap(*md.Tap, exp, tapName, routed); err != nil {
			return fmt.Errorf("setting up tap interface for break: %w", err)
		}

		defer teardownTap(*md.Tap, exp, tapName, routed)
	}

	if app.IsContextTriggerCLI(ctx) {
		// this blocks until terminal is exited
		if err := terminal(ctx, "/bin/bash"); err != nil {
			return fmt.Errorf("starting bash terminal: %w", err)
		}
	} else if app.IsContextTriggerUI(ctx) {
		done, err := scorch.CreateWebTerminal(ctx, exp, this.options.Run, this.options.Loop, string(stage), this.options.Name, "/bin/bash")
		if err != nil {
			return fmt.Errorf("triggering web terminal: %w", err)
		}

		select {
		case <-ctx.Done():
			// don't return ctx error here so we can clean up tap and internet access below
		case <-done: // this blocks until web terminal is exited
		}
	}

	return ctx.Err()
}
