package plog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	logFileOnce sync.Once //nolint:gochecknoglobals // one file handler per test binary
	logFilePath string    //nolint:gochecknoglobals // one file handler per test binary
)

// TestMain removes the shared log file's directory after the tests.
func TestMain(m *testing.M) {
	code := m.Run()

	if logFilePath != "" {
		_ = CloseFile()
		_ = os.RemoveAll(filepath.Dir(logFilePath))
	}

	os.Exit(code)
}

// useLogFile adds the system log file handler once for every test: adding
// it again races the rotated-file cleanup the first one started.
func useLogFile(t *testing.T) string {
	t.Helper()

	logFileOnce.Do(func() {
		dir, err := os.MkdirTemp("", "plog-test-") //nolint:usetesting // outlives the first test; TestMain removes it
		if err != nil {
			t.Fatalf("os.MkdirTemp() error = %v", err)
		}

		logFilePath = filepath.Join(dir, "phenix.log")
		AddFileHandler(logFilePath, GetDefaultFileHandlerOpts())
	})

	return logFilePath
}

// Catches the writer/reader timezone mismatch (only reproduces when TZ != UTC).
func TestGetLogsReturnsRecentEntry(t *testing.T) {
	useLogFile(t)

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
