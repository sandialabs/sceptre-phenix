package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

func (us userScheduler) Schedule(spec ifaces.ExperimentSpec) error {
	err := us.shellOut(spec)
	if err != nil {
		return fmt.Errorf("running user scheduler: %w", err)
	}

	return nil
}

func (us userScheduler) shellOut(spec ifaces.ExperimentSpec) error {
	cmdName := "phenix-scheduler-" + us.options.Name

	if !shell.CommandExists(cmdName) {
		return fmt.Errorf(
			"external user scheduler %s does not exist in your path: %w",
			cmdName,
			ErrUserSchedulerNotFound,
		)
	}

	cluster, err := mm.GetClusterHosts(true)
	if err != nil {
		return fmt.Errorf("getting cluster hosts: %w", err)
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
		return fmt.Errorf("marshaling experiment spec to JSON: %w", err)
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

	stdOut, _, err := shell.ExecCommand(context.Background(), opts...)
	if err != nil {
		return fmt.Errorf("user scheduler %s command %s failed: %w", us.options.Name, cmdName, err)
	}

	if err := json.Unmarshal(stdOut, &exp); err != nil {
		return fmt.Errorf("unmarshaling experiment spec from JSON: %w", err)
	}

	spec.SetSchedule(exp.Spec.Schedules())

	return nil
}
