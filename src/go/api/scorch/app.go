package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util"
	"phenix/util/mm/mmcli"
	"phenix/util/shell"
	"phenix/version"
	"phenix/web/scorch"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/activeshadow/structs"
	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"
)

func init() {
	app.RegisterUserApp("scorch", func() app.App { return newScorch() })
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

	var (
		runID  = scorchexe.MustRunID(ctx)
		runDir = filepath.Join(exp.FilesDir(), "scorch", fmt.Sprintf("run-%d", runID))
		start  = time.Now().UTC()
	)

	if runID < 0 || runID >= len(this.md.Runs) {
		return fmt.Errorf("invalid Scorch run ID for experiment %s", exp.Metadata.Name)
	}

	if err := os.RemoveAll(runDir); err != nil {
		return fmt.Errorf("removing existing contents of run directory at %s: %w", runDir, err)
	}

	var (
		cmd  *exec.Cmd
		port int
	)

	if this.md.FilebeatEnabled(runID) {
		inputs, err := createFilebeatConfig(this.md, exp.Spec.ExperimentName(), runID, runDir, start)
		if err != nil {
			return fmt.Errorf("creating Filebeat config: %w", err)
		}

		if inputs > 0 {
			cmd, port, err = this.startFilebeat(ctx, runID, runDir)
			if err != nil {
				return fmt.Errorf("starting Filebeat: %w", err)
			}
		}
	}

	var (
		errors error
		run    = this.md.Runs[runID]
		opts   = []Option{Experiment(*exp), RunID(runID), StartTime(start.Format(time.RubyDate))}
	)

	for i := 0; i < run.Count; i++ {
		opts := append(opts, LoopCount(i))

		if err := executor(ctx, this.md.ComponentSpecs(), run, opts...); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("executing Scorch for run %d, count %d: %w", runID, i, err))
			break
		}
	}

	update := scorch.ComponentUpdate{
		Exp:   exp.Metadata.Name,
		Run:   runID,
		Loop:  0,
		Stage: string(ACTIONDONE),
	}

	update.Status = "running"
	scorch.UpdatePipeline(update)

	if cmd != nil {
		this.stopFilebeat(ctx, cmd, port)
	}

	if err := this.recordInfo(runID, runDir, exp.Metadata, start); err != nil {
		errors = multierror.Append(errors, err)
	}

	if _, err := os.Stat(runDir); err == nil {
		archive := filepath.Join(exp.FilesDir(), fmt.Sprintf("scorch-run-%d_%s.tgz", runID, start.Format(time.RFC3339)))

		if err := util.CreateArchive(runDir, archive); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("archiving data generated for run %d: %w", runID, err))
		}
	}

	update.Status = "success"
	scorch.UpdatePipeline(update)

	return errors
}

func (Scorch) Cleanup(context.Context, *types.Experiment) error {
	return nil
}

func (this Scorch) startFilebeat(ctx context.Context, runID int, runDir string) (*exec.Cmd, int, error) {
	var (
		cmd    *exec.Cmd
		port   int
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
			data = filepath.Join(runDir, "filebeat", "data")
			cfg  = filepath.Join(runDir, "filebeat", "filebeat.yml")
		)

		var err error
		port, err = util.GetFreePort("127.0.0.1")
		if err != nil {
			return nil, 0, fmt.Errorf("unable to determine free port for Filebeat httpprof: %w", err)
		}

		// We include the httpprof server so we can query for running harvesters
		// when stopping Filebeat below.
		httpprof := fmt.Sprintf("127.0.0.1:%d", port)
		cmd = exec.CommandContext(ctx, "filebeat", "-c", cfg, "--path.data", data, "--httpprof", httpprof)

		cmd.Stdin = nil
		cmd.Stdout = &stdOut
		cmd.Stderr = &stdErr

		if err := cmd.Start(); err != nil {
			return nil, 0, err
		}

		// Give Filebeat time to start up or die.
		time.Sleep(2 * time.Second)

		if !shell.ProcessExists(cmd.Process.Pid) {
			if err := cmd.Wait(); err != nil {
				return nil, 0, err
			}
		}
	}

	return cmd, port, nil
}

func (this Scorch) stopFilebeat(ctx context.Context, cmd *exec.Cmd, port int) {
	if cmd == nil {
		return
	}

	if !shell.ProcessExists(cmd.Process.Pid) {
		if err := cmd.Wait(); err != nil {
			log.Error("the Filebeat process terminated early (logs may be missing): %v", err)
		}

		return
	}

	// Sleeping for 7s here since we have Filebeat configured with a scan
	// frequency of 5s for inputs in the config file.
	time.Sleep(7 * time.Second)

	var (
		max  = time.NewTimer(5 * time.Minute)
		tick = time.NewTicker(5 * time.Second)

		metrics filebeatMetrics
	)

	defer max.Stop()
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			cmd.Process.Signal(os.Interrupt)
			return
		case <-max.C:
			log.Warn("max amount of time (%v) for Filebeat to harvest inputs reached", max)

			cmd.Process.Signal(os.Interrupt)
			cmd.Wait()

			return
		case <-tick.C:
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/debug/vars", port))
			if err != nil {
				log.Error("unable to get number of active harvesters from Filebeat: %v", err)

				cmd.Process.Signal(os.Interrupt)
				cmd.Wait()

				return
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Error("unable to get number of active harvesters from Filebeat: %v", err)

				cmd.Process.Signal(os.Interrupt)
				cmd.Wait()

				return
			}

			prev := metrics

			if err := json.Unmarshal(body, &metrics); err != nil {
				log.Error("unmarshaling Filebeat harvester metrics: %v", err)
				continue
			}

			// Reset the max timer if progress is being made.
			if metrics.Progress(prev) {
				// stop and drain the max timer before resetting it
				if !max.Stop() {
					<-max.C
				}

				max.Reset(1 * time.Minute)

				// Skip the check below so we don't kill Filebeat prematurely if
				// progress is being made.
				continue
			}

			// NOTE: This might cause Filebeat to be killed prematurely if the
			// number of started harvesters doesn't yet match the number of total
			// files generated that need to be harvested. However, I'm not sure it's
			// a good idea to assume started must equal the number of inputs defined
			// in the Scorch config since 1) there's no guarantee all of them will
			// actually be generated, and 2) the input paths defined in the Filebeat
			// config have wildcards in them for run loops and counts.
			if metrics.Done() {
				log.Info("Filebeat has completed harvesting inputs")

				cmd.Process.Signal(os.Interrupt)
				cmd.Wait()

				return
			}
		}
	}
}

func (this Scorch) recordInfo(runID int, runDir string, md store.ConfigMetadata, startTime time.Time) error {
	c := mmcli.NewCommand()
	c.Command = "version"

	mmVersion, err := mmcli.SingleResponse(mmcli.Run(c))
	if err != nil {
		return fmt.Errorf("getting minimega version: %w", err)
	}

	info := []string{
		"Experiment Name: %s",
		"Experiment Tags: %s",
		"Scorch Run Name: %s",
		"Start Time: %s",
		"End Time: %s",
		"Phenix Version: %s %s %s",
		"Minimega Version: %s",
	}

	body := fmt.Sprintf(
		strings.Join(info, "\n"),
		md.Name,
		md.Annotations["phenix.workflow/tags"],
		this.md.RunName(runID),
		startTime.Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		version.Commit, version.Tag, version.Date,
		mmVersion,
	)

	fileName := fmt.Sprintf("info-scorch-run-%d_%s.txt", runID, startTime.Format(time.RFC3339))

	if err := os.MkdirAll(runDir, 0755); err != nil {
		return fmt.Errorf("creating %s directory for scorch run %d: %w", runDir, runID, err)
	}

	if err := os.WriteFile(filepath.Join(runDir, fileName), []byte(body), 0644); err != nil {
		return fmt.Errorf("writing scorch information file (%s): %w", fileName, err)
	}

	return nil
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

	if update.Loop != 0 {
		update.CmpType = ""
		update.CmpName = ""
		update.Stage = string(ACTIONDONE)
		update.Status = "success"

		scorch.UpdatePipeline(update)
	}

	return errors
}
