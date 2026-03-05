package plog

import (
	"encoding/json"
)

var haveWarnedLoggingFormat = false //nolint:gochecknoglobals // state flag

// ProcessStderrLogs reads from a channel of bytes and logs them using plog.
// It expects JSON with 'level' and 'msg' keys.
// Non-JSON lines are logged as warnings.
func ProcessStderrLogs(stderrChan <-chan []byte, logType LogType, attrs ...any) {
	for logBytes := range stderrChan {
		parts := map[string]any{}

		err := json.Unmarshal(logBytes, &parts)
		if err != nil {
			if !haveWarnedLoggingFormat {
				Warn(
					logType,
					"For best experience, modify process to log as json with 'level' and 'msg' keys",
					attrs...)

				haveWarnedLoggingFormat = true
			}

			Error(logType, string(logBytes), attrs...)
		} else {
			levelStr, ok := parts["level"]
			if !ok {
				Warn(
					logType,
					"could not find level key in log",
					append([]any{"log", string(logBytes)}, attrs...)...)
				Info(logType, string(logBytes), attrs...)

				continue
			}

			msg, ok := parts["msg"]
			if !ok {
				Warn(
					logType,
					"could not find msg key in log",
					append([]any{"log", string(logBytes)}, attrs...)...)
				Info(logType, string(logBytes), attrs...)

				continue
			}

			// prevent time interfering with plog
			if t, ok := parts["time"]; ok {
				parts["proc_time"] = t
				delete(parts, "time")
			}

			// If a traceback is present, append it to the message for better readability
			if tb, ok := parts["traceback"]; ok {
				if tbStr, ok := tb.(string); ok {
					msgStr, _ := msg.(string)
					msg = msgStr + "\n" + tbStr
				}

				delete(parts, "traceback")
			}

			delete(parts, "msg")
			delete(parts, "level")

			extras := attrs
			for k, v := range parts {
				extras = append(extras, k, v)
			}

			level, _ := levelStr.(string)
			message, _ := msg.(string)
			Log(TextToLevel(level), logType, message, extras...)
		}
	}
}
