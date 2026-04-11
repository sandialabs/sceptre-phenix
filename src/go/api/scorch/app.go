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
	"time"

	"github.com/activeshadow/structs"
	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"

	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util"
	"phenix/util/mm/mmcli"
	"phenix/util/plog"
	"phenix/util/shell"
	"phenix/version"
	"phenix/web/scorch"
)

const (
	statusRunning  = "running"
	statusSuccess  = "success"
	statusFailure  = "failure"
	statusUnstable = "unstable"

	filebeatStartupDelay = 2 * time.Second
	filebeatScanDelay    = 7 * time.Second
	filebeatMaxHarvest   = 5 * time.Minute
	filebeatHarvestTick  = 5 * time.Second
)

func init() { //nolint:gochecknoinits // app registration
	_ = app.RegisterUserApp("scorch", func() app.App { return newScorch() })
}

type Scorch struct {
	md scorchmd.ScorchMetadata

	options app.Options
}

func newScorch() *Scorch {
	return new(Scorch)
}

func (s *Scorch) Init(opts ...app.Option) error {
	s.options = app.NewOptions(opts...)

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

	err := mapstructure.Decode(app.Metadata(), &md)
	if err != nil {
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

func (s Scorch) PostStart(ctx context.Context, exp *types.Experiment) error {
	return nil
}

func (s *Scorch) Running(ctx context.Context, exp *types.Experiment) error {
	var err error

	if s.md, err = scorchmd.DecodeMetadata(exp); err != nil {
		return err
	}

	var (
		runID  = scorchexe.MustRunID(ctx)
		runDir = filepath.Join(exp.FilesDir(), "scorch", fmt.Sprintf("run-%d", runID))
		start  = time.Now().UTC()
	)

	if runID < 0 || runID >= len(s.md.Runs) {
		return fmt.Errorf("invalid Scorch run ID for experiment %s", exp.Metadata.Name)
	}

	if err := os.RemoveAll(runDir); err != nil {
		return fmt.Errorf("removing existing contents of run directory at %s: %w", runDir, err)
	}

	var (
		cmd  *exec.Cmd
		port int
	)

	if s.md.FilebeatEnabled(runID) {
		inputs, err := createFilebeatConfig(s.md, exp.Spec.ExperimentName(), runID, runDir, start)
		if err != nil {
			return fmt.Errorf("creating Filebeat config: %w", err)
		}

		if inputs > 0 {
			cmd, port, err = s.startFilebeat(ctx, runDir)
			if err != nil {
				return fmt.Errorf("starting Filebeat: %w", err)
			}
		}
	}

	var (
		errors error
		run    = s.md.Runs[runID]
		opts   = []Option{Experiment(*exp), RunID(runID), StartTime(start.Format(time.RubyDate))}
	)

	for i := range run.Count {
		loopOpts := append([]Option(nil), opts...)
		loopOpts = append(loopOpts, LoopCount(i))

		err := executor(ctx, s.md.ComponentSpecs(), run, loopOpts...)
		if err != nil {
			errors = multierror.Append(
				errors,
				fmt.Errorf("executing Scorch for run %d, count %d: %w", runID, i, err),
			)

			break
		}
	}

	update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
		Exp:   exp.Metadata.Name,
		Run:   runID,
		Loop:  0,
		Stage: string(ActionDone),
	}

	update.Status = statusRunning
	_ = scorch.UpdatePipeline(update)

	if cmd != nil {
		s.stopFilebeat(ctx, cmd, port)
	}

	if err := s.recordInfo(runID, runDir, exp.Metadata, start); err != nil {
		errors = multierror.Append(errors, err)
	}

	if _, err := os.Stat(runDir); err == nil {
		archive := filepath.Join(
			exp.FilesDir(),
			fmt.Sprintf("scorch-run-%d_%s.tgz", runID, start.Format("2006-01-02T15-04-05Z0700")),
		)

		err := util.CreateArchive(runDir, archive)
		if err != nil {
			errors = multierror.Append(
				errors,
				fmt.Errorf("archiving data generated for run %d: %w", runID, err),
			)
		}
	}

	update.Status = statusSuccess
	_ = scorch.UpdatePipeline(update)

	return errors
}

func (Scorch) Cleanup(context.Context, *types.Experiment) error {
	return nil
}

func (s Scorch) startFilebeat(
	ctx context.Context,
	runDir string,
) (*exec.Cmd, int, error) {
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

	if s.md.Filebeat.Enabled && shell.CommandExists("filebeat") {
		var (
			data = filepath.Join(runDir, "filebeat", "data")
			cfg  = filepath.Join(runDir, "filebeat", "filebeat.yml")
		)

		var err error

		port, err = util.GetFreePort("127.0.0.1")
		if err != nil {
			return nil, 0, fmt.Errorf(
				"unable to determine free port for Filebeat httpprof: %w",
				err,
			)
		}

		// We include the httpprof server so we can query for running harvesters
		// when stopping Filebeat below.
		httpprof := fmt.Sprintf("127.0.0.1:%d", port)
		cmd = exec.CommandContext(
			ctx,
			"filebeat",
			"-c",
			cfg,
			"--path.data",
			data,
			"--httpprof",
			httpprof,
		)

		cmd.Stdin = nil
		cmd.Stdout = &stdOut
		cmd.Stderr = &stdErr

		if err := cmd.Start(); err != nil {
			return nil, 0, err
		}

		// Give Filebeat time to start up or die.
		time.Sleep(filebeatStartupDelay)

		if !shell.ProcessExists(cmd.Process.Pid) {
			err := cmd.Wait()
			if err != nil {
				return nil, 0, err
			}
		}
	}

	return cmd, port, nil
}

//nolint:funlen // complex logic
func (s Scorch) stopFilebeat(ctx context.Context, cmd *exec.Cmd, port int) {
	if cmd == nil {
		return
	}

	if !shell.ProcessExists(cmd.Process.Pid) {
		err := cmd.Wait()
		if err != nil {
			plog.Error(
				plog.TypePhenixApp,
				"the Filebeat process terminated early (logs may be missing)",
				"err",
				err,
				"app",
				"scorch",
			)
		}

		return
	}

	// Sleeping for 7s here since we have Filebeat configured with a scan
	// frequency of 5s for inputs in the config file.
	time.Sleep(filebeatScanDelay)

	var (
		maxTimer = time.NewTimer(filebeatMaxHarvest)
		tick     = time.NewTicker(filebeatHarvestTick)

		metrics filebeatMetrics
	)

	defer maxTimer.Stop()
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(os.Interrupt)

			return
		case <-maxTimer.C:
			plog.Warn(
				plog.TypePhenixApp,
				"max amount of time for Filebeat to harvest inputs reached",
				"max",
				maxTimer,
				"app",
				"scorch",
			)

			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Wait()

			return
		case <-tick.C:
			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				fmt.Sprintf("http://localhost:%d/debug/vars", port),
				nil,
			)
			if err != nil {
				plog.Error(
					plog.TypePhenixApp,
					"unable to create request to get number of active harvesters from Filebeat",
					"err",
					err,
					"app",
					"scorch",
				)

				_ = cmd.Process.Signal(os.Interrupt)
				_ = cmd.Wait()

				return
			}
			resp, err := http.DefaultClient.Do(req) //nolint:gosec // SSRF via taint analysis
			if err != nil {
				plog.Error(
					plog.TypePhenixApp,
					"unable to get number of active harvesters from Filebeat",
					"err",
					err,
					"app",
					"scorch",
				)

				_ = cmd.Process.Signal(os.Interrupt)
				_ = cmd.Wait()

				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				plog.Error(
					plog.TypePhenixApp,
					"unable to get number of active harvesters from Filebeat",
					"err",
					err,
					"app",
					"scorch",
				)

				_ = cmd.Process.Signal(os.Interrupt)
				_ = cmd.Wait()

				return
			}

			prev := metrics

			if err := json.Unmarshal(body, &metrics); err != nil {
				plog.Error(
					plog.TypePhenixApp,
					"unmarshaling Filebeat harvester metrics",
					"err",
					err,
					"app",
					"scorch",
				)

				continue
			}

			// Reset the max timer if progress is being made.
			if metrics.Progress(prev) {
				// stop and drain the max timer before resetting it
				if !maxTimer.Stop() {
					<-maxTimer.C
				}

				maxTimer.Reset(1 * time.Minute)

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
				plog.Info(
					plog.TypePhenixApp,
					"Filebeat has completed harvesting inputs",
					"app",
					"scorch",
				)

				_ = cmd.Process.Signal(os.Interrupt)
				_ = cmd.Wait()

				return
			}
		}
	}
}

func (s Scorch) recordInfo(
	runID int,
	runDir string,
	md store.ConfigMetadata,
	startTime time.Time,
) error {
	c := mmcli.NewCommand()
	c.Command = "version"

	mmVersion, err := mmcli.SingleResponse(mmcli.Run(c))
	if err != nil {
		return fmt.Errorf("getting minimega version: %w", err)
	}

	info := map[string]any{
		"experiment": map[string]string{
			"name": md.Name,
			"tags": md.Annotations["phenix.workflow/tags"],
		},
		"run": map[string]any{
			"name":  s.md.RunName(runID),
			"index": runID,
		},
		"start": startTime.Format(time.RFC3339),
		"end":   time.Now().UTC().Format(time.RFC3339),
		"phenix_version": map[string]string{
			"commit": version.Commit,
			"tag":    version.Tag,
			"date":   version.Date,
		},
		"minimega_version": mmVersion,
	}

	fileName := fmt.Sprintf(
		"%s-scorch-run-%d-%s.json",
		md.Name,
		runID,
		startTime.Format("2006-01-02T15-04-05Z0700"),
	)

	if err := os.MkdirAll(runDir, 0o750); err != nil {
		return fmt.Errorf("creating %s directory for scorch run %d: %w", runDir, runID, err)
	}

	body, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling scorch run info: %w", err)
	}

	if err := os.WriteFile(filepath.Join(runDir, fileName), body, 0o600); err != nil {
		return fmt.Errorf("writing scorch information file (%s): %w", fileName, err)
	}

	return nil
}

//nolint:funlen,maintidx // complex logic
func executor(
	ctx context.Context,
	components scorchmd.ComponentSpecMap,
	exe *scorchmd.Loop,
	opts ...Option,
) error {
	options := NewOptions(opts...)

	loopReplacements, err := scorchmd.ResolveReplacements(exe.Replace)
	if err != nil {
		return fmt.Errorf("resolving replacements: %w", err)
	}

	mergedReplacements := scorchmd.MergeReplacements(options.Replacements, loopReplacements)
	plog.Info(
		plog.TypePhenixApp,
		"Resolved replacements for Scorch loop",
		"run_id",
		options.Run,
		"loop_idx",
		options.Loop,
		"replacements",
		mergedReplacements,
		"app",
		"scorch",
	)

	options.Replacements = mergedReplacements
	opts = append(opts, Replacements(mergedReplacements))

	var (
		exp        = options.Exp.Spec.ExperimentName()
		loopPrefix = fmt.Sprintf(
			"[RUN: %d - LOOP: %d - COUNT: %d]",
			options.Run,
			options.Loop,
			options.Count,
		)
	)

	logger := plog.LoggerFromContext(ctx, plog.TypeScorch)

	if options.Loop == 0 {
		scorch.DeletePipeline(exp, options.Run, -1, true)
	}

	update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
		Exp:   exp,
		Run:   options.Run,
		Loop:  options.Loop,
		Count: options.Count,
	}

	logger.Info("starting scorch", "run", loopPrefix)

	runStage := func(stage Action, names []string, failFast bool) error {
		update.Stage = string(stage)

		if len(names) == 0 {
			update.CmpType = ""
			update.CmpName = ""
			update.Status = statusSuccess
			_ = scorch.UpdatePipeline(update)

			return nil
		}

		var errors error

		logger.Info("running scorch stage", "stage", stage)

		for _, name := range names {
			typ := components[name].Type

			update.CmpType = typ
			update.CmpName = name
			update.Status = "start"

			scorch.UpdateComponent(update)

			meta := scorchmd.ApplyReplacements(components[name].Metadata, options.Replacements)
			cmpOpts := opts
			cmpOpts = append(cmpOpts, Name(name), Type(typ), Stage(stage), Metadata(meta))

			status := statusRunning

			if components[name].Background && failFast {
				cmpOpts = append(cmpOpts, Background())
				status = "background"
			}

			update.Status = status
			scorch.UpdateComponent(update)
			_ = scorch.UpdatePipeline(update)

			logger.Debug("running scorch stage component", "stage", stage, "component", name)

			err := ExecuteComponent(ctx, cmpOpts...)
			if err != nil {
				update.Status = statusFailure
				scorch.UpdateComponent(update)
				_ = scorch.UpdatePipeline(update)

				logger.Error(
					"[✗] failed scorch stage component",
					"stage",
					stage,
					"component",
					name,
					"err",
					err,
				)

				err = fmt.Errorf(
					"%s %s component %s for experiment %s: %w",
					loopPrefix,
					stage,
					name,
					exp,
					err,
				)

				if failFast {
					return err
				}

				errors = multierror.Append(errors, err)
			} else if !components[name].Background || !failFast {
				update.Status = statusSuccess
				scorch.UpdateComponent(update)
				_ = scorch.UpdatePipeline(update)

				logger.Debug(
					"[✓] completed scorch stage component",
					"stage",
					stage,
					"component",
					name,
				)
			}
		}

		return errors
	}

	configure := func() error { return runStage(ActionConfigure, exe.Configure, true) }
	start := func() error { return runStage(ActionStart, exe.Start, true) }
	stop := func() error { return runStage(ActionStop, exe.Stop, false) }
	cleanup := func() error { return runStage(ActionCleanup, exe.Cleanup, false) }

	if err := configure(); err != nil {
		errors := multierror.Append(nil, err)

		err := cleanup()
		if err != nil {
			errors = multierror.Append(errors, err)
		}

		return errors
	}

	if err := start(); err != nil {
		errors := multierror.Append(nil, err)

		err := stop()
		if err != nil {
			errors = multierror.Append(errors, err)
		}

		err = cleanup()
		if err != nil {
			errors = multierror.Append(errors, err)
		}

		return errors
	}

	var errors error

	if exe.Loop != nil {
		update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
			Exp:   exp,
			Loop:  options.Loop,
			Stage: string(ActionLoop),
		}

		update.Status = statusRunning
		_ = scorch.UpdatePipeline(update)

		for i := range exe.Loop.Count {
			loopOpts := append([]Option(nil), opts...)
			loopOpts = append(loopOpts, CurrentLoop(options.Loop+1), LoopCount(i))

			err := executor(ctx, components, exe.Loop, loopOpts...)
			if err != nil {
				errors = multierror.Append(errors, err)

				break
			}
		}

		if errors != nil {
			update.Status = statusFailure
		} else {
			update.Status = statusSuccess
		}

		_ = scorch.UpdatePipeline(update)
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
		update.Stage = string(ActionDone)
		update.Status = statusSuccess

		_ = scorch.UpdatePipeline(update)
	}

	return errors
}
