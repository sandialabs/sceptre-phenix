package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/hpcloud/tail"

	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

const (
	DefaultLogDuration = 30 * time.Second
	MMLogParts         = 4
	mmLogPartTime      = 1
	mmLogPartLevel     = 2
	mmLogPartLog       = 3
)

// GetLogs handles GET requests for /logs?start=<start>&end=<end>.
func GetLogs(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetLogs")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		start   = r.URL.Query().Get("start")
		end     = r.URL.Query().Get("end")
	)

	if !role.Allowed("logs", "get") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting logs not allowed",
			"user",
			user,
		)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		plog.Warn(plog.TypeSystem, "Invalid start time provided", "start", start, "error", err)

		return weberror.New(http.StatusBadRequest, "Missing or invalid start time. Expected RFC3339 format: "+err.Error())
	}

	endTime := time.Now().Add(DefaultLogDuration)
	if end != "" {
		endTime, err = time.Parse(time.RFC3339, end)
		if err != nil {
			plog.Warn(plog.TypeSystem, "Invalid start time provided", "start", start, "error", err)

			return weberror.New(http.StatusBadRequest, "Invalid end time. Expected RFC3339 format: "+err.Error())
		}
	}

	logs, err := plog.GetLogs(startTime, endTime)
	if err != nil {
		plog.Error(plog.TypeSystem, "Error getting logs in handler", "error", err)

		return weberror.New(http.StatusInternalServerError, "error with fetching logs")
	}

	w.Header().Set("Content-Type", "application/json")

	if logs == nil {
		// marshal an empty list, not null, when no logs match
		logs = []plog.LogEntry{}
	}

	body, err := json.Marshal(logs)
	if err != nil {
		plog.Error(plog.TypeSystem, "error marshaling loglist", "error", err)

		return weberror.New(http.StatusInternalServerError, "error with converting logs to protobuf")
	}

	_, _ = w.Write(body)

	return nil
}

var mmLogRegex = regexp.MustCompile(
	`\A(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2})\s* (DEBUG|INFO|WARN|ERROR|FATAL) .*?: (.*)\z`,
)

func mmLogLevelConversion(l string) slog.Level {
	switch l {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "FATAL", "ERROR":
		return slog.LevelError
	default:
		plog.Warn(plog.TypeSystem, "found unknown level in mm log file", "level", l)

		return slog.LevelError
	}
}

// SyncMinimegaLogs tails mm log file and writes back as plog entries.
func SyncMinimegaLogs(ctx context.Context, minimega string) {
	if minimega == "" {
		return
	}

	logs := make(chan string)

	tailConfig := tail.Config{ //nolint:exhaustruct // partial initialization
		Follow:   true,
		ReOpen:   true,
		Poll:     true,
		Location: &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
	}

	mmLogs, err := tail.TailFile(minimega, tailConfig)
	if err != nil {
		panic("setting up tail for minimega logs: " + err.Error())
	}

	go func() {
		// drains the tail until it stops, so stopping it never blocks
		for l := range mmLogs.Lines {
			select {
			case logs <- l.Text:
			case <-ctx.Done():
			}
		}
	}()

	// used to detect multi-line minimega logs
	var body map[string]string

	for {
		select {
		case <-ctx.Done():
			mmLogs.Kill(nil)

			return
		case log := <-logs:
			if log == "" {
				continue
			}

			parts := mmLogRegex.FindStringSubmatch(log)

			switch {
			case len(parts) == MMLogParts:
				body = map[string]string{
					"time":  parts[mmLogPartTime],
					"level": parts[mmLogPartLevel],
					"log":   parts[mmLogPartLog],
				}
			case body != nil:
				body["log"] = log
			default:
				continue
			}

			plog.Log(
				mmLogLevelConversion(body["level"]),
				plog.TypeMinimega,
				body["log"],
				"mm_time",
				body["time"],
			)
		}
	}
}

func PublishPhenixLog(ts time.Time, level, logtype, log string) {
	body := &plog.LogEntry{
		Time:      ts.UnixNano(),
		Timestamp: ts.Format(plog.TimestampFormat),
		Level:     level,
		Type:      logtype,
		Message:   log,
	}

	marshalled, _ := json.Marshal(body)

	broker.BroadcastLog(marshalled)
}
