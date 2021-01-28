package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"phenix/internal/common"
	"phenix/internal/mm"
	"phenix/scheduler"
	"phenix/types"
	"phenix/util"
	"phenix/util/shell"
)

const (
	EXIT_SCHEDULE int = 101
)

var ErrUserAppNotFound = errors.New("user app not found")

type UserApp struct {
	options Options
}

func (this *UserApp) Init(opts ...Option) error {
	this.options = NewOptions(opts...)

	return nil
}

func (this UserApp) Name() string {
	return this.options.Name
}

func (this UserApp) Configure(ctx context.Context, exp *types.Experiment) error {
	if err := this.shellOut(ctx, ACTIONCONFIG, exp); err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (this UserApp) PreStart(ctx context.Context, exp *types.Experiment) error {
	if err := this.shellOut(ctx, ACTIONPRESTART, exp); err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (this UserApp) PostStart(ctx context.Context, exp *types.Experiment) error {
	if err := this.shellOut(ctx, ACTIONPOSTSTART, exp); err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (this UserApp) Running(ctx context.Context, exp *types.Experiment) error {
	if err := this.shellOut(ctx, ACTIONRUNNING, exp); err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (this UserApp) Cleanup(ctx context.Context, exp *types.Experiment) error {
	if err := this.shellOut(ctx, ACTIONCLEANUP, exp); err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (this UserApp) shellOut(ctx context.Context, action Action, exp *types.Experiment) error {
	cmdName := "phenix-app-" + this.options.Name

	if !shell.CommandExists(cmdName) {
		return fmt.Errorf("external user app %s does not exist in your path: %w", cmdName, ErrUserAppNotFound)
	}

	cluster, err := mm.GetClusterHosts(true)
	if err != nil {
		return fmt.Errorf("getting cluster hosts: %w", err)
	}

	exp.Hosts = cluster

	data, err := json.Marshal(exp)
	if err != nil {
		return fmt.Errorf("marshaling experiment to JSON: %w", err)
	}

	var logFile string

	if dir := filepath.Dir(common.LogFile); dir == "/var/log/phenix" {
		logFile = dir + "/phenix-apps.log"
	} else {
		logFile = dir + "/.phenix-apps.log"
	}

	opts := []shell.Option{
		shell.Command(cmdName),
		shell.Args(string(action)),
		shell.Stdin(data),
		shell.Env(
			"PHENIX_DIR="+common.PhenixBase,
			"PHENIX_LOG_LEVEL="+util.GetEnv("PHENIX_LOG_LEVEL", "DEBUG"),
			"PHENIX_LOG_FILE="+util.GetEnv("PHENIX_LOG_FILE", logFile),
			"PHENIX_DRYRUN="+strconv.FormatBool(this.options.DryRun),
		),
	}

	stdOut, stdErr, err := shell.ExecCommand(ctx, opts...)
	if err != nil {
		var exitErr *exec.ExitError

		// The user app returned a non-zero exit status, so see if it matches any of
		// our special exit codes and handle accordingly.
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case EXIT_SCHEDULE:
				sched := strings.TrimSpace(string(stdOut))

				if err := scheduler.Schedule(sched, exp.Spec); err != nil {
					return fmt.Errorf("scheduling experiment with %s: %w", sched, err)
				}

				return this.shellOut(ctx, action, exp)
			}
		}

		// FIXME: improve on this
		fmt.Printf(string(stdErr))

		return fmt.Errorf("user app %s command %s failed: %w", this.options.Name, cmdName, err)
	}

	// If we make it to this point, then the user app exited with a 0 exit code.
	// If the user app didn't make any modifications, then we don't require it to
	// output an experiment config. So, if there's nothing on STDOUT then just
	// return immediately without error.
	if len(stdOut) == 0 {
		return nil
	}

	result := types.NewExperiment(exp.Metadata)

	if err := json.Unmarshal(stdOut, &result); err != nil {
		return fmt.Errorf("unmarshaling experiment from JSON: %w", err)
	}

	switch action {
	case ACTIONCONFIG, ACTIONPRESTART:
		exp.SetSpec(result.Spec)
	case ACTIONPOSTSTART, ACTIONRUNNING:
		if metadata, ok := result.Status.AppStatus()[this.options.Name]; ok {
			exp.Status.SetAppStatus(this.options.Name, metadata)
		}
	case ACTIONCLEANUP:
		exp.SetSpec(result.Spec)

		if metadata, ok := result.Status.AppStatus()[this.options.Name]; ok {
			exp.Status.SetAppStatus(this.options.Name, metadata)
		}
	}

	return nil
}
