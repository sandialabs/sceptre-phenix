package scorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/mapstructure"

	"phenix/api/experiment"
	"phenix/api/soh"
	"phenix/app"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/web/scorch"
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

func (s *SOH) Init(opts ...Option) error {
	s.options = NewOptions(opts...)

	return nil
}

func (s SOH) Type() string {
	return "soh"
}

func (s SOH) Configure(ctx context.Context) error {
	if s.options.Background {
		ctx = background(ctx, ActionConfigure, s.options)

		go func() { _ = s.check(ctx, ActionConfigure) }()

		return nil
	}

	return s.check(ctx, ActionConfigure)
}

func (s SOH) Start(ctx context.Context) error {
	if s.options.Background {
		ctx = background(ctx, ActionStart, s.options)

		go func() { _ = s.check(ctx, ActionStart) }()

		return nil
	}

	return s.check(ctx, ActionStart)
}

func (s SOH) Stop(ctx context.Context) error {
	if handleBackgrounded(ActionStop, s.options) {
		return nil
	}

	return s.check(ctx, ActionStop)
}

func (s SOH) Cleanup(ctx context.Context) error {
	if handleBackgrounded(ActionCleanup, s.options) {
		return nil
	}

	return s.check(ctx, ActionCleanup)
}

//nolint:funlen // complex logic
func (s SOH) check(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	var (
		exp = &s.options.Exp
		md  SOHMetadata
	)

	update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
		Exp:     exp.Spec.ExperimentName(),
		CmpName: s.options.Name,
		CmpType: s.options.Type,
		Run:     s.options.Run,
		Loop:    s.options.Loop,
		Count:   s.options.Count,
		Stage:   string(stage),
		Status:  "running",
	}

	updateComponent := func(msg string) {
		update.Output = []byte(msg)
		scorch.UpdateComponent(update)
	}

	if !soh.Configured(exp) {
		updateComponent("State of health app is not configured for this experiment.\n")

		return errors.New("state of health checks: soh app not configured")
	}

	if !soh.Initialized(exp) {
		updateComponent("State of health app has not yet executed after experiment start.\n")

		return errors.New("state of health checks: soh app not initialized")
	}

	if soh.Running(exp) {
		updateComponent("State of health app is currently already running.\n")

		if md.FailOnError {
			return errors.New("state of health checks: app already running")
		}

		return nil
	}

	err := mapstructure.Decode(s.options.Meta, &md)
	if err != nil {
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
		app.Stage(app.ActionRunning),
		app.FilterApp("soh"),
	}

	handlerName := fmt.Sprintf("%s-%d-%s", exp.Metadata.Name, s.options.Run, s.options.Name)
	plog.AddHandler(
		handlerName,
		plog.NewScorchSohHandler(handlerName, md.LogLevel, updateComponent),
	)

	ctx = plog.ContextWithLogger(ctx, plog.With(plog.ScorchSohKey, handlerName), plog.TypeScorch)
	ctx = notes.Context(ctx, false)

	appErr := app.ApplyApps(ctx, exp, options...)

	plog.RemoveHandler(handlerName)

	exp, _ = experiment.Get(exp.Metadata.Name)

	var results map[string]any

	_ = exp.Status.ParseAppStatus("soh", &results)

	body, _ := json.MarshalIndent(results, "", "  ")

	if appErr != nil {
		updateComponent(fmt.Sprintf("State of health checks failed: %v\n", appErr))
	} else {
		updateComponent("State of health checks succeeded\n")
	}

	updateComponent(string(body) + "\n")

	var (
		runDir = filepath.Join(exp.FilesDir(), "scorch", fmt.Sprintf("run-%d", s.options.Run))
		path   = filepath.Join(
			runDir,
			s.options.Name,
			fmt.Sprintf("loop-%d-count-%d", s.options.Loop, s.options.Count),
			"soh.json",
		)
	)

	err = os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		plog.Error(
			plog.TypeSoh,
			"creating directory for state of health results",
			"path",
			path,
			"err",
			err,
		)
		updateComponent(fmt.Sprintf("Writing state of health results to file failed: %v\n", err))
	} else {
		err = os.WriteFile(path, body, 0o600)
		if err != nil {
			plog.Error(plog.TypeSoh, "writing state of health results", "path", path, "err", err)
			updateComponent(fmt.Sprintf("Writing state of health results to file failed: %v\n", err))
		} else {
			updateComponent(fmt.Sprintf("State of health results written to %s\n", path))
		}
	}

	if appErr != nil && md.FailOnError {
		return fmt.Errorf("state of health checks: %w", appErr)
	}

	return nil
}
