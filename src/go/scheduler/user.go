package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	ifaces "phenix/types/interfaces"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/shell"
)

var ErrUserSchedulerNotFound = errors.New("user scheduler not found")

type userScheduler struct {
	options Options
}

func (us *userScheduler) Init(opts ...Option) error {
	us.options = NewOptions(opts...)

	return nil
}

func (us userScheduler) Name() string {
	return us.options.Name
}

func (us userScheduler) Schedule(ctx context.Context, spec ifaces.ExperimentSpec) ([]string, error) {
	warnings, err := us.shellOut(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("running user scheduler: %w", err)
	}

	return warnings, nil
}

// shellOut runs the user scheduler on spec and the cluster hosts, takes the
// schedules it answers, and returns the warnings it answers in a top-level
// warnings array; it ignores the rest of what it answers. ctx bounds the
// command.
func (us userScheduler) shellOut(ctx context.Context, spec ifaces.ExperimentSpec) ([]string, error) {
	cmdName := "phenix-scheduler-" + us.options.Name

	if !shell.CommandExists(cmdName) {
		return nil, fmt.Errorf(
			"external user scheduler %s does not exist in your path: %w",
			cmdName,
			ErrUserSchedulerNotFound,
		)
	}

	cluster, err := schedulableHosts(ctx, true)
	if err != nil {
		return nil, err
	}

	exp := struct {
		Spec  ifaces.ExperimentSpec `json:"spec"`
		Hosts mm.Hosts              `json:"hosts"`
	}{
		Spec:  spec,
		Hosts: cluster,
	}

	data, err := json.Marshal(exp)
	if err != nil {
		return nil, fmt.Errorf("marshaling experiment spec to JSON: %w", err)
	}

	stderrChan := make(chan []byte)
	go plog.ProcessStderrLogs(
		stderrChan,
		plog.TypeSystem,
		"scheduler",
		us.options.Name,
		"exp",
		spec.ExperimentName(),
	)

	opts := []shell.Option{
		shell.Command(cmdName),
		shell.Stdin(data),
		shell.Env(
			"PHENIX_LOG_LEVEL=DEBUG",
			"PHENIX_LOG_FILE=stderr",
			"PHENIX_DIR="+common.PhenixBase,
		),
		shell.StreamStderr(stderrChan),
	}

	stdOut, _, err := shell.ExecCommand(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("user scheduler %s command %s failed: %w", us.options.Name, cmdName, err)
	}

	// decoded apart from spec, as decoding into exp would also change the rest of spec in place
	var answer struct {
		Spec struct {
			Schedules map[string]string `json:"schedules"`
		} `json:"spec"`
		Warnings []string `json:"warnings"`
	}

	if err := json.Unmarshal(stdOut, &answer); err != nil {
		return nil, fmt.Errorf("unmarshaling experiment spec from JSON: %w", err)
	}

	maps.Copy(spec.Schedules(), answer.Spec.Schedules)

	return answer.Warnings, nil
}
