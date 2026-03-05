package scorch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/mitchellh/mapstructure"

	"phenix/util"
	"phenix/web/scorch"
)

type PauseMetadata struct {
	Duration   string   `mapstructure:"duration"`
	FailStages []string `mapstructure:"failStages"`
}

func (p *PauseMetadata) Validate() error {
	if p.Duration == "" {
		p.Duration = "10s"
	}

	return nil
}

type Pause struct {
	options Options
}

func (p *Pause) Init(opts ...Option) error {
	p.options = NewOptions(opts...)

	return nil
}

func (p Pause) Type() string {
	return "pause"
}

func (p Pause) Configure(ctx context.Context) error {
	if p.options.Background {
		ctx = background(ctx, ActionConfigure, p.options)

		go func() { _ = p.pause(ctx, ActionConfigure) }()

		return nil
	}

	return p.pause(ctx, ActionConfigure)
}

func (p Pause) Start(ctx context.Context) error {
	if p.options.Background {
		ctx = background(ctx, ActionStart, p.options)

		go func() { _ = p.pause(ctx, ActionStart) }()

		return nil
	}

	return p.pause(ctx, ActionStart)
}

func (p Pause) Stop(ctx context.Context) error {
	if handleBackgrounded(ActionStop, p.options) {
		return nil
	}

	return p.pause(ctx, ActionStop)
}

func (p Pause) Cleanup(ctx context.Context) error {
	if handleBackgrounded(ActionCleanup, p.options) {
		return nil
	}

	return p.pause(ctx, ActionCleanup)
}

func (p Pause) pause(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var md PauseMetadata

	if err := mapstructure.Decode(p.options.Meta, &md); err != nil {
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
	_, _ = printer.Printf("pausing for %v\n", d)

	update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
		Exp:     p.options.Exp.Spec.ExperimentName(),
		CmpName: p.options.Name,
		CmpType: p.options.Type,
		Run:     p.options.Run,
		Loop:    p.options.Loop,
		Count:   p.options.Count,
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
		return errors.New("failing as instructed")
	}

	return nil
}
