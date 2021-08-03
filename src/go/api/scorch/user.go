package scorch

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"phenix/internal/common"
	"phenix/store"
	"phenix/util"
	"phenix/util/shell"
	"phenix/web/scorch"
)

var ErrUserComponentNotFound = fmt.Errorf("user component not found")

type UserComponent struct {
	options Options
}

func (this *UserComponent) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (this UserComponent) Type() string {
	return this.options.Type
}

func (this UserComponent) Configure(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONCONFIG, this.options)
	}

	return this.shellOut(ctx, ACTIONCONFIG)
}

func (this UserComponent) Start(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONSTART, this.options)
	}

	return this.shellOut(ctx, ACTIONSTART)
}

func (this UserComponent) Stop(ctx context.Context) error {
	handleBackgrounded(ACTIONSTOP, this.options)
	return this.shellOut(ctx, ACTIONSTOP)
}

func (this UserComponent) Cleanup(ctx context.Context) error {
	handleBackgrounded(ACTIONCLEANUP, this.options)
	return this.shellOut(ctx, ACTIONCLEANUP)
}

func (this UserComponent) shellOut(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	cmd := "phenix-scorch-component-" + this.options.Type

	if !shell.CommandExists(cmd) {
		return fmt.Errorf("external user component %s does not exist in your path: %w", cmd, ErrUserComponentNotFound)
	}

	data, err := json.Marshal(this.options.Exp)
	if err != nil {
		return fmt.Errorf("marshaling experiment metadata to JSON: %w", err)
	}

	// TODO: consider letting the child process send a signal indicating it wants
	// to run in the background instead of having to configure it in the scenario.

	if this.options.Background {
		go this.run(ctx, stage, cmd, data)
		return nil
	}

	return this.run(ctx, stage, cmd, data)
}

func (this UserComponent) run(ctx context.Context, stage Action, cmd string, data []byte) error {
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

	stdout := make(chan []byte)

	opts := []shell.Option{
		shell.Command(cmd),
		shell.Args(string(stage), this.options.Name, strconv.Itoa(this.options.Run), strconv.Itoa(this.options.Loop), strconv.Itoa(this.options.Count)),
		shell.Stdin(data),
		shell.StreamStdout(stdout),
		shell.Env(
			"PHENIX_DIR="+common.PhenixBase,
			"PHENIX_LOG_LEVEL="+util.GetEnv("PHENIX_LOG_LEVEL", "DEBUG"),
			"PHENIX_LOG_FILE="+util.GetEnv("PHENIX_LOG_FILE", common.LogFile),
			"PHENIX_DRYRUN="+strconv.FormatBool(this.options.Exp.DryRun()),
		),
	}

	go func() {
		for output := range stdout {
			update.Output = append(output, []byte("\n")...)
			scorch.UpdateComponent(update)
		}
	}()

	stdoutBytes, stderrBytes, err := shell.ExecCommand(ctx, opts...)
	if err != nil {
		// FIXME: improve on this
		fmt.Println(string(stderrBytes))

		return fmt.Errorf("external user component %s (command %s) failed: %w", this.options.Type, cmd, err)
	}

	if len(stdoutBytes) != 0 {
		event := store.NewHistoryEvent(string(stdoutBytes)).
			WithMetadata("experiment", this.options.Exp.Spec.ExperimentName()).
			WithMetadata("app", "scorch").
			WithMetadata("component", this.options.Name).
			WithMetadata("stage", string(stage)).
			WithMetadata("run", strconv.Itoa(this.options.Run)).
			WithMetadata("loop", strconv.Itoa(this.options.Loop)).
			WithMetadata("count", strconv.Itoa(this.options.Count))

		store.AddEvent(*event)
	}

	return nil
}
