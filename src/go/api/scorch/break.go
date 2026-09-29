package scorch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/mapstructure"

	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/util"
	"phenix/util/tap"
	"phenix/web/scorch"
)

const tapBreakSuffixLen = 8

type BreakMetadata struct {
	Tap    *tap.Tap `mapstructure:"tap"`
	Readme string   `mapstructure:"readme"`
}

type Break struct {
	options Options
}

func (b *Break) Init(opts ...Option) error {
	b.options = NewOptions(opts...)

	return nil
}

func (Break) Type() string {
	return "break"
}

func (b Break) Configure(ctx context.Context) error {
	return b.breakPoint(ctx, ActionConfigure)
}

func (b Break) Start(ctx context.Context) error {
	return b.breakPoint(ctx, ActionStart)
}

func (b Break) Stop(ctx context.Context) error {
	return b.breakPoint(ctx, ActionStop)
}

func (b Break) Cleanup(ctx context.Context) error {
	return b.breakPoint(ctx, ActionCleanup)
}

func (b Break) breakPoint(ctx context.Context, stage Action) error {
	exp := b.options.Exp.Spec.ExperimentName()

	var md BreakMetadata

	if err := mapstructure.Decode(b.options.Meta, &md); err != nil {
		return fmt.Errorf("decoding 'break' component metadata: %w", err)
	}

	if md.Tap != nil {
		// backwards compatibility (doesn't support external access firewall rules)
		if v, ok := md.Tap.Other["internetAccess"]; ok {
			enabled, _ := v.(bool)
			md.Tap.External.Enabled = enabled
		}

		// tap names cannot be longer than 15 characters
		// (dictated by max length of Linux interface names)
		md.Tap.Name = util.RandomString(tapBreakSuffixLen) + "-tapbrk"

		if err := createOwnedTap(ctx, b.options, md.Tap); err != nil {
			return fmt.Errorf("setting up tap for break: %w", err)
		}
		defer func() { _ = deleteOwnedTap(ctx, b.options) }()
	}

	var (
		cmd  string
		args []string
	)

	dir, err := os.MkdirTemp("", "*")
	if err != nil {
		return fmt.Errorf("creating temporary directory for break component: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

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

		err = os.WriteFile(readme, []byte(md.Readme), 0o600)
		if err != nil {
			return fmt.Errorf("writing break component README to file: %w", err)
		}

		cmd = "/usr/local/bin/glow"
		args = []string{"README"}
	}

	if err := scorchexe.State(
		ctx,
		exp,
		scorchmd.StateWaiting,
		string(stage),
		b.options.Name,
		b.options.Loop,
		b.options.Count,
		"breakpoint",
		"",
	); err != nil {
		return err
	}
	if app.IsContextTriggerCLI(ctx) {
		// this blocks until terminal is exited
		err = terminal(ctx, dir, cmd, args)
		if err != nil {
			return fmt.Errorf("starting bash terminal: %w", err)
		}
	} else if app.IsContextTriggerUI(ctx) {
		var done <-chan struct{}
		done, err = scorch.CreateWebTerminal(
			ctx,
			scorchexe.ExecutionID(ctx),
			exp,
			b.options.Run,
			b.options.Loop,
			string(stage),
			b.options.Name,
			dir,
			cmd,
			args,
		)
		if err != nil {
			return fmt.Errorf("triggering web terminal: %w", err)
		}

		select {
		case <-ctx.Done(): // this blocks until the context is canceled
		case <-done: // this blocks until web terminal is exited
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scorchexe.State(ctx, exp, scorchmd.StateRunning, string(stage), b.options.Name, b.options.Loop, b.options.Count, "", "")
}
