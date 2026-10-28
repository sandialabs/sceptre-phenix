package cmd

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/web"
)

func TestLogRange(t *testing.T) {
	t.Parallel()

	var (
		now   = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
		start = time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
		end   = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	)

	tests := map[string]struct {
		start, end string
		since      time.Duration
		sinceSet   bool
		wantStart  time.Time
		wantEnd    time.Time
		wantErr    error
	}{
		"default since": {since: time.Hour, wantStart: now.Add(-time.Hour)},
		"since":         {since: 10 * time.Minute, sinceSet: true, wantStart: now.Add(-10 * time.Minute)},
		"start and end": {
			start: "2026-11-06T08:00:00Z", end: "2026-11-06T09:00:00Z", since: time.Hour, wantStart: start, wantEnd: end,
		},
		"since and end": {
			end: "2026-11-06T11:30:00Z", since: time.Hour, wantStart: now.Add(-time.Hour), wantEnd: now.Add(-30 * time.Minute),
		},
		"start and since":    {start: "2026-11-06T08:00:00Z", since: time.Hour, sinceSet: true, wantErr: errLogsRange},
		"start not RFC 3339": {start: "2026-11-06 08:00", since: time.Hour, wantErr: errLogsRange},
		"end not RFC 3339":   {end: "yesterday", since: time.Hour, wantErr: errLogsRange},
		"end before start": {
			start: "2026-11-06T09:00:00Z", end: "2026-11-06T08:00:00Z", since: time.Hour, wantErr: errLogsRange,
		},
		"since of zero":        {since: 0, sinceSet: true, wantErr: errLogsRange},
		"negative since":       {since: -time.Minute, sinceSet: true, wantErr: errLogsRange},
		"start with an offset": {start: "2026-11-06T10:00:00+02:00", since: time.Hour, wantStart: start},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			from, to, err := logRange(tt.start, tt.end, tt.since, tt.sinceSet, now)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("logRange(%q, %q, %s, %t) error = %v, want %v", tt.start, tt.end, tt.since, tt.sinceSet, err, tt.wantErr)
			}

			if err == nil && (!from.Equal(tt.wantStart) || !to.Equal(tt.wantEnd)) {
				t.Errorf("logRange(%q, %q, %s, %t) = %s, %s, want %s, %s",
					tt.start, tt.end, tt.since, tt.sinceSet, from, to, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// A range of two RFC 3339 times is accepted exactly when the end is not before the start, and the times come back
// unchanged; a positive since always gives a start that long before now.
func TestLogRangeProperties(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	rapid.Check(t, func(t *rapid.T) {
		a := time.Unix(rapid.Int64Range(0, 4e9).Draw(t, "a"), 0).UTC()
		b := time.Unix(rapid.Int64Range(0, 4e9).Draw(t, "b"), 0).UTC()

		from, to, err := logRange(a.Format(time.RFC3339), b.Format(time.RFC3339), time.Hour, false, now)
		if b.Before(a) != (err != nil) {
			t.Fatalf("logRange(%s, %s) error = %v, want an error %t", a, b, err, b.Before(a))
		}

		if err == nil && (!from.Equal(a) || !to.Equal(b)) {
			t.Fatalf("logRange(%s, %s) = %s, %s, want them unchanged", a, b, from, to)
		}

		since := time.Duration(rapid.Int64Range(1, int64(1000*time.Hour)).Draw(t, "since"))

		from, to, err = logRange("", "", since, true, now)
		if err != nil || !from.Equal(now.Add(-since)) || !to.IsZero() {
			t.Fatalf("logRange(--since %s) = %s, %s, %v, want %s and no end", since, from, to, err, now.Add(-since))
		}
	})
}

func TestLogsCommands(t *testing.T) {
	size := int64(2048)
	modified := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/logs":
			writeJSONBody(w, []logEntry{{Timestamp: "2026-11-06 08:00:00.000", Level: "INFO", Type: "SYSTEM", Message: "started "}})
		case "/api/v1/logs/files":
			writeJSONBody(w, map[string][]web.LogFile{"files": {
				{Name: "phenix", Available: true, Size: &size, Modified: &modified},
				{Name: "minimega", Reason: "the minimega log file is not configured"},
			}})
		case "/api/v1/logs/files/phenix":
			_, _ = w.Write([]byte("whole log\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out, err := runSocketCmd(t, fake.socket, newLogsCmd(), "--start", "2026-11-06T08:00:00Z", "--end", "2026-11-06T09:00:00Z")
	if want := "2026-11-06 08:00:00.000 INFO  SYSTEM    started\n"; err != nil || out != want {
		t.Errorf("logs --start --end = %q, %v, want %q", out, err, want)
	}

	out, err = runSocketCmd(t, fake.socket, newLogsCmd(), "files")
	if err != nil || !strings.Contains(out, "2.0 KiB") || !strings.Contains(out, "is not configured") {
		t.Errorf("logs files = %q, %v, want the phenix log size and why minimega's is not available", out, err)
	}

	local := filepath.Join(t.TempDir(), "phenix.log")

	if _, err := runSocketCmd(t, fake.socket, newLogsCmd(), "download", "phenix", "-O", local); err != nil {
		t.Fatalf("logs download phenix -O error = %v", err)
	}

	if b, _ := os.ReadFile(local); string(b) != "whole log\n" {
		t.Errorf("logs download phenix -O wrote %q, want %q", b, "whole log\n")
	}

	if _, err := runSocketCmd(t, fake.socket, newLogsCmd(), "download", "syslog"); !errors.Is(err, errUnknownLogFile) {
		t.Errorf("logs download syslog error = %v, want %v", err, errUnknownLogFile)
	}

	if _, err := runSocketCmd(t, fake.socket, newLogsCmd(), "--since", "1h", "--start", "2026-11-06T08:00:00Z"); !errors.Is(err,
		errLogsRange) {
		t.Errorf("logs --since --start error = %v, want %v", err, errLogsRange)
	}

	want := []recordedRequest{
		{
			method: http.MethodGet, path: "/api/v1/logs",
			query: url.Values{"start": {"2026-11-06T08:00:00Z"}, "end": {"2026-11-06T09:00:00Z"}},
		},
		{method: http.MethodGet, path: "/api/v1/logs/files"},
		{method: http.MethodGet, path: "/api/v1/logs/files/phenix"},
	}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("logs requests mismatch (-want +got):\n%s", diff)
	}
}
