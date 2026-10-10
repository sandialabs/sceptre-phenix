package plogtest_test

import (
	"log/slog"
	"strings"
	"testing"

	"phenix/util/plog"
	"phenix/util/plog/plogtest"
)

func TestCaptureRecordsWhatPlogLogs(t *testing.T) {
	logs := plogtest.Capture(t)

	plog.Info(plog.TypeSystem, "first", "n", 1)
	plog.Warn(plog.TypeSystem, "second")

	if got := logs.Records(t, nil); len(got) != 2 {
		t.Fatalf("records = %v, want both", got)
	}

	if got := logs.Records(t, plogtest.Level(slog.LevelWarn)); len(got) != 1 || got[0]["msg"] != "second" {
		t.Fatalf("warnings = %v, want the second record", got)
	}

	if !strings.Contains(logs.String(), `"msg":"first"`) {
		t.Fatalf("String() = %q, want the first record", logs.String())
	}

	if got := logs.Take(t, plogtest.Message("first")); len(got) != 1 || got[0]["n"] != float64(1) {
		t.Fatalf("Take = %v, want the first record", got)
	}

	plog.Info(plog.TypeSystem, "third")

	if got := logs.Records(t, nil); len(got) != 1 || got[0]["msg"] != "third" {
		t.Fatalf("records after Take = %v, want only the third", got)
	}
}
