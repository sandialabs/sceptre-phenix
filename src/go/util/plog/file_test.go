package plog

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Catches the writer/reader timezone mismatch (only reproduces when TZ != UTC).
func TestGetLogsReturnsRecentEntry(t *testing.T) {
	AddFileHandler(filepath.Join(t.TempDir(), "phenix.log"), GetDefaultFileHandlerOpts())

	Info(TypeSystem, "recent entry for GetLogs test")

	logs, err := GetLogs(time.Now().Add(-10*time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}

	for _, l := range logs {
		if strings.Contains(l.Message, "recent entry for GetLogs test") {
			return
		}
	}

	t.Fatalf("entry not found in recent window (got %d entries)", len(logs))
}
