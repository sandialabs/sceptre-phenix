package plog

import (
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"
)

// haveWarnedLoggingFormat is true after the first warning about a log line
// that is not JSON.
var haveWarnedLoggingFormat atomic.Bool //nolint:gochecknoglobals // state flag

// ProcessStderrLogs reads from a channel of bytes and logs them using plog.
// It expects JSON with 'level' and 'msg' keys.
// Non-JSON lines are logged as warnings.
// It logs the parts of a multi-part message as one record.
func ProcessStderrLogs(stderrChan <-chan []byte, logType LogType, attrs ...any) {
	joiner := NewPartJoiner(func(_ string, record map[string]any, msg string) {
		record["msg"] = msg
		logStderrRecord(logType, record, attrs)
	})

	timer := time.NewTimer(PartFlushInterval)
	timer.Stop()

	for {
		select {
		case logBytes, ok := <-stderrChan:
			if !ok {
				joiner.Flush()

				return
			}

			processStderrLine(joiner, logBytes, logType, attrs)

			if joiner.Pending() {
				timer.Reset(PartFlushInterval)
			}
		case <-timer.C:
			joiner.Flush()
		}
	}
}

func processStderrLine(
	joiner *PartJoiner[map[string]any], logBytes []byte, logType LogType, attrs []any,
) {
	parts := map[string]any{}

	err := json.Unmarshal(logBytes, &parts)
	if err != nil {
		joiner.Flush()

		if haveWarnedLoggingFormat.CompareAndSwap(false, true) {
			Warn(
				logType,
				"For best experience, modify process to log as json with 'level' and 'msg' keys",
				attrs...)
		}

		Error(logType, string(logBytes), attrs...)

		return
	}

	level, levelOK := parts["level"].(string)
	msg, msgOK := parts["msg"].(string)
	part, partOK := ParsePart(parts["part"], parts["parts"])

	if levelOK && msgOK && partOK {
		delete(parts, "part")
		delete(parts, "parts")
		delete(parts, "chunk_type")
		joiner.Add(level, part, msg, parts)

		return
	}

	joiner.Flush()

	if _, ok := parts["level"]; !ok {
		Warn(
			logType,
			"could not find level key in log",
			append([]any{"log", string(logBytes)}, attrs...)...)
		Info(logType, string(logBytes), attrs...)

		return
	}

	if _, ok := parts["msg"]; !ok {
		Warn(
			logType,
			"could not find msg key in log",
			append([]any{"log", string(logBytes)}, attrs...)...)
		Info(logType, string(logBytes), attrs...)

		return
	}

	logStderrRecord(logType, parts, attrs)
}

// logStderrRecord logs a decoded record that has 'level' and 'msg' keys. A
// "type" key with a log type that an app can use sets the log type. An app key
// that has the name of a phenix key gets the "proc_" prefix.
func logStderrRecord(logType LogType, parts map[string]any, attrs []any) {
	msg := parts["msg"]

	// If a traceback is present, append it to the message for better readability
	if tb, ok := parts["traceback"]; ok {
		if tbStr, ok := tb.(string); ok {
			msgStr, _ := msg.(string)
			msg = msgStr + "\n" + tbStr
		}

		delete(parts, "traceback")
	}

	levelStr := parts["level"]

	delete(parts, "msg")
	delete(parts, "level")

	if t, ok := appLogType(parts[logAttrType]); ok {
		logType = t

		delete(parts, logAttrType)
	}

	phenixKeys := map[string]bool{slog.TimeKey: true, logAttrType: true}

	for i := 0; i+1 < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok {
			phenixKeys[k] = true
		}
	}

	extras := attrs
	for k, v := range parts {
		if phenixKeys[k] {
			k = "proc_" + k
		}

		extras = append(extras, k, v)
	}

	level, _ := levelStr.(string)
	message, _ := msg.(string)
	Log(TextToLevel(level), logType, message, extras...)
}

// appLogType returns the log type that the "type" value v of an app record
// gives. An app cannot give the audit or the security log type.
func appLogType(v any) (LogType, bool) {
	s, _ := v.(string)

	switch t := LogType(s); t {
	case TypeSoh, TypeScorch, TypePhenixApp, TypeAction, TypeHTTP, TypeMinimega, TypeSystem:
		return t, true
	case TypeSecurity, TypeAudit:
		return "", false
	default:
		return "", false
	}
}
