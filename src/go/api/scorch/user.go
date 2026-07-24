package scorch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"phenix/util"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/util/shell"
	"phenix/web/scorch"
)

var ErrUserComponentNotFound = errors.New("user component not found")

const (
	levelInfo  = "INFO"
	levelWarn  = "WARN"
	levelError = "ERROR"
	levelDebug = "DEBUG"

	logFlushInterval = 10 * time.Millisecond

	// uiUpdateBuffer is how many component output updates can be queued for
	// the scorch UI modal before further updates are dropped instead of
	// blocking component execution.
	uiUpdateBuffer = 256
)

type UserComponent struct {
	options Options
}

func (u *UserComponent) Init(opts ...Option) error {
	u.options = NewOptions(opts...)

	return nil
}

func (u UserComponent) Type() string {
	return u.options.Type
}

func (u UserComponent) Configure(ctx context.Context) error {
	if u.options.Background {
		ctx = background(ctx, ActionConfigure, u.options)
	}

	return u.shellOut(ctx, ActionConfigure)
}

func (u UserComponent) Start(ctx context.Context) error {
	if u.options.Background {
		ctx = background(ctx, ActionStart, u.options)
	}

	return u.shellOut(ctx, ActionStart)
}

func (u UserComponent) Stop(ctx context.Context) error {
	handleBackgrounded(ActionStop, u.options)

	return u.shellOut(ctx, ActionStop)
}

func (u UserComponent) Cleanup(ctx context.Context) error {
	handleBackgrounded(ActionCleanup, u.options)

	return u.shellOut(ctx, ActionCleanup)
}

func (u UserComponent) shellOut(ctx context.Context, stage Action) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	cmd := "phenix-scorch-component-" + u.options.Type

	if !shell.CommandExists(cmd) {
		return fmt.Errorf(
			"external user component %s does not exist in your path: %w",
			cmd,
			ErrUserComponentNotFound,
		)
	}

	// Need to unmarshal the experiment, apply replacements, and then marshal before sending to component
	blob, err := json.Marshal(u.options.Exp)
	if err != nil {
		return fmt.Errorf("marshaling experiment: %w", err)
	}

	var generic map[string]any
	if err = json.Unmarshal(blob, &generic); err != nil {
		return fmt.Errorf("unmarshaling experiment to generic map: %w", err)
	}

	if spec, ok := generic["spec"].(map[string]any); ok {
		if scenario, ok2 := spec["scenario"].(map[string]any); ok2 {
			if apps, ok3 := scenario["apps"].([]any); ok3 {
				for _, a := range apps {
					appMap, ok4 := a.(map[string]any)
					if !ok4 {
						continue
					}

					meta, ok5 := appMap["metadata"].(map[string]any)
					if !ok5 {
						continue
					}

					cmps, ok6 := meta["components"].([]any)
					if !ok6 {
						continue
					}

					for _, comp := range cmps {
						cmpMap, ok7 := comp.(map[string]any)
						if !ok7 {
							continue
						}

						if name, ok8 := cmpMap["name"].(string); ok8 && name == u.options.Name {
							cmpMap["metadata"] = u.options.Meta

							break
						}
					}
				}
			}
		}
	}

	data, err := json.Marshal(generic)
	if err != nil {
		return fmt.Errorf("marshaling experiment metadata to JSON: %w", err)
	}

	// TODO: consider letting the child process send a signal indicating it wants
	// to run in the background instead of having to configure it in the scenario.

	if u.options.Background {
		go func() { _ = u.run(ctx, stage, cmd, data) }()

		return nil
	}

	return u.run(ctx, stage, cmd, data)
}

func (u UserComponent) run(ctx context.Context, stage Action, cmd string, data []byte) error {
	update := scorch.ComponentUpdate{ //nolint:exhaustruct // partial update
		Exp:     u.options.Exp.Spec.ExperimentName(),
		CmpName: u.options.Name,
		CmpType: u.options.Type,
		Run:     u.options.Run,
		Loop:    u.options.Loop,
		Count:   u.options.Count,
		Stage:   string(stage),
		Status:  statusRunning,
	}

	sendUI, stopUI := startUIForwarder(update)

	stdout := make(chan []byte)

	stderrChan := make(chan []byte)

	logDone := make(chan struct{})

	go func() {
		defer close(logDone)

		processLogChannel(stderrChan, u.componentLogger(stage, sendUI))
	}()

	opts := []shell.Option{
		shell.Command(cmd),
		shell.Args(
			string(stage),
			u.options.Name,
			strconv.Itoa(u.options.Run),
			strconv.Itoa(u.options.Loop),
			strconv.Itoa(u.options.Count),
		),
		shell.Stdin(data),
		shell.StreamStdout(stdout),
		shell.Env(
			"PHENIX_DIR="+common.PhenixBase,
			"PHENIX_FILES_DIR="+u.options.Exp.FilesDir(),
			"PHENIX_LOG_LEVEL="+util.GetEnv("PHENIX_LOG_LEVEL", "DEBUG"),
			"PHENIX_LOG_FILE=stderr",
			"PHENIX_DRYRUN="+strconv.FormatBool(u.options.Exp.DryRun()),
			"PHENIX_SCORCH_STARTTIME="+u.options.StartTime,
		),
		shell.StreamStderr(stderrChan),
	}

	var (
		stdoutBuf  bytes.Buffer
		stdoutDone = make(chan struct{})
	)

	go func() {
		defer close(stdoutDone)

		for output := range stdout {
			output = append(output, '\n')
			stdoutBuf.Write(output)
			sendUI(output)
		}
	}()

	_, _, err := shell.ExecCommand(ctx, opts...)

	// ExecCommand closes both stream channels on every return path, so these
	// joins are prompt. The UI forwarder is stopped only after both senders
	// have exited.
	<-stdoutDone
	<-logDone
	stopUI()

	if err != nil {
		return fmt.Errorf(
			"external user component %s (command %s) failed: %w",
			u.options.Type,
			cmd,
			err,
		)
	}

	// Anything a component writes directly to stdout (log messages go to
	// stderr as JSON) is captured here so it still lands in the phenix log.
	if stdoutBuf.Len() != 0 {
		plog.Info(plog.TypePhenixApp, strings.TrimRight(stdoutBuf.String(), "\n"),
			"experiment", u.options.Exp.Spec.ExperimentName(),
			"app", "scorch",
			"component", u.options.Name,
			"stage", string(stage),
			"run", strconv.Itoa(u.options.Run),
			"loop", strconv.Itoa(u.options.Loop),
			"count", strconv.Itoa(u.options.Count),
		)
	}

	return nil
}

// startUIForwarder returns a send function that streams component output to
// the scorch UI modal without ever blocking the caller: updates flow through
// a buffered channel drained by a separate goroutine and are dropped once the
// buffer fills. This keeps a stalled UI websocket client from back-pressuring
// the child process's stdout/stderr pipes and hanging the component run. The
// stop function must only be called once both senders have exited; it drains
// the remaining backlog (bounded by the modal websocket write deadline) so
// every output update is delivered before the caller posts the component's
// terminal status.
func startUIForwarder(update scorch.ComponentUpdate) (func([]byte), func()) {
	updates := make(chan []byte, uiUpdateBuffer)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for output := range updates {
			outUpdate := update
			outUpdate.Output = output
			scorch.UpdateComponent(outUpdate)
		}
	}()

	send := func(output []byte) {
		select {
		case updates <- output:
		default: // drop rather than block component execution
		}
	}

	stop := func() {
		close(updates)
		<-done
	}

	return send, stop
}

// componentLogger returns a processLogChannel callback that writes component
// log messages to the phenix log and streams them to the scorch UI modal.
func (u UserComponent) componentLogger(stage Action, sendUI func([]byte)) func(level, msg string) {
	return func(level, msg string) {
		kv := []any{
			"component", u.options.Name,
			"stage", stage,
			"exp", u.options.Exp.Spec.ExperimentName(),
		}

		switch level {
		case levelError, "ERR":
			plog.Error(plog.TypeScorch, msg, kv...)
		case levelWarn, "WARNING":
			plog.Warn(plog.TypeScorch, msg, kv...)
		case levelDebug, "DBG":
			plog.Debug(plog.TypeScorch, msg, kv...)
		default:
			plog.Info(plog.TypeScorch, msg, kv...)
		}

		sendUI(fmt.Appendf(nil, "[%s] %s\n", level, msg))
	}
}

// processLogChannel reads from ch and calls logFn for each detected log entry.
// It buffers non-JSON lines for up to 10ms to reconstruct multi-line messages (like stack traces).
func processLogChannel(ch <-chan []byte, logFn func(level, msg string)) {
	var (
		buf   bytes.Buffer
		timer = time.NewTimer(time.Hour)
	)
	timer.Stop()

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		msg := buf.String()
		buf.Reset()

		level := levelInfo
		switch {
		case strings.Contains(msg, "| ERROR |") || strings.Contains(msg, "| ERR |"):
			level = levelError
		case strings.Contains(msg, "| WARN |") || strings.Contains(msg, "| WARNING |"):
			level = levelWarn
		case strings.Contains(msg, "| DEBUG |") || strings.Contains(msg, "| DBG |"):
			level = levelDebug
		}

		logFn(level, msg)
	}

	type jsonLog struct {
		Level     string `json:"level"`
		Message   string `json:"msg"`
		Traceback string `json:"traceback"`
	}

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				flush()
				return
			}

			var entry jsonLog
			isJSON := false

			if len(line) > 0 && line[0] == '{' {
				err := json.Unmarshal(
					line,
					&entry,
				)
				if err == nil &&
					entry.Level != "" {
					isJSON = true
				}
			}

			if isJSON {
				flush()

				if entry.Traceback != "" {
					entry.Message += "\n" + entry.Traceback
				}

				logFn(entry.Level, entry.Message)
			} else {
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
				buf.Write(bytes.TrimRight(line, "\r\n"))

				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(logFlushInterval)
			}
		case <-timer.C:
			flush()
		}
	}
}
