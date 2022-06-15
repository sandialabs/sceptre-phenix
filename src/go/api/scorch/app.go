package scorch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/shell"
	"phenix/web/scorch"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/activeshadow/structs"
	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"
)

func init() {
	app.RegisterUserApp(newScorch())
}

type Scorch struct {
	md scorchmd.ScorchMetadata

	options app.Options
}

func newScorch() *Scorch {
	return new(Scorch)
}

func (this *Scorch) Init(opts ...app.Option) error {
	this.options = app.NewOptions(opts...)
	return nil
}

func (Scorch) Name() string {
	return "scorch"
}

func (Scorch) Configure(ctx context.Context, exp *types.Experiment) error {
	var app ifaces.ScenarioApp

	for _, app = range exp.Apps() {
		if app.Name() == "scorch" {
			break // this will keep `app` set to SCORCH app
		}
	}

	var md scorchmd.ScorchMetadata

	if err := mapstructure.Decode(app.Metadata(), &md); err != nil {
		return fmt.Errorf("decoding app metadata: %w", err)
	}

	// Ensure type is set for each component.
	for idx, c := range md.Components {
		if c.Type == "" {
			c.Type = c.Name
			md.Components[idx] = c
		}
	}

	body := structs.MapDefaultCase(md, structs.CASESNAKE)
	app.SetMetadata(body)

	return nil
}

func (Scorch) PreStart(context.Context, *types.Experiment) error {
	return nil
}

func (this Scorch) PostStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (this *Scorch) Running(ctx context.Context, exp *types.Experiment) error {
	var err error

	if this.md, err = scorchmd.DecodeMetadata(exp); err != nil {
		return err
	}

	runID := scorchexe.MustRunID(ctx)

	if runID < 0 || runID >= len(this.md.Runs) {
		return fmt.Errorf("invalid Scorch run ID for experiment %s", exp.Metadata.Name)
	}

	if this.md.Filebeat.Enabled {
		if err := createFilebeatConfig(this.md, exp.Spec.ExperimentName(), exp.FilesDir(), runID); err != nil {
			return fmt.Errorf("creating Filebeat config: %w", err)
		}
	}

	cmd, err := this.startFilebeat(ctx, exp.FilesDir(), runID)
	if err != nil {
		return fmt.Errorf("starting Filebeat: %w", err)
	}

	defer this.stopFilebeat(ctx, cmd, exp.FilesDir(), runID)

	var (
		errors error
		run    = this.md.Runs[runID]
		opts   = []Option{Experiment(*exp), RunID(runID), StartTime(time.Now().UTC().Format("Mon Jan 02 15:04:05 -0700 2006"))}
	)

	for i := 0; i < run.Count; i++ {
		opts := append(opts, LoopCount(i))

		if err := executor(ctx, this.md.ComponentSpecs(), run, opts...); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("executing Scorch for run %d, count %d: %w", runID, i, err))
			break
		}
	}

	return errors
}

func (Scorch) Cleanup(context.Context, *types.Experiment) error {
	return nil
}

func (this Scorch) startFilebeat(ctx context.Context, baseDir string, runID int) (*exec.Cmd, error) {
	var (
		cmd    *exec.Cmd
		stdOut bytes.Buffer
		stdErr bytes.Buffer
	)

	// TODO: don't rely on `shell.ProcessExists` since it's not working correctly
	// for detecting zombie (defunct) processes. For example, when filebeat fails
	// to start because of an issue w/ the config the current code still thinks
	// it's running. Instead, call `cmd.Wait` in a Goroutine and use a channel or
	// atomic int to determine later on if the command has finished.

	if this.md.Filebeat.Enabled && shell.CommandExists("filebeat") {
		var (
			base = fmt.Sprintf("%s/scorch/run-%d/filebeat", baseDir, runID)
			data = fmt.Sprintf("%s/data", base)
			cfg  = fmt.Sprintf("%s/filebeat.yml", base)
		)

		cmd = exec.CommandContext(ctx, "filebeat", "-c", cfg, "--path.data", data)

		cmd.Stdin = nil
		cmd.Stdout = &stdOut
		cmd.Stderr = &stdErr

		if err := cmd.Start(); err != nil {
			return nil, err
		}

		// Give Filebeat time to start up or die.
		time.Sleep(2 * time.Second)

		if !shell.ProcessExists(cmd.Process.Pid) {
			if err := cmd.Wait(); err != nil {
				return nil, err
			}
		}

		fmt.Println("FILEBEAT STARTED")
	}

	return cmd, nil
}

func (this Scorch) stopFilebeat(ctx context.Context, cmd *exec.Cmd, baseDir string, runID int) {
	if cmd != nil {
		if shell.ProcessExists(cmd.Process.Pid) {
			defer func() {
				cmd.Process.Signal(os.Interrupt)
				cmd.Wait()
			}()

			fmt.Println("FILEBEAT STILL RUNNING")

			reg := fmt.Sprintf("%s/scorch/run-%d/filebeat/registry/filebeat/log.json", baseDir, runID)

			for i := 0; i < 60; i++ {
				select {
				case <-ctx.Done():
					return
				default:
					fmt.Println("WAITING FOR FILEBEAT TO FINISH")

					time.Sleep(1 * time.Second)

					stats, err := os.Stat(reg)
					if err != nil {
						fmt.Println("  NO REGISTRY FILE")
						continue
					}

					if stats.Size() > 0 {
						// Give Filebeat a little more time to finish processing logs.
						fmt.Println("  GIVING FILEBEAT 5 MORE SECONDS")
						time.Sleep(5 * time.Second)
						return
					}
				}
			}
		} else {
			if err := cmd.Wait(); err != nil {
				log.Error("the Filebeat process terminated early (logs may be missing): %v", err)
			}
		}
	}
}

func executor(ctx context.Context, components scorchmd.ComponentSpecMap, exe *scorchmd.Loop, opts ...Option) error {
	options := NewOptions(opts...)

	var (
		exp        = options.Exp.Spec.ExperimentName()
		loopPrefix = fmt.Sprintf("[RUN: %d - LOOP: %d - COUNT: %d]", options.Run, options.Loop, options.Count)
	)

	if options.Loop == 0 {
		scorch.DeletePipeline(exp, options.Run, -1, true)
	}

	update := scorch.ComponentUpdate{
		Exp:   exp,
		Run:   options.Run,
		Loop:  options.Loop,
		Count: options.Count,
	}

	configure := func() error {
		update.Stage = string(ACTIONCONFIG)

		if len(exe.Configure) == 0 {
			update.CmpType = ""
			update.CmpName = ""
			update.Status = "success"
			scorch.UpdatePipeline(update)
			return nil
		}

		for _, name := range exe.Configure {
			typ := components[name].Type

			update.CmpType = typ
			update.CmpName = name
			update.Status = "start"

			scorch.UpdateComponent(update)

			options := append(opts, Name(name), Type(typ), Stage(ACTIONCONFIG), Metadata(components[name].Metadata))

			status := "running"

			if components[name].Background {
				options = append(options, Background())
				status = "background"
			}

			update.Status = status
			scorch.UpdateComponent(update)
			scorch.UpdatePipeline(update)

			if err := ExecuteComponent(ctx, options...); err != nil {
				update.Status = "failure"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)

				return fmt.Errorf("%s configuring component %s for experiment %s: %w", loopPrefix, name, exp, err)
			}

			if !components[name].Background {
				update.Status = "success"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)
			}
		}

		return nil
	}

	start := func() error {
		update.Stage = string(ACTIONSTART)

		if len(exe.Start) == 0 {
			update.CmpType = ""
			update.CmpName = ""
			update.Status = "success"
			scorch.UpdatePipeline(update)
			return nil
		}

		for _, name := range exe.Start {
			typ := components[name].Type

			update.CmpType = typ
			update.CmpName = name
			update.Status = "start"

			scorch.UpdateComponent(update)

			options := append(opts, Name(name), Type(typ), Stage(ACTIONSTART), Metadata(components[name].Metadata))

			status := "running"

			if components[name].Background {
				options = append(options, Background())
				status = "background"
			}

			update.Status = status
			scorch.UpdateComponent(update)
			scorch.UpdatePipeline(update)

			if err := ExecuteComponent(ctx, options...); err != nil {
				update.Status = "failure"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)

				return fmt.Errorf("%s starting component %s for experiment %s: %w", loopPrefix, name, exp, err)
			}

			if !components[name].Background {
				update.Status = "success"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)
			}
		}

		return nil
	}

	stop := func() error {
		update.Stage = string(ACTIONSTOP)

		if len(exe.Stop) == 0 {
			update.CmpType = ""
			update.CmpName = ""
			update.Status = "success"
			scorch.UpdatePipeline(update)
			return nil
		}

		var errors error

		for _, name := range exe.Stop {
			typ := components[name].Type

			update.CmpType = typ
			update.CmpName = name
			update.Status = "start"

			scorch.UpdateComponent(update)

			options := append(opts, Name(name), Type(typ), Stage(ACTIONSTOP), Metadata(components[name].Metadata))

			update.Status = "running"
			scorch.UpdateComponent(update)
			scorch.UpdatePipeline(update)

			if err := ExecuteComponent(ctx, options...); err != nil {
				update.Status = "failure"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)

				errors = multierror.Append(errors, fmt.Errorf("%s stopping component %s for experiment %s: %w", loopPrefix, name, exp, err))
			} else {
				update.Status = "success"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)
			}
		}

		return errors
	}

	cleanup := func() error {
		update.Stage = string(ACTIONCLEANUP)

		if len(exe.Cleanup) == 0 {
			update.CmpType = ""
			update.CmpName = ""
			update.Status = "success"
			scorch.UpdatePipeline(update)
			return nil
		}

		var errors error

		for _, name := range exe.Cleanup {
			typ := components[name].Type

			update.CmpType = typ
			update.CmpName = name
			update.Status = "start"

			scorch.UpdateComponent(update)

			options := append(opts, Name(name), Type(typ), Stage(ACTIONCLEANUP), Metadata(components[name].Metadata))

			update.Status = "running"
			scorch.UpdateComponent(update)
			scorch.UpdatePipeline(update)

			err := ExecuteComponent(ctx, options...)
			if err != nil {
				update.Status = "failure"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)

				errors = multierror.Append(errors, fmt.Errorf("%s cleaning up component %s for experiment %s: %w", loopPrefix, name, exp, err))
			} else {
				update.Status = "success"
				scorch.UpdateComponent(update)
				scorch.UpdatePipeline(update)
			}
		}

		return errors
	}

	if err := configure(); err != nil {
		errors := multierror.Append(nil, err)

		if err := cleanup(); err != nil {
			errors = multierror.Append(errors, err)
		}

		return errors
	}

	if err := start(); err != nil {
		errors := multierror.Append(nil, err)

		if err := stop(); err != nil {
			errors = multierror.Append(errors, err)
		}

		if err := cleanup(); err != nil {
			errors = multierror.Append(errors, err)
		}

		return errors
	}

	var errors error

	if exe.Loop != nil {
		update := scorch.ComponentUpdate{
			Exp:   exp,
			Loop:  options.Loop,
			Stage: string(ACTIONLOOP),
		}

		update.Status = "running"
		scorch.UpdatePipeline(update)

		for i := 0; i < exe.Loop.Count; i++ {
			opts := append(opts, CurrentLoop(options.Loop+1), LoopCount(i))

			if err := executor(ctx, components, exe.Loop, opts...); err != nil {
				errors = multierror.Append(errors, err)
				break
			}
		}

		if errors != nil {
			update.Status = "failure"
		} else {
			update.Status = "success"
		}

		scorch.UpdatePipeline(update)
	}

	if err := stop(); err != nil {
		errors = multierror.Append(errors, err)
	}

	if err := cleanup(); err != nil {
		errors = multierror.Append(errors, err)
	}

	return errors
}
