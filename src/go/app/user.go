package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"phenix/scheduler"
	"phenix/types"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/shell"
)

const (
	ExitSchedule int = 101
)

var (
	UserAppPrefix      = "phenix-app-" //nolint:gochecknoglobals // global constant
	ErrUserAppNotFound = errors.New("user app not found")
)

type UserApp struct {
	options Options
}

func (u *UserApp) Init(opts ...Option) error {
	u.options = NewOptions(opts...)

	return nil
}

func (u UserApp) Name() string {
	return u.options.Name
}

func (u UserApp) Configure(ctx context.Context, exp *types.Experiment) error {
	err := u.shellOut(ctx, ActionConfigure, exp)
	if err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (u UserApp) PreStart(ctx context.Context, exp *types.Experiment) error {
	err := u.shellOut(ctx, ActionPreStart, exp)
	if err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (u UserApp) PostStart(ctx context.Context, exp *types.Experiment) error {
	err := u.shellOut(ctx, ActionPostStart, exp)
	if err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (u UserApp) Running(ctx context.Context, exp *types.Experiment) error {
	err := u.shellOut(ctx, ActionRunning, exp)
	if err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (u UserApp) Cleanup(ctx context.Context, exp *types.Experiment) error {
	err := u.shellOut(ctx, ActionCleanup, exp)
	if err != nil {
		return fmt.Errorf("running user app: %w", err)
	}

	return nil
}

func (u UserApp) shellOut(ctx context.Context, action Action, exp *types.Experiment) error {
	cmdName := UserAppPrefix + u.options.Name

	if !shell.CommandExists(cmdName) {
		return fmt.Errorf(
			"external user app %s does not exist in your path: %w",
			cmdName,
			ErrUserAppNotFound,
		)
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

	stderrChan := make(chan []byte)
	go plog.ProcessStderrLogs(
		stderrChan,
		plog.TypePhenixApp,
		"app",
		u.options.Name,
		"action",
		action,
		"exp",
		exp.Metadata.Name,
	)

	opts := []shell.Option{
		shell.Command(cmdName),
		shell.Args(string(action)),
		shell.Stdin(data),
		shell.SplitBytes(),
		shell.Env(
			"PHENIX_DIR="+common.PhenixBase,
			"PHENIX_FILES_DIR="+exp.FilesDir(),
			"PHENIX_LOG_LEVEL="+util.GetEnv("PHENIX_LOG_LEVEL", "DEBUG"),
			"PHENIX_LOG_FILE=stderr",
			"PHENIX_DRYRUN="+strconv.FormatBool(u.options.DryRun),
			"PHENIX_STORE_ENDPOINT="+common.StoreEndpoint,
		),
		shell.StreamStderr(stderrChan),
	}

	stdOut, _, err := shell.ExecCommand(ctx, opts...)
	if err != nil {
		var exitErr *exec.ExitError

		// The user app returned a non-zero exit status, so see if it matches any of
		// our special exit codes and handle accordingly.
		if errors.As(err, &exitErr) && exitErr.ExitCode() == ExitSchedule {
			sched := strings.TrimSpace(string(stdOut))

			err := scheduler.Schedule(sched, exp.Spec)
			if err != nil {
				return fmt.Errorf("scheduling experiment with %s: %w", sched, err)
			}

			return u.shellOut(ctx, action, exp)
		}

		return fmt.Errorf("user app %s command %s failed: %w", u.options.Name, cmdName, err)
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
	case ActionConfigure, ActionPreStart:
		exp.SetSpec(result.Spec)
	case ActionPostStart, ActionRunning:
		if metadata, ok := result.Status.AppStatus()[u.options.Name]; ok {
			exp.Status.SetAppStatus(u.options.Name, metadata)
		}
	case ActionCleanup:
		exp.SetSpec(result.Spec)

		if metadata, ok := result.Status.AppStatus()[u.options.Name]; ok {
			exp.Status.SetAppStatus(u.options.Name, metadata)
		}
	}

	return nil
}
