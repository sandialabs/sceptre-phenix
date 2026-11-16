package plog

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// raiseLevel sets the shared log level for the test, restoring it after.
func raiseLevel(t *testing.T, level string) {
	t.Helper()

	original := Level.Level()

	SetLevelText(level)
	t.Cleanup(func() { Level.Set(original) })
}

func TestAuditReachesFileAtAnyLevel(t *testing.T) {
	logPath := useLogFile(t)

	for _, level := range []string{"error", "none"} {
		t.Run(level, func(t *testing.T) {
			raiseLevel(t, level)

			Audit(slog.LevelInfo, "audited at "+level, "event.action", "test-"+level)
			Info(TypeSystem, "not audited at "+level)

			logs, err := GetLogs(time.Now().Add(-10*time.Minute), time.Now().Add(time.Minute))
			if err != nil {
				t.Fatalf("GetLogs(last 10 minutes) error = %v", err)
			}

			var found bool

			for _, l := range logs {
				if strings.Contains(l.Message, "not audited at "+level) {
					t.Errorf("GetLogs(last 10 minutes) at log.level %s holds Info record %+v, want it filtered", level, l)
				}

				if !strings.Contains(l.Message, "audited at "+level) || strings.Contains(l.Message, "not audited") {
					continue
				}

				found = true

				if l.Type != string(TypeAudit) {
					t.Errorf("GetLogs(last 10 minutes) audit entry type = %q, want %q", l.Type, TypeAudit)
				}

				if strings.Contains(l.Message, auditTimestampKey) {
					t.Errorf("GetLogs(last 10 minutes) audit message = %q, want it without %s", l.Message, auditTimestampKey)
				}

				if !strings.Contains(l.Message, "event.action=test-"+level) {
					t.Errorf("GetLogs(last 10 minutes) audit message = %q, want it with event.action=test-%s", l.Message, level)
				}
			}

			if !found {
				t.Errorf("GetLogs(last 10 minutes) at log.level %s holds no audit record, want one", level)
			}
		})
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", logPath, err)
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("json.Unmarshal(log line %q) error = %v", line, err)

			continue
		}

		if record[logAttrType] != string(TypeAudit) {
			continue
		}

		ts, _ := record[auditTimestampKey].(string)

		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !strings.HasSuffix(ts, "Z") {
			t.Errorf("audit record %s = %q (parse error %v), want RFC 3339 in UTC", auditTimestampKey, ts, err)
		}

		if time.Since(parsed) > time.Minute {
			t.Errorf("audit record %s = %q, want within a minute of now", auditTimestampKey, ts)
		}
	}
}

func TestAuditSkipsLevelForOtherHandlers(t *testing.T) {
	var got []string

	AddHandler(t.Name(), NewUIHandler("ERROR", func(_ time.Time, _, logtype, message string) {
		got = append(got, logtype+" "+message)
	}))
	t.Cleanup(func() { RemoveHandler(t.Name()) })

	Audit(slog.LevelInfo, "info audit")
	Audit(slog.LevelError, "error audit", "user.name", "alice")

	// type AUDIT, without the ECS timestamp
	want := []string{"AUDIT error audit user.name=alice"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("UI handler at ERROR lines after Audit(Info) and Audit(Error) mismatch (-want +got):\n%s", diff)
	}
}

// Audit does nothing before the package logger exists.
func TestAuditWithoutLogger(t *testing.T) { //nolint:paralleltest // replaces the package logger
	var got []string

	AddHandler(t.Name(), NewUIHandler("DEBUG", func(_ time.Time, _, _, message string) {
		got = append(got, message)
	}))
	t.Cleanup(func() { RemoveHandler(t.Name()) })

	original := logger
	logger = nil

	t.Cleanup(func() { logger = original })

	Audit(slog.LevelError, "dropped audit")

	if len(got) != 0 {
		t.Errorf("UI handler lines after Audit() without a logger = %q, want none", got)
	}

	logger = original

	Audit(slog.LevelError, "kept audit")

	if diff := cmp.Diff([]string{"kept audit"}, got); diff != "" {
		t.Errorf("UI handler lines after Audit() with the logger mismatch (-want +got):\n%s", diff)
	}
}

// Only the context of an Audit record is one.
func TestIsAudit(t *testing.T) {
	t.Parallel()

	type otherKey struct{}

	tests := map[string]struct {
		ctx  context.Context //nolint:containedctx // the context under test
		want bool
	}{
		"audit":      {ctx: context.WithValue(context.Background(), auditKey{}, true), want: true},
		"not audit":  {ctx: context.WithValue(context.Background(), auditKey{}, false), want: false},
		"other type": {ctx: context.WithValue(context.Background(), auditKey{}, "true"), want: false},
		"no value":   {ctx: context.Background(), want: false},
		"derived":    {ctx: context.WithValue(context.WithValue(context.Background(), auditKey{}, true), otherKey{}, "v"), want: true},
		"other key":  {ctx: context.WithValue(context.Background(), otherKey{}, true), want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := isAudit(tc.ctx); got != tc.want {
				t.Errorf("isAudit(%s context) = %t, want %t", name, got, tc.want)
			}
		})
	}
}
