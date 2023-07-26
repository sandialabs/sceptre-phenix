package scorch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/util"
	"phenix/util/mm"
	"phenix/util/tap"
	"phenix/web/scorch"

	"github.com/mitchellh/mapstructure"
)

type BreakMetadata struct {
	Tap    *tap.Tap `mapstructure:"tap"`
	Readme string   `mapstructure:"readme"`
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

	if md.Tap != nil {
		pairs := discoverUsedPairs()
		md.Tap.Init(tap.Experiment(exp), tap.UsedPairs(pairs))

		// backwards compatibility (doesn't support external access firewall rules)
		if v, ok := md.Tap.Other["internetAccess"]; ok {
			enabled, _ := v.(bool)
			md.Tap.External.Enabled = enabled
		}

		// tap names cannot be longer than 15 characters
		// (dictated by max length of Linux interface names)
		md.Tap.Name = fmt.Sprintf("%s-tapbrk", util.RandomString(8))

		if _, err := md.Tap.Create(mm.Headnode()); err != nil {
			return fmt.Errorf("setting up tap for break: %w", err)
		}

		var status scorchmd.ScorchStatus
		if err := this.options.Exp.Status.ParseAppStatus("scorch", &status); err != nil {
			return fmt.Errorf("getting experiment status for scorch app: %w", err)
		}

		status.Taps[this.options.Name] = md.Tap

		this.options.Exp.Status.SetAppStatus("scorch", status)
		this.options.Exp.WriteToStore(true)

		defer md.Tap.Delete(mm.Headnode())
	}

	var (
		cmd  string
		args []string
	)

	dir, err := os.MkdirTemp("", "*")
	if err != nil {
		return fmt.Errorf("creating temporary directory for break component: %w", err)
	}

	defer os.RemoveAll(dir)

	if md.Readme == "" {
		if md.Tap == nil {
			cmd = "/bin/bash"
		} else {
			// create shell in the network namespace created for the tap
			cmd = "ip"
			args = []string{"netns", "exec", md.Tap.Name, "/bin/bash"}
		}
	} else {
		readme := filepath.Join(dir, "README")

		if err := os.WriteFile(readme, []byte(md.Readme), 0644); err != nil {
			return fmt.Errorf("writing break component README to file: %w", err)
		}

		cmd = "/usr/local/bin/glow"
		args = []string{"README"}
	}

	if app.IsContextTriggerCLI(ctx) {
		// this blocks until terminal is exited
		if err := terminal(ctx, dir, cmd, args); err != nil {
			return fmt.Errorf("starting bash terminal: %w", err)
		}
	} else if app.IsContextTriggerUI(ctx) {
		done, err := scorch.CreateWebTerminal(ctx, exp, this.options.Run, this.options.Loop, string(stage), this.options.Name, dir, cmd, args)
		if err != nil {
			return fmt.Errorf("triggering web terminal: %w", err)
		}

		select {
		case <-ctx.Done(): // this blocks until the context is canceled
		case <-done: // this blocks until web terminal is exited
		}
	}

	return ctx.Err()
}
