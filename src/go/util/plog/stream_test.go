package plog_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/plog"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
)

// Lines that the phenix-apps Python logger writes for a message with three
// lines, for a message with a traceback in two parts, and for a message with
// one line.
const (
	pyPart1 = `{"level": "INFO", "proc_time": "2026-11-04 11:54:32.110", "file": "vyos.py", "line": 42, ` +
		`"type": "SCORCH", "msg": "executing command '#!/bin/vbash", "part": 1, "parts": 3}`
	pyPart2 = `{"level": "INFO", "proc_time": "2026-11-04 11:54:32.110", "file": "vyos.py", "line": 42, ` +
		`"type": "SCORCH", "msg": "configure", "part": 2, "parts": 3}`
	pyPart3 = `{"level": "INFO", "proc_time": "2026-11-04 11:54:32.110", "file": "vyos.py", "line": 42, ` +
		`"type": "SCORCH", "msg": "' in VM INT-fw", "part": 3, "parts": 3}`
	pyErrMsg = `{"level": "ERROR", "proc_time": "2026-11-04 11:54:33.000", "file": "app.py", "line": 7, ` +
		`"msg": "component failed", "part": 1, "parts": 3}`
	pyErrTB1 = `{"level": "ERROR", "proc_time": "2026-11-04 11:54:33.000", "file": "app.py", "line": 7, ` +
		`"msg": "Traceback (most recent call last):", "part": 2, "parts": 3, "chunk_type": "traceback"}`
	pyErrTB2 = `{"level": "ERROR", "proc_time": "2026-11-04 11:54:33.000", "file": "app.py", "line": 7, ` +
		`"msg": "ValueError: bad", "part": 3, "parts": 3, "chunk_type": "traceback"}`
	pySingle = `{"level": "WARNING", "proc_time": "2026-11-04 11:54:34.000", "file": "app.py", "line": 9, ` +
		`"msg": "one line", "traceback": "trace line"}`
)

// appRecord is the record that ProcessStderrLogs logs for the app
// "test-app", without its time.
func appRecord(level, msg string, extra ...any) map[string]any {
	record := map[string]any{"level": level, "msg": msg, "type": string(plog.TypePhenixApp), "app": "test-app"}

	for i := 0; i+1 < len(extra); i += 2 {
		record[extra[i].(string)] = extra[i+1] //nolint:forcetypeassert // test keys are strings
	}

	return record
}

// stderrRecord is appRecord for a line from the Python logger.
func stderrRecord(level, msg, procTime, file string, line float64, extra ...any) map[string]any {
	return appRecord(level, msg, append([]any{"proc_time", procTime, "file", file, "line", line}, extra...)...)
}

// notFormatWarning drops the warning that ProcessStderrLogs logs once for
// each process, at the first line that is not JSON.
func notFormatWarning(record map[string]any) bool {
	return !strings.HasPrefix(record["msg"].(string), "For best experience") //nolint:forcetypeassert // msg is a string
}

func withoutTime(records []map[string]any) []map[string]any {
	for _, r := range records {
		delete(r, "time")
	}

	return records
}

// ProcessStderrLogs logs the parts of a multi-part message as one record, and
// logs what it collected when the part sequence breaks or the channel closes.
func TestProcessStderrLogsJoinsParts(t *testing.T) {
	scorch := func(level, msg string) map[string]any {
		return stderrRecord(level, msg, "2026-11-04 11:54:32.110", "vyos.py", 42, "type", "SCORCH")
	}
	errRecord := func(msg string) map[string]any {
		return stderrRecord("ERROR", msg, "2026-11-04 11:54:33.000", "app.py", 7)
	}
	other := `{"level": "INFO", "msg": "other"}`
	otherRecord := appRecord("INFO", "other")

	tests := []struct {
		name  string
		lines []string
		want  []map[string]any
	}{
		{
			name:  "three lines",
			lines: []string{pyPart1, pyPart2, pyPart3},
			want:  []map[string]any{scorch("INFO", "executing command '#!/bin/vbash\nconfigure\n' in VM INT-fw")},
		},
		{
			name:  "message and traceback",
			lines: []string{pyErrMsg, pyErrTB1, pyErrTB2},
			want:  []map[string]any{errRecord("component failed\nTraceback (most recent call last):\nValueError: bad")},
		},
		{
			name:  "record without parts interrupts",
			lines: []string{pyPart1, pyPart2, other, pyPart3},
			want: []map[string]any{
				scorch("INFO", "executing command '#!/bin/vbash\nconfigure"),
				otherRecord,
				scorch("INFO", "' in VM INT-fw"),
			},
		},
		{
			name:  "restarted numbering interrupts",
			lines: []string{pyPart1, pyPart2, pyPart1, pyPart2, pyPart3},
			want: []map[string]any{
				scorch("INFO", "executing command '#!/bin/vbash\nconfigure"),
				scorch("INFO", "executing command '#!/bin/vbash\nconfigure\n' in VM INT-fw"),
			},
		},
		{
			name:  "other level interrupts",
			lines: []string{pyPart1, pyErrTB1, pyErrTB2},
			want: []map[string]any{
				scorch("INFO", "executing command '#!/bin/vbash"),
				errRecord("Traceback (most recent call last):\nValueError: bad"),
			},
		},
		{
			name:  "channel closes mid-sequence",
			lines: []string{pyPart1, pyPart2},
			want:  []map[string]any{scorch("INFO", "executing command '#!/bin/vbash\nconfigure")},
		},
		{
			name:  "text line interrupts",
			lines: []string{pyPart1, "plain text", pyPart2, pyPart3},
			want: []map[string]any{
				scorch("INFO", "executing command '#!/bin/vbash"),
				appRecord("ERROR", "plain text"),
				scorch("INFO", "configure\n' in VM INT-fw"),
			},
		},
		{
			name:  "record without level",
			lines: []string{`{"msg": "no level", "part": 1, "parts": 1}`},
			want: []map[string]any{
				appRecord("WARN", "could not find level key in log", "log", `{"msg": "no level", "part": 1, "parts": 1}`),
				appRecord("INFO", `{"msg": "no level", "part": 1, "parts": 1}`),
			},
		},
		{
			name:  "record without msg",
			lines: []string{`{"level": "INFO"}`},
			want: []map[string]any{
				appRecord("WARN", "could not find msg key in log", "log", `{"level": "INFO"}`),
				appRecord("INFO", `{"level": "INFO"}`),
			},
		},
		{
			name:  "time becomes proc_time",
			lines: []string{`{"level": "DEBUG", "msg": "timed", "time": "12:00"}`},
			want:  []map[string]any{appRecord("DEBUG", "timed", "proc_time", "12:00")},
		},
		{
			name:  "single record unchanged",
			lines: []string{pySingle},
			want:  []map[string]any{stderrRecord("WARN", "one line\ntrace line", "2026-11-04 11:54:34.000", "app.py", 9)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := plogtest.Capture(t)

			ch := make(chan []byte)
			done := make(chan struct{})

			go func() {
				defer close(done)

				plog.ProcessStderrLogs(ch, plog.TypePhenixApp, "app", "test-app")
			}()

			for _, line := range tc.lines {
				ch <- []byte(line)
			}

			close(ch)
			<-done

			got := withoutTime(logs.Records(t, notFormatWarning))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ProcessStderrLogs(%q) records mismatch (-want +got):\n%s", tc.lines, diff)
			}
		})
	}
}

// ProcessStderrLogs logs the collected parts when the next part does not come
// within the flush interval.
func TestProcessStderrLogsFlushesPartsAfterInterval(t *testing.T) {
	logs := plogtest.Capture(t)

	ch := make(chan []byte)
	done := make(chan struct{})

	go func() {
		defer close(done)

		plog.ProcessStderrLogs(ch, plog.TypePhenixApp, "app", "test-app")
	}()

	ch <- []byte(pyPart1)

	polltest.Until(t, "the first part logged alone", func() bool {
		return len(logs.Records(t, nil)) == 1
	})

	ch <- []byte(pyPart2)
	ch <- []byte(pyPart3)

	close(ch)
	<-done

	want := []map[string]any{
		stderrRecord("INFO", "executing command '#!/bin/vbash", "2026-11-04 11:54:32.110", "vyos.py", 42, "type", "SCORCH"),
		stderrRecord("INFO", "configure\n' in VM INT-fw", "2026-11-04 11:54:32.110", "vyos.py", 42, "type", "SCORCH"),
	}
	if diff := cmp.Diff(want, withoutTime(logs.Records(t, nil))); diff != "" {
		t.Errorf("ProcessStderrLogs() records mismatch (-want +got):\n%s", diff)
	}
}

// Two processes that log text lines at the same time share the warning about
// the log format: ProcessStderrLogs logs it at most once.
func TestProcessStderrLogsConcurrentTextLines(t *testing.T) {
	logs := plogtest.Capture(t)

	var wg sync.WaitGroup

	for _, app := range []string{"app-1", "app-2"} {
		ch := make(chan []byte)

		wg.Add(1)

		go func() {
			defer wg.Done()

			plog.ProcessStderrLogs(ch, plog.TypePhenixApp, "app", app)
		}()

		wg.Add(1)

		go func() {
			defer wg.Done()
			defer close(ch)

			for range 20 {
				ch <- []byte("plain text")
			}
		}()
	}

	wg.Wait()

	warnings := logs.Records(t, func(record map[string]any) bool { return !notFormatWarning(record) })
	if len(warnings) > 1 {
		t.Errorf("ProcessStderrLogs() logged %d log format warnings, want at most 1", len(warnings))
	}

	if got := len(logs.Records(t, plogtest.Message("plain text"))); got != 40 {
		t.Errorf("ProcessStderrLogs() logged %d text records, want 40", got)
	}
}

// duplicateKeys returns the top-level keys that occur more than once in the
// JSON object line.
func duplicateKeys(tb testing.TB, line []byte) []string {
	tb.Helper()

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(line, &keys); err != nil {
		tb.Fatalf("decoding log record %q: %v", line, err)
	}

	dec := json.NewDecoder(bytes.NewReader(line))
	seen := make(map[string]bool, len(keys))

	var dups []string

	if _, err := dec.Token(); err != nil {
		tb.Fatalf("decoding log record %q: %v", line, err)
	}

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			tb.Fatalf("decoding log record %q: %v", line, err)
		}

		key, _ := tok.(string)
		if seen[key] {
			dups = append(dups, key)
		}

		seen[key] = true

		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			tb.Fatalf("decoding log record %q: %v", line, err)
		}
	}

	return dups
}

// An app key that has the name of a phenix attribute does not replace it and
// does not make a duplicate key: it gets the "proc_" prefix. A "type" key with
// a log type that is not AUDIT or SECURITY sets the type of the record.
func TestProcessStderrLogsAppKeyCollisions(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []map[string]any
	}{
		{
			name:  "type sets the log type",
			lines: []string{`{"level": "INFO", "msg": "typed", "type": "SCORCH"}`},
			want:  []map[string]any{appRecord("INFO", "typed", "type", "SCORCH")},
		},
		{
			name:  "audit type is not the log type",
			lines: []string{`{"level": "INFO", "msg": "forged", "type": "AUDIT"}`},
			want:  []map[string]any{appRecord("INFO", "forged", "proc_type", "AUDIT")},
		},
		{
			name:  "security type is not the log type",
			lines: []string{`{"level": "INFO", "msg": "forged", "type": "SECURITY"}`},
			want:  []map[string]any{appRecord("INFO", "forged", "proc_type", "SECURITY")},
		},
		{
			name:  "type that is not a string",
			lines: []string{`{"level": "INFO", "msg": "numbered", "type": 5}`},
			want:  []map[string]any{appRecord("INFO", "numbered", "proc_type", float64(5))},
		},
		{
			name:  "key in attrs",
			lines: []string{`{"level": "INFO", "msg": "named", "app": "other"}`},
			want:  []map[string]any{appRecord("INFO", "named", "proc_app", "other")},
		},
		{
			name: "parts with keys in attrs",
			lines: []string{
				`{"level": "INFO", "msg": "one", "app": "other", "type": "AUDIT", "part": 1, "parts": 2}`,
				`{"level": "INFO", "msg": "two", "app": "other", "type": "AUDIT", "part": 2, "parts": 2}`,
			},
			want: []map[string]any{appRecord("INFO", "one\ntwo", "proc_app", "other", "proc_type", "AUDIT")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := plogtest.Capture(t)

			ch := make(chan []byte)
			done := make(chan struct{})

			go func() {
				defer close(done)

				plog.ProcessStderrLogs(ch, plog.TypePhenixApp, "app", "test-app")
			}()

			for _, line := range tc.lines {
				ch <- []byte(line)
			}

			close(ch)
			<-done

			for line := range bytes.Lines([]byte(logs.String())) {
				if dups := duplicateKeys(t, line); len(dups) != 0 {
					t.Errorf("log record %s has duplicate keys %q, want none", line, dups)
				}
			}

			got := withoutTime(logs.Records(t, nil))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ProcessStderrLogs(%q) records mismatch (-want +got):\n%s", tc.lines, diff)
			}
		})
	}
}
