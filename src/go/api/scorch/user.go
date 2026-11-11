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

	"phenix/api/scorch/scorchmd"
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

	setComponentMetadata(generic, u.options.Name, u.options.Meta)

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

// setComponentMetadata sets the metadata of each SCORCH component called name
// in the scenario apps of exp, an experiment decoded from JSON, to md. It
// changes only the first such component of each app.
func setComponentMetadata(exp map[string]any, name string, md scorchmd.ComponentMetadata) {
	spec, _ := exp["spec"].(map[string]any)
	scenario, _ := spec["scenario"].(map[string]any)
	apps, _ := scenario["apps"].([]any)

	for _, a := range apps {
		appMap, _ := a.(map[string]any)
		meta, _ := appMap["metadata"].(map[string]any)
		cmps, _ := meta["components"].([]any)

		for _, c := range cmps {
			if cmpMap, ok := c.(map[string]any); ok && cmpMap["name"] == name {
				cmpMap["metadata"] = md

				break
			}
		}
	}
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

	stdout := make(chan []byte)

	stderrChan := make(chan []byte)
	go processLogChannel(stderrChan, func(level, msg string) {
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
	})

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

	go func() {
		for output := range stdout {
			update.Output = output
			update.Output = append(update.Output, '\n')
			scorch.UpdateComponent(update)
		}
	}()

	stdoutBytes, _, err := shell.ExecCommand(ctx, opts...)
	if err != nil {
		return fmt.Errorf(
			"external user component %s (command %s) failed: %w",
			u.options.Type,
			cmd,
			err,
		)
	}

	if len(stdoutBytes) != 0 {
		plog.Info(plog.TypePhenixApp, string(stdoutBytes),
			"experiment", u.options.Exp.Spec.ExperimentName(),
			"app", appName,
			"component", u.options.Name,
			"stage", string(stage),
			"run", strconv.Itoa(u.options.Run),
			"loop", strconv.Itoa(u.options.Loop),
			"count", strconv.Itoa(u.options.Count),
		)
	}

	return nil
}

// processLogChannel reads from ch and calls logFn for each detected log entry.
// It buffers non-JSON lines for up to 10ms to reconstruct multi-line messages (like stack traces).
// It joins the parts of a multi-part JSON message into one entry.
func processLogChannel(ch <-chan []byte, logFn func(level, msg string)) {
	var (
		buf    bytes.Buffer
		timer  = time.NewTimer(time.Hour)
		joiner = plog.NewPartJoiner(func(level string, _ struct{}, msg string) { logFn(level, msg) })
	)
	timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(logFlushInterval)
	}

	flushText := func() {
		if buf.Len() == 0 {
			return
		}
		msg := buf.String()
		buf.Reset()

		logFn(textLevel(msg), msg)
	}

	type jsonLog struct {
		Level     string `json:"level"`
		Message   string `json:"msg"`
		Traceback string `json:"traceback"`
		Part      any    `json:"part"`
		Parts     any    `json:"parts"`
	}

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				joiner.Flush()
				flushText()
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
				if entry.Traceback != "" {
					entry.Message += "\n" + entry.Traceback
				}

				if part, ok := plog.ParsePart(entry.Part, entry.Parts); ok {
					flushText()
					joiner.Add(entry.Level, part, entry.Message, struct{}{})

					if joiner.Pending() {
						resetTimer()
					}

					continue
				}

				joiner.Flush()
				flushText()
				logFn(entry.Level, entry.Message)
			} else {
				joiner.Flush()

				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
				buf.Write(bytes.TrimRight(line, "\r\n"))

				resetTimer()
			}
		case <-timer.C:
			joiner.Flush()
			flushText()
		}
	}
}

// textLevel returns the level that the "| LEVEL |" marker in a text entry
// names, or INFO if the entry has no marker.
func textLevel(msg string) string {
	switch {
	case strings.Contains(msg, "| ERROR |") || strings.Contains(msg, "| ERR |"):
		return levelError
	case strings.Contains(msg, "| WARN |") || strings.Contains(msg, "| WARNING |"):
		return levelWarn
	case strings.Contains(msg, "| DEBUG |") || strings.Contains(msg, "| DBG |"):
		return levelDebug
	default:
		return levelInfo
	}
}
