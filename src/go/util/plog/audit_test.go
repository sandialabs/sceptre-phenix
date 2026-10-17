package plog

import (
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
