package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/util/plog"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
	"phenix/web/broker"
	"phenix/web/middleware"
)

// GET /logs answers a role without logs get, or a bad time range, with an
// error and no logs.
func TestGetLogsRefusals(t *testing.T) {
	start := url.QueryEscape(time.Now().Add(-time.Minute).Format(time.RFC3339))

	tests := map[string]struct {
		role       string
		query      string
		wantStatus int
		wantPrefix string
	}{
		"no logs access":    {"experiments", "start=" + start, http.StatusForbidden, "forbidden"},
		"no start":          {"logs", "", http.StatusBadRequest, "Missing or invalid start time"},
		"malformed start":   {"logs", "start=yesterday", http.StatusBadRequest, "Missing or invalid start time"},
		"start not RFC3339": {"logs", "start=2024-01-01", http.StatusBadRequest, "Missing or invalid start time"},
		"malformed end":     {"logs", "start=" + start + "&end=soon", http.StatusBadRequest, "Invalid end time"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			role := testRole("logs", "get")
			if tc.role != "logs" {
				role = testRole(tc.role, "list")
			}

			path := "/api/v1/logs?" + tc.query
			resp := do(t, http.MethodGet, serveAs(t, "alice", role).URL+path, "")

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, tc.wantStatus)
			}

			if got := errorMessage(t, resp.StatusCode, readBody(t, resp)); !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("GET %s = %q, want it to start with %q", path, got, tc.wantPrefix)
			}
		})
	}
}

// logGetEntry is a GET /logs entry as sent; plog.LogEntry's own decoding
// reads the time otherwise.
type logGetEntry struct {
	Time      int64  `json:"time"`
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"msg"`
	Type      string `json:"type"`
}

// GET /logs answers the system log's entries strictly between start and end,
// an empty list when none are, and 500 when the log cannot be read. Each case
// points plog at its own log file, which the test cannot unset afterward.
func TestGetLogs(t *testing.T) {
	at := func(minute, second int) time.Time { return time.Date(2024, 5, 6, 7, minute, second, 0, time.UTC) }

	// as plog's file handler writes them, in two cache buckets
	lines := []string{
		`{"time":%q,"level":"INFO","msg":"first","type":"SYSTEM"}`,
		`{"time":%q,"level":"WARN","msg":"second","type":"SECURITY","vm":"a"}`,
		`{"time":%q,"level":"ERROR","msg":"third","type":"MINIMEGA"}`,
	}
	times := []time.Time{at(0, 0), at(1, 0), at(15, 0)}
	stamps := make([]string, len(times))

	var contents strings.Builder
	for i, line := range lines {
		stamps[i] = times[i].Local().Format(plog.TimestampFormat) //nolint:gosmopolitan // plog writes zone-less local times
		fmt.Fprintf(&contents, line+"\n", stamps[i])
	}

	entry := func(i int, level, msg, typ string) logGetEntry {
		return logGetEntry{times[i].UnixMicro(), stamps[i], level, msg, typ}
	}

	// plog appends any other attributes to the message, after a space
	var (
		first  = entry(0, "INFO", "first ", "SYSTEM")
		second = entry(1, "WARN", "second vm=a", "SECURITY")
		third  = entry(2, "ERROR", "third ", "MINIMEGA")
	)

	tests := map[string]struct {
		noFile     bool
		start, end time.Time
		wantStatus int
		want       []logGetEntry
	}{
		"all":                 {start: at(-10, 0), end: at(20, 0), wantStatus: http.StatusOK, want: []logGetEntry{first, second, third}},
		"from the first":      {start: at(0, 30), end: at(20, 0), wantStatus: http.StatusOK, want: []logGetEntry{second, third}},
		"bounds are excluded": {start: at(0, 0), end: at(15, 0), wantStatus: http.StatusOK, want: []logGetEntry{second}},
		"in the last bucket":  {start: at(14, 0), end: at(20, 0), wantStatus: http.StatusOK, want: []logGetEntry{third}},
		"none":                {start: at(30, 0), end: at(40, 0), wantStatus: http.StatusOK, want: []logGetEntry{}},
		"no log file":         {noFile: true, start: at(0, 30), end: at(20, 0), wantStatus: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "phenix.log")
			if !tc.noFile {
				if err := os.WriteFile(file, []byte(contents.String()), 0o600); err != nil {
					t.Fatalf("writing phenix.log: %v", err)
				}
			}

			plog.ChangeLogFile(file)

			path := "/api/v1/logs?start=" + url.QueryEscape(tc.start.Format(time.RFC3339)) +
				"&end=" + url.QueryEscape(tc.end.Format(time.RFC3339))
			resp := do(t, http.MethodGet, serveAs(t, "alice", testRole("logs", "get")).URL+path, "")
			body := readBody(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d", path, resp.StatusCode, tc.wantStatus)
			}

			if tc.wantStatus != http.StatusOK {
				if got, want := errorMessage(t, resp.StatusCode, body), "error with fetching logs"; got != want {
					t.Errorf("GET %s = %q, want %q", path, got, want)
				}

				return
			}

			if got := resp.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("GET %s Content-Type = %q, want application/json", path, got)
			}

			// no entries is an empty list, not null
			var got []logGetEntry
			if err := json.Unmarshal([]byte(body), &got); err != nil || got == nil {
				t.Fatalf("GET %s body = %s, want a JSON list (error %v)", path, body, err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GET %s entries mismatch (-want +got):\n%s", path, diff)
			}
		})
	}
}

func TestMMLogLevelConversion(t *testing.T) {
	logs := plogtest.Capture(t)

	for level, want := range map[string]slog.Level{
		"DEBUG": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"WARN":  slog.LevelWarn,
		"ERROR": slog.LevelError,
		"FATAL": slog.LevelError,
		"":      slog.LevelError,
		"info":  slog.LevelError,
		"TRACE": slog.LevelError,
	} {
		if got := mmLogLevelConversion(level); got != want {
			t.Errorf("mmLogLevelConversion(%q) = %v, want %v", level, got, want)
		}
	}

	// only the three unknown levels are warned of
	if got := len(logs.Take(t, plogtest.Message("found unknown level in mm log file"))); got != 3 {
		t.Errorf("unknown level warnings = %d, want 3", got)
	}
}

// logMinimegaRecord is a minimega log line as SyncMinimegaLogs logs it.
type logMinimegaRecord struct {
	Level, Msg, MMTime string
}

// SyncMinimegaLogs logs each new line of minimega's log file as a MINIMEGA
// record at its level, a line without a header as one more line of the
// record before it, and skips empty lines; it stops when ctx is done.
func TestSyncMinimegaLogs(t *testing.T) {
	logs := plogtest.Capture(t)

	file := filepath.Join(t.TempDir(), "minimega.log")
	if err := os.WriteFile(file, []byte("2024/05/06 07:08:00 INFO old.go:1: before the tail started\n"), 0o600); err != nil {
		t.Fatalf("writing minimega.log: %v", err)
	}

	appendLines := func(lines ...string) {
		t.Helper()

		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("opening minimega.log: %v", err)
		}

		defer f.Close()

		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatalf("appending to minimega.log: %v", err)
		}
	}

	minimega := func() []logMinimegaRecord {
		records := logs.Records(t, func(r map[string]any) bool { return r["type"] == string(plog.TypeMinimega) })
		got := make([]logMinimegaRecord, 0, len(records))

		for _, r := range records {
			level, _ := r["level"].(string)
			msg, _ := r["msg"].(string)
			mmTime, _ := r["mm_time"].(string)
			got = append(got, logMinimegaRecord{level, msg, mmTime})
		}

		return got
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		SyncMinimegaLogs(ctx, file)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	// the tail starts at the end of the file at some point: a line written
	// before then is not read, so write one until it is
	const ready = "2024/05/06 07:08:01 INFO main.go:1: ready"

	var written time.Time

	polltest.Until(t, "SyncMinimegaLogs reading new lines", func() bool {
		if time.Since(written) > 100*time.Millisecond {
			appendLines(ready)
			written = time.Now()
		}

		return len(minimega()) > 0
	})

	// every ready line comes before this one: once it is logged, none is in flight
	appendLines("2024/05/06 07:08:02 INFO main.go:1: caught up")

	polltest.Until(t, "SyncMinimegaLogs reading the caught up line", func() bool {
		return slices.ContainsFunc(minimega(), func(r logMinimegaRecord) bool { return r.Msg == "caught up" })
	})

	logs.Take(t, nil)

	appendLines(
		"2024/05/06 07:08:09 WARN   vm.go:12: disk low",
		"",
		"  more about the disk",
		"2024/05/06 07:08:10 FATAL main.go:1: dying",
		"2024/05/06 07:08:11 DEBUG x.go:1: detail: with a colon",
		"2024/05/06 07:08:12 ERROR x.go:1: failed",
	)

	want := []logMinimegaRecord{
		{"WARN", "disk low", "2024/05/06 07:08:09"},
		{"WARN", "  more about the disk", "2024/05/06 07:08:09"},
		{"ERROR", "dying", "2024/05/06 07:08:10"},
		{"DEBUG", "detail: with a colon", "2024/05/06 07:08:11"},
		{"ERROR", "failed", "2024/05/06 07:08:12"},
	}

	polltest.Until(t, "SyncMinimegaLogs logging the new lines", func() bool { return len(minimega()) >= len(want) })

	if diff := cmp.Diff(want, minimega()); diff != "" {
		t.Errorf("MINIMEGA log records mismatch (-want +got):\n%s", diff)
	}

	cancel()
	polltest.Within(t, "SyncMinimegaLogs stopping", func() { <-done })
}

// Without a minimega log file to follow, SyncMinimegaLogs returns at once.
func TestSyncMinimegaLogsWithoutFile(t *testing.T) {
	t.Parallel()

	polltest.Within(t, "SyncMinimegaLogs without a file returning", func() {
		SyncMinimegaLogs(t.Context(), "")
	})
}

// PublishPhenixLog sends the entry to the clients that show logs.
//
//nolint:paralleltest // runs the broker
func TestPublishPhenixLog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		broker.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyRole, testRole("logs", "get"))
		broker.ServeWS(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the broker: %v", err)
	}

	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"resource": {"type": "log", "name": "phenix", "action": "subscribe"}}`)); err != nil {
		t.Fatalf("subscribing to logs: %v", err)
	}

	// the entry's fields as sent; LogEntry's own decoding reads time otherwise
	type logEntry struct {
		Time      int64  `json:"time"`
		Timestamp string `json:"timestamp"`
		Level     string `json:"level"`
		Message   string `json:"msg"`
		Type      string `json:"type"`
	}

	type logResource struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Action string `json:"action"`
	}

	type logMessage struct {
		Resource logResource `json:"resource"`
		Result   logEntry    `json:"result"`
	}

	messages := make(chan logMessage, 64)

	go func() {
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// the broker may send several messages in one frame
			for dec := json.NewDecoder(bytes.NewReader(b)); ; {
				var m logMessage
				if dec.Decode(&m) != nil {
					break
				}

				if m.Result.Message == "published for the logs page" {
					select {
					case messages <- m:
					default:
					}
				}
			}
		}
	}()

	ts := time.Date(2024, 5, 6, 7, 8, 9, 10_000_000, time.UTC)

	// the subscription takes effect at some point: publish until it has
	var got logMessage

	polltest.Until(t, "the logs page getting the entry", func() bool {
		PublishPhenixLog(ts, "WARN", string(plog.TypeSystem), "published for the logs page")

		select {
		case got = <-messages:
			return true
		case <-time.After(20 * time.Millisecond):
			return false
		}
	})

	var want logMessage

	want.Resource.Type, want.Resource.Name, want.Resource.Action = "log", "phenix", "update"
	want.Result = logEntry{
		Time: ts.UnixNano(), Timestamp: ts.Format(plog.TimestampFormat),
		Level: "WARN", Message: "published for the logs page", Type: string(plog.TypeSystem),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("published log message mismatch (-want +got):\n%s", diff)
	}
}
