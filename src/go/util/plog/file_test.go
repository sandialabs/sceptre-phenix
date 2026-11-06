package plog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // the child test of TestGetLogsReturnsRecentEntry loads nonUTCZone
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

// nonUTCZone is the local zone of the child process that
// TestGetLogsReturnsRecentEntry starts. It has no daylight saving time.
const nonUTCZone = "Asia/Kolkata"

// GetLogs finds an entry just written, also when the local zone is not UTC:
// the writer and the reader must use the same zone.
func TestGetLogsReturnsRecentEntry(t *testing.T) {
	if _, offset := time.Now().Zone(); offset == 0 {
		if os.Getenv("TZ") == nonUTCZone {
			t.Fatalf("local zone is UTC with TZ=%s, want a non-UTC zone", nonUTCZone)
		}

		runInZone(t, nonUTCZone)

		return
	}

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

// runInZone runs the current test again in a child test binary whose local
// zone is zone, because the process reads TZ only once.
func runInZone(t *testing.T, zone string) {
	t.Helper()

	args := []string{"-test.run=^" + t.Name() + "$", "-test.count=1", "-test.v"}

	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = append(os.Environ(), "TZ="+zone)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s with TZ=%s failed: %v\n%s", t.Name(), zone, err, out)
	}

	if !strings.Contains(string(out), "--- PASS: "+t.Name()) {
		t.Fatalf("%s with TZ=%s did not run:\n%s", t.Name(), zone, out)
	}
}

// LogFilePath names the file the file handler writes, for downloads of it.
func TestLogFilePath(t *testing.T) {
	path := useLogFile(t)

	if got := LogFilePath(); got != path {
		t.Errorf("LogFilePath() = %q, want %q", got, path)
	}
}
