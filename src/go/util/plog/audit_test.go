package plog

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
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
				t.Fatalf("GetLogs: %v", err)
			}

			var found bool

			for _, l := range logs {
				if strings.Contains(l.Message, "not audited at "+level) {
					t.Errorf("an Info record below log.level %s reached the file: %+v", level, l)
				}

				if !strings.Contains(l.Message, "audited at "+level) || strings.Contains(l.Message, "not audited") {
					continue
				}

				found = true

				if l.Type != string(TypeAudit) {
					t.Errorf("type = %q, want %q", l.Type, TypeAudit)
				}

				if strings.Contains(l.Message, auditTimestampKey) {
					t.Errorf("history entry shows the ECS timestamp: %q", l.Message)
				}

				if !strings.Contains(l.Message, "event.action=test-"+level) {
					t.Errorf("history entry lost the audit fields: %q", l.Message)
				}
			}

			if !found {
				t.Fatalf("audit record not in the log file at log.level %s", level)
			}
		})
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}

		if record[logAttrType] != string(TypeAudit) {
			continue
		}

		ts, _ := record[auditTimestampKey].(string)

		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !strings.HasSuffix(ts, "Z") {
			t.Errorf("@timestamp %q is not RFC 3339 in UTC (%v)", ts, err)
		}

		if time.Since(parsed) > time.Minute {
			t.Errorf("@timestamp %q is not the record's time", ts)
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

	if len(got) != 1 {
		t.Fatalf("UI handler at ERROR got %d records, want only the error audit: %q", len(got), got)
	}

	if got[0] != "AUDIT error audit user.name=alice" {
		t.Errorf("UI line = %q, want type AUDIT without the ECS timestamp", got[0])
	}
}
