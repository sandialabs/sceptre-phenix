// Package plogtest records what phenix/util/plog logs while a test runs. It
// is imported only by tests.
package plogtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"phenix/util/plog"
)

// captures numbers the plog handlers Capture adds, so two captures in one
// test never replace each other.
var captures atomic.Int64 //nolint:gochecknoglobals // handler names for the process

// Logs holds what plog logged, as JSON lines, since [Capture] or the last
// [Logs.Take].
type Logs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

// Capture records every record plog logs, at every level, until the test
// ends.
func Capture(tb testing.TB) *Logs {
	tb.Helper()

	logs := new(Logs)
	name := fmt.Sprintf("plogtest-%s-%d", tb.Name(), captures.Add(1))

	plog.AddHandler(name, slog.NewJSONHandler(logs, &slog.HandlerOptions{
		AddSource: false, Level: slog.LevelDebug, ReplaceAttr: nil,
	}))
	tb.Cleanup(func() { plog.RemoveHandler(name) })

	return logs
}

// Write records p, which plog's JSON handler writes one record at a time.
func (l *Logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buffer.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

// String returns everything logged so far, as JSON lines.
func (l *Logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buffer.String()
}

// Records returns every record logged so far that match accepts, each as its
// JSON object, in the order they were logged. A nil match accepts every
// record.
func (l *Logs) Records(tb testing.TB, match func(record map[string]any) bool) []map[string]any {
	tb.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.records(tb, match)
}

// Take is [Logs.Records] that then forgets everything logged so far, so the
// next call sees only what is logged after it.
func (l *Logs) Take(tb testing.TB, match func(record map[string]any) bool) []map[string]any {
	tb.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	found := l.records(tb, match)
	l.buffer.Reset()

	return found
}

// Message accepts the records logged with the message msg.
func Message(msg string) func(map[string]any) bool {
	return func(record map[string]any) bool { return record["msg"] == msg }
}

// Level accepts the records logged at level.
func Level(level slog.Level) func(map[string]any) bool {
	return func(record map[string]any) bool { return record["level"] == level.String() }
}

func (l *Logs) records(tb testing.TB, match func(map[string]any) bool) []map[string]any {
	tb.Helper()

	var found []map[string]any

	for line := range bytes.Lines(l.buffer.Bytes()) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			tb.Fatalf("decoding log record %q: %v", line, err)
		}

		if match == nil || match(record) {
			found = append(found, record)
		}
	}

	return found
}
