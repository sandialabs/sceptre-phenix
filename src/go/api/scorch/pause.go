package scorch

import (
	"context"
	"fmt"
	"time"

	"phenix/util"
	"phenix/web/scorch"

	"github.com/fatih/color"
	"github.com/mitchellh/mapstructure"
)

type PauseMetadata struct {
	Duration   string   `mapstructure:"duration"`
	FailStages []string `mapstructure:"failStages"`
}

func (this *PauseMetadata) Validate() error {
	if this.Duration == "" {
		this.Duration = "10s"
	}

	return nil
}

type Pause struct {
	options Options
}

func (this *Pause) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (this Pause) Type() string {
	return "pause"
}

func (this Pause) Configure(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONCONFIG, this.options)
		go this.pause(ctx, ACTIONCONFIG)
		return nil
	}

	return this.pause(ctx, ACTIONCONFIG)
}

func (this Pause) Start(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONSTART, this.options)
		go this.pause(ctx, ACTIONSTART)
		return nil
	}

	return this.pause(ctx, ACTIONSTART)
}

func (this Pause) Stop(ctx context.Context) error {
	if handleBackgrounded(ACTIONSTOP, this.options) {
		return nil
	}

	return this.pause(ctx, ACTIONSTOP)
}

func (this Pause) Cleanup(ctx context.Context) error {
	if handleBackgrounded(ACTIONCLEANUP, this.options) {
		return nil
	}

	return this.pause(ctx, ACTIONCLEANUP)
}

func (this Pause) pause(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var md PauseMetadata

	if err := mapstructure.Decode(this.options.Meta, &md); err != nil {
		return fmt.Errorf("decoding pause component metadata: %w", err)
	}

	if err := md.Validate(); err != nil {
		return fmt.Errorf("validating pause component metadata: %w", err)
	}

	d, err := time.ParseDuration(md.Duration)
	if err != nil {
		return fmt.Errorf("invalid duration provided for pause component: %w", err)
	}

	printer := color.New(color.FgYellow)
	printer.Printf("pausing for %v\n", d)

	update := scorch.ComponentUpdate{
		Exp:     this.options.Exp.Spec.ExperimentName(),
		CmpName: this.options.Name,
		CmpType: this.options.Type,
		Run:     this.options.Run,
		Loop:    this.options.Loop,
		Count:   this.options.Count,
		Stage:   string(stage),
		Status:  "running",
	}

	start := time.Now()

	for time.Since(start) < d {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			update.Output = []byte("pausing...\n")
			scorch.UpdateComponent(update)
		}
	}

	if util.StringSliceContains(md.FailStages, string(stage)) {
		return fmt.Errorf("failing as instructed")
	}

	return nil
}
