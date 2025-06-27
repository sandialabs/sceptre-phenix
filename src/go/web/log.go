package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"time"

	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/rbac"

	bt "phenix/web/broker/brokertypes"

	"github.com/hpcloud/tail"
	"golang.org/x/exp/slog"
)

// GET /logs?start=<start>&end=<end>
func GetLogs(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetLogs")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		start = r.URL.Query().Get("start")
		end   = r.URL.Query().Get("end")
	)

	if !role.Allowed("logs", "get") {
		plog.Warn(plog.TypeSecurity, "getting logs not allowed", "user", ctx.Value("user").(string))
		http.Error(w, "forbidden", http.StatusForbidden)
	}

	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		plog.Warn(plog.TypeSystem, "Invalid start time provided", "start", start, "error", err)
		http.Error(w, "Missing or invalid start time. Expected RFC3339 format: "+err.Error(), http.StatusBadRequest)
		return
	}

	endTime := time.Now().Add(time.Second * 30)
	if end != "" {
		endTime, err = time.Parse(time.RFC3339, end)
		if err != nil {
			plog.Warn(plog.TypeSystem, "Invalid start time provided", "start", start, "error", err)
			http.Error(w, "Invalid end time. Expected RFC3339 format: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	logs, err := plog.GetLogs(startTime, endTime)
	if err != nil {
		plog.Error(plog.TypeSystem, "Error getting logs in handler", "error", err)
		http.Error(w, "error with fetching logs", http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	// if len(logs) > 10_000 {
	// 	logs = logs[len(logs)-10_000:]
	// }

	body, err := json.Marshal(logs)
	if err != nil {
		plog.Error(plog.TypeSystem, "error marshaling loglist", "error", err)
		http.Error(w, "error with converting logs to protobuf", http.StatusInternalServerError)
	}
	w.Write(body)
}

var mmLogRegex = regexp.MustCompile(`\A(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2})\s* (DEBUG|INFO|WARN|ERROR|FATAL) .*?: (.*)\z`)

func mmLogLevelConversion(l string) slog.Level {
	switch l {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	default:
		plog.Warn(plog.TypeSystem, "found unknown level in mm log file", "level", l)
		fallthrough
	case "FATAL":
		fallthrough
	case "ERROR":
		return slog.LevelError
	}
}

// tails mm log file and writes back as plog entries
func SyncMinimegaLogs(ctx context.Context, minimega string) {
	if minimega == "" {
		return
	}

	logs := make(chan string)

	tailConfig := tail.Config{Follow: true, ReOpen: true, Poll: true, Location: &tail.SeekInfo{Offset: 0, Whence: os.SEEK_END}}
	mmLogs, err := tail.TailFile(minimega, tailConfig)
	if err != nil {
		panic("setting up tail for minimega logs: " + err.Error())
	}

	go func() {
		for l := range mmLogs.Lines {
			logs <- l.Text
		}
	}()

	// used to detect multi-line minimega logs
	var body map[string]string

	for {
		select {
		case <-ctx.Done():
			return
		case log := <-logs:
			if log == "" {
				continue
			}
			parts := mmLogRegex.FindStringSubmatch(log)

			if len(parts) == 4 {
				body = map[string]string{
					"time":  parts[1],
					"level": parts[2],
					"log":   parts[3],
				}
			} else if body != nil {
				body["log"] = log
			} else {
				continue
			}

			plog.Log(mmLogLevelConversion(body["level"]), plog.TypeMinimega, body["log"], "mm_time", body["time"])
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

	broker.Broadcast(
		nil,
		bt.NewResource("log", "phenix", "update"),
		marshalled,
	)
}
