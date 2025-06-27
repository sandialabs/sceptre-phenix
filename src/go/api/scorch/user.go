package scorch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"

	"phenix/util"
	"phenix/util/common"
	"phenix/util/plog"
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

	logPipePath := filepath.Join(common.PhenixBase, "experiments", this.options.Exp.Spec.ExperimentName(), "scorch_pipes", this.options.Name)
	done, err := plog.ReadProcessLogs(logPipePath, plog.TypeScorch, "component", this.options.Name, "stage", stage, "exp", this.options.Exp.Spec.ExperimentName())
	defer close(done)
	if err != nil {
		return err
	}

	stdout := make(chan []byte)

	opts := []shell.Option{
		shell.Command(cmd),
		shell.Args(string(stage), this.options.Name, strconv.Itoa(this.options.Run), strconv.Itoa(this.options.Loop), strconv.Itoa(this.options.Count)),
		shell.Stdin(data),
		shell.StreamStdout(stdout),
		shell.Env(
			"PHENIX_DIR="+common.PhenixBase,
			"PHENIX_FILES_DIR="+this.options.Exp.FilesDir(),
			"PHENIX_LOG_LEVEL="+util.GetEnv("PHENIX_LOG_LEVEL", "DEBUG"),
			"PHENIX_LOG_FILE="+logPipePath,
			"PHENIX_DRYRUN="+strconv.FormatBool(this.options.Exp.DryRun()),
			"PHENIX_SCORCH_STARTTIME="+this.options.StartTime,
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
		plog.Warn(plog.TypeScorch, "component returned stderr", "stderr", string(stderrBytes), "component", this.options.Name, "stage", stage, "exp", this.options.Exp.Spec.ExperimentName())


		return fmt.Errorf("external user component %s (command %s) failed: %w", this.options.Type, cmd, err)
	}

	if len(stdoutBytes) != 0 {
		plog.Info(plog.TypePhenixApp, string(stdoutBytes), 
				"experiment", this.options.Exp.Spec.ExperimentName(),
				"app", "scorch",
				"component", this.options.Name,
				"stage", string(stage),
				"run", strconv.Itoa(this.options.Run),
				"loop", strconv.Itoa(this.options.Loop),
				"count", strconv.Itoa(this.options.Count),
			)
	}

	return nil
}
