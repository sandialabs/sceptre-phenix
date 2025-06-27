package scorch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/app"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/scorch"

	"github.com/mitchellh/mapstructure"
)

type SOHMetadata struct {
	Checks      []string `mapstructure:"checks"`
	C2Timeout   string   `mapstructure:"c2Timeout"`
	FailOnError bool     `mapstructure:"failOnError"`
	LogLevel    string   `mapstructure:"logLevel"`
}

type SOH struct {
	options Options
}

func (this *SOH) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (this SOH) Type() string {
	return "soh"
}

func (this SOH) Configure(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONCONFIG, this.options)
		go this.check(ctx, ACTIONCONFIG)
		return nil
	}

	return this.check(ctx, ACTIONCONFIG)
}

func (this SOH) Start(ctx context.Context) error {
	if this.options.Background {
		ctx = background(ctx, ACTIONSTART, this.options)
		go this.check(ctx, ACTIONSTART)
		return nil
	}

	return this.check(ctx, ACTIONSTART)
}

func (this SOH) Stop(ctx context.Context) error {
	if handleBackgrounded(ACTIONSTOP, this.options) {
		return nil
	}

	return this.check(ctx, ACTIONSTOP)
}

func (this SOH) Cleanup(ctx context.Context) error {
	if handleBackgrounded(ACTIONCLEANUP, this.options) {
		return nil
	}

	return this.check(ctx, ACTIONCLEANUP)
}

func (this SOH) check(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var (
		exp = &this.options.Exp
		md  SOHMetadata
	)

	update := scorch.ComponentUpdate{
		Exp:     exp.Spec.ExperimentName(),
		CmpName: this.options.Name,
		CmpType: this.options.Type,
		Run:     this.options.Run,
		Loop:    this.options.Loop,
		Count:   this.options.Count,
		Stage:   string(stage),
		Status:  "running",
	}

	updateComponent := func(msg string) {
		update.Output = []byte(msg)
		scorch.UpdateComponent(update)
	}

	if !soh.Configured(exp) {
		updateComponent("State of health app is not configured for this experiment.\n")
		return fmt.Errorf("state of health checks: soh app not configured")
	}

	if !soh.Initialized(exp) {
		updateComponent("State of health app has not yet executed after experiment start.\n")
		return fmt.Errorf("state of health checks: soh app not initialized")
	}

	if soh.Running(exp) {
		updateComponent("State of health app is currently already running.\n")

		if md.FailOnError {
			return fmt.Errorf("state of health checks: app already running")
		}

		return nil
	}

	if err := mapstructure.Decode(this.options.Meta, &md); err != nil {
		updateComponent("Unable to parse metadata for this component.\n")
		return fmt.Errorf("decoding soh component metadata: %w", err)
	}

	if md.C2Timeout != "" {
		ctx = app.AddContextMetadata(ctx, "c2Timeout", md.C2Timeout)
	}

	if len(md.Checks) == 0 {
		updateComponent("Starting all state of health checks\n")
	} else {
		output := fmt.Sprintf("Starting state of health checks (limited to %v)\n", md.Checks)
		updateComponent(output)

		ctx = app.AddContextMetadata(ctx, "checks", md.Checks)
	}

	options := []app.Option{
		app.Stage(app.ACTIONRUNNING),
		app.FilterApp("soh"),
	}

	handlerName := fmt.Sprintf("%s-%d-%s", exp.Metadata.Name, this.options.Run, this.options.Name)
	plog.AddHandler(handlerName, plog.NewScorchSohHandler(handlerName, md.LogLevel, updateComponent))

	ctx = plog.ContextWithLogger(ctx, plog.With(plog.ScorchSohKey, handlerName), plog.TypeScorch)
	ctx = notes.Context(ctx, false)

	appErr := app.ApplyApps(ctx, exp, options...)
	plog.RemoveHandler(handlerName)

	exp, _ = experiment.Get(exp.Metadata.Name)

	var results map[string]any
	exp.Status.ParseAppStatus("soh", &results)

	body, _ := json.MarshalIndent(results, "", "  ")

	if appErr != nil {
		updateComponent(fmt.Sprintf("State of health checks failed: %v\n", appErr))
	} else {
		updateComponent("State of health checks succeeded\n")
	}

	updateComponent(string(body) + "\n")

	var (
		runDir = filepath.Join(exp.FilesDir(), "scorch", fmt.Sprintf("run-%d", this.options.Run))
		path   = filepath.Join(runDir, this.options.Name, fmt.Sprintf("loop-%d-count-%d", this.options.Loop, this.options.Count), "soh.json")
	)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		plog.Error(plog.TypeSoh, "creating directory for state of health results", "path", path, "err", err)
		updateComponent(fmt.Sprintf("Writing state of health results to file failed: %v\n", err))
	} else if err := os.WriteFile(path, body, 0644); err != nil {
		plog.Error(plog.TypeSoh, "writing state of health results", "path", path, "err", err)
		updateComponent(fmt.Sprintf("Writing state of health results to file failed: %v\n", err))
	} else {
		updateComponent(fmt.Sprintf("State of health results written to %s\n", path))
	}

	if appErr != nil && md.FailOnError {
		return fmt.Errorf("state of health checks: %w", appErr)
	}

	return nil
}
