//nolint:testpackage // testing internals
package scorch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
	"phenix/util/polltest"
)

func TestProcessLogChannel(t *testing.T) {
	type logEntry struct {
		level string
		msg   string
	}

	var (
		captured []logEntry
		mu       sync.Mutex
		wg       sync.WaitGroup
	)

	// Mock logger callback
	logFn := func(level, msg string) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, logEntry{level, msg})
		wg.Done()
	}

	ch := make(chan []byte)

	// Start processor
	go processLogChannel(ch, logFn)

	// 1. Test JSON log (Immediate flush)
	wg.Add(1)
	ch <- []byte(`{"level":"INFO","msg":"json message"}`)
	wg.Wait()

	mu.Lock()
	if len(captured) != 1 {
		t.Fatalf("expected 1 log, got %d", len(captured))
	}
	if captured[0].msg != "json message" || captured[0].level != "INFO" {
		t.Errorf("unexpected json log: %+v", captured[0])
	}
	mu.Unlock()

	// 2. Test Multi-line Text buffering (Wait for timeout)
	// We expect these 3 lines to be combined into 1 log entry
	wg.Add(1)
	ch <- []byte("Traceback (most recent call last):")
	ch <- []byte("  File \"app.py\", line 10, in <module>")
	ch <- []byte("ValueError: something went wrong")

	// Wait for the 10ms buffer timer to expire
	wg.Wait()

	mu.Lock()
	if len(captured) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(captured))
	}
	expectedMultiLine := "Traceback (most recent call last):\n  File \"app.py\", line 10, in <module>\nValueError: something went wrong"
	if captured[1].msg != expectedMultiLine {
		t.Errorf("expected multiline log:\n%q\ngot:\n%q", expectedMultiLine, captured[1].msg)
	}
	mu.Unlock()

	// 3. Test Text followed by JSON (JSON forces flush of text)
	wg.Add(2) // 1 for the text flush, 1 for the json log
	ch <- []byte("some stray text log")
	ch <- []byte(`{"level":"ERROR","msg":"fatal error"}`)
	wg.Wait()

	mu.Lock()
	if len(captured) != 4 {
		t.Fatalf("expected 4 logs, got %d", len(captured))
	}
	if captured[2].msg != "some stray text log" {
		t.Errorf("unexpected text log: %s", captured[2].msg)
	}
	if captured[3].msg != "fatal error" || captured[3].level != "ERROR" {
		t.Errorf("unexpected json log: %+v", captured[3])
	}
	mu.Unlock()

	close(ch)
}

func TestProcessLogChannelAppendsTraceback(t *testing.T) {
	type logEntry struct {
		level string
		msg   string
	}

	var (
		captured []logEntry
		mu       sync.Mutex
		wg       sync.WaitGroup
	)

	logFn := func(level, msg string) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, logEntry{level, msg})
		wg.Done()
	}

	ch := make(chan []byte)
	go processLogChannel(ch, logFn)

	wg.Add(1)
	ch <- []byte(`{"level":"ERROR","msg":"component failed","traceback":"line one\nline two"}`)
	wg.Wait()
	close(ch)

	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 1 {
		t.Fatalf("expected 1 log, got %d", len(captured))
	}

	if captured[0].level != levelError {
		t.Fatalf("expected level %q, got %q", levelError, captured[0].level)
	}

	want := "component failed\nline one\nline two"
	if captured[0].msg != want {
		t.Fatalf("expected message %q, got %q", want, captured[0].msg)
	}
}

func TestProcessLogChannelTreatsInvalidJSONAsText(t *testing.T) {
	type logEntry struct {
		level string
		msg   string
	}

	var (
		captured []logEntry
		mu       sync.Mutex
		wg       sync.WaitGroup
	)

	logFn := func(level, msg string) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, logEntry{level, msg})
		wg.Done()
	}

	ch := make(chan []byte)
	go processLogChannel(ch, logFn)

	wg.Add(1)
	ch <- []byte(`{"level":"INFO","msg":`)
	close(ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 1 {
		t.Fatalf("expected 1 log, got %d", len(captured))
	}

	if captured[0].level != levelInfo {
		t.Fatalf("expected level %q, got %q", levelInfo, captured[0].level)
	}

	if captured[0].msg != `{"level":"INFO","msg":` {
		t.Fatalf("unexpected message %q", captured[0].msg)
	}
}

// processLogChannel gives a text entry the level of the "| LEVEL |" marker in
// it (INFO without one), and treats a JSON line without a level as text.
func TestProcessLogChannelLevels(t *testing.T) {
	tests := map[string]struct {
		line      string
		wantLevel string
		wantMsg   string
	}{
		"ERROR marker":       {line: "12:00 | ERROR | broke", wantLevel: levelError},
		"ERR marker":         {line: "12:00 | ERR | broke", wantLevel: levelError},
		"WARN marker":        {line: "12:00 | WARN | odd", wantLevel: levelWarn},
		"WARNING marker":     {line: "12:00 | WARNING | odd", wantLevel: levelWarn},
		"DEBUG marker":       {line: "12:00 | DEBUG | detail", wantLevel: levelDebug},
		"DBG marker":         {line: "12:00 | DBG | detail", wantLevel: levelDebug},
		"no marker":          {line: "plain text", wantLevel: levelInfo},
		"marker needs bars":  {line: "ERROR without bars", wantLevel: levelInfo},
		"JSON without level": {line: `{"msg":"no level"}`, wantLevel: levelInfo},
		"JSON with level":    {line: `{"level":"WARN","msg":"structured"}`, wantLevel: levelWarn, wantMsg: "structured"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			type logEntry struct {
				Level string
				Msg   string
			}

			var got []logEntry

			ch := make(chan []byte)
			done := make(chan struct{})

			go func() {
				defer close(done)

				processLogChannel(ch, func(level, msg string) { got = append(got, logEntry{level, msg}) })
			}()

			ch <- []byte(tc.line)
			close(ch)
			<-done

			wantMsg := tc.wantMsg
			if wantMsg == "" {
				wantMsg = tc.line
			}

			want := []logEntry{{Level: tc.wantLevel, Msg: wantMsg}}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("processLogChannel(%q) entries mismatch (-want +got):\n%s", tc.line, diff)
			}
		})
	}
}

// Lines that the phenix-apps Python logger writes for a message with three
// lines and for a message with a traceback in two parts.
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

	pyJoined = "executing command '#!/bin/vbash\nconfigure\n' in VM INT-fw"
)

// processLogChannel gives the parts of a multi-part message as one entry, and
// gives what it collected when the part sequence breaks or the channel closes.
func TestProcessLogChannelJoinsParts(t *testing.T) {
	type logEntry struct {
		Level string
		Msg   string
	}

	tests := []struct {
		name  string
		lines []string
		want  []logEntry
	}{
		{
			name:  "three lines",
			lines: []string{pyPart1, pyPart2, pyPart3},
			want:  []logEntry{{Level: levelInfo, Msg: pyJoined}},
		},
		{
			name:  "message and traceback",
			lines: []string{pyErrMsg, pyErrTB1, pyErrTB2},
			want:  []logEntry{{Level: levelError, Msg: "component failed\nTraceback (most recent call last):\nValueError: bad"}},
		},
		{
			name:  "record without parts interrupts",
			lines: []string{pyPart1, pyPart2, `{"level":"WARN","msg":"other"}`, pyPart3},
			want: []logEntry{
				{Level: levelInfo, Msg: "executing command '#!/bin/vbash\nconfigure"},
				{Level: levelWarn, Msg: "other"},
				{Level: levelInfo, Msg: "' in VM INT-fw"},
			},
		},
		{
			name:  "text line interrupts",
			lines: []string{pyPart1, "12:00 | ERROR | stray", pyPart2, pyPart3},
			want: []logEntry{
				{Level: levelInfo, Msg: "executing command '#!/bin/vbash"},
				{Level: levelError, Msg: "12:00 | ERROR | stray"},
				{Level: levelInfo, Msg: "configure\n' in VM INT-fw"},
			},
		},
		{
			name:  "restarted numbering interrupts",
			lines: []string{pyPart1, pyPart1, pyPart2, pyPart3},
			want: []logEntry{
				{Level: levelInfo, Msg: "executing command '#!/bin/vbash"},
				{Level: levelInfo, Msg: pyJoined},
			},
		},
		{
			name:  "channel closes mid-sequence",
			lines: []string{pyPart1, pyPart2},
			want:  []logEntry{{Level: levelInfo, Msg: "executing command '#!/bin/vbash\nconfigure"}},
		},
		{
			name:  "single record unchanged",
			lines: []string{`{"level":"ERROR","msg":"one","traceback":"trace"}`},
			want:  []logEntry{{Level: levelError, Msg: "one\ntrace"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []logEntry

			ch := make(chan []byte)
			done := make(chan struct{})

			go func() {
				defer close(done)

				processLogChannel(ch, func(level, msg string) { got = append(got, logEntry{level, msg}) })
			}()

			for _, line := range tc.lines {
				ch <- []byte(line)
			}

			close(ch)
			<-done

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("processLogChannel(%q) entries mismatch (-want +got):\n%s", tc.lines, diff)
			}
		})
	}
}

// processLogChannel gives the collected parts when the next part does not
// come within the flush interval.
func TestProcessLogChannelFlushesPartsAfterInterval(t *testing.T) {
	var (
		got []string
		mu  sync.Mutex
	)

	ch := make(chan []byte)
	done := make(chan struct{})

	go func() {
		defer close(done)

		processLogChannel(ch, func(_, msg string) {
			mu.Lock()
			defer mu.Unlock()

			got = append(got, msg)
		})
	}()

	ch <- []byte(pyPart1)

	polltest.Until(t, "the first part given alone", func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(got) == 1
	})

	ch <- []byte(pyPart2)
	ch <- []byte(pyPart3)

	close(ch)
	<-done

	want := []string{"executing command '#!/bin/vbash", "configure\n' in VM INT-fw"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("processLogChannel() entries mismatch (-want +got):\n%s", diff)
	}
}

// run logs the stdout of a component as a phenix app entry, and each
// structured stderr line as a scorch entry at the level that the line names.
func TestUserComponentRunRoutesStdoutAndStructuredStderr(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "component.sh")
	script := strings.Join([]string{
		"#!/bin/sh",
		"printf 'component stdout\\n'",
		"printf '{\"level\":\"INFO\",\"msg\":\"component stderr\",\"traceback\":\"trace line\"}\\n' >&2",
		"printf '{\"level\":\"ERR\",\"msg\":\"short error\"}\\n' >&2",
		"printf '{\"level\":\"WARNING\",\"msg\":\"long warning\"}\\n' >&2",
		"printf '{\"level\":\"DBG\",\"msg\":\"short debug\"}\\n' >&2",
		"printf '{\"level\":\"INFO\",\"msg\":\"part one\",\"part\":1,\"parts\":2}\\n' >&2",
		"printf '{\"level\":\"INFO\",\"msg\":\"part two\",\"part\":2,\"parts\":2}\\n' >&2",
		"cat >/dev/null",
	}, "\n") + "\n"

	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("writing temp script: %v", err)
	}

	type logEntry struct {
		level   string
		logType string
		msg     string
	}

	var (
		entries []logEntry
		mu      sync.Mutex
	)

	handlerName := "test-user-component-run"
	plog.AddHandler(handlerName, plog.NewUIHandler("DEBUG", func(_ time.Time, level, logType, message string) {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, logEntry{level: level, logType: logType, msg: message})
	}))
	t.Cleanup(func() { plog.RemoveHandler(handlerName) })

	exp := types.NewExperiment(store.ConfigMetadata{Name: "test-exp"})
	exp.Spec.SetExperimentName("test-exp")

	var u UserComponent
	if err := u.Init(
		Name("test-component"),
		Type("test-type"),
		Experiment(*exp),
		RunID(2),
		CurrentLoop(3),
		LoopCount(4),
	); err != nil {
		t.Fatalf("initializing user component: %v", err)
	}

	if err := u.run(context.Background(), ActionStart, scriptPath, []byte(`{"input":"value"}`)); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := []struct {
		level, logType, msg string
	}{
		{"INFO", string(plog.TypeScorch), "component stderr\ntrace line"},
		{"ERROR", string(plog.TypeScorch), "short error"},
		{"WARN", string(plog.TypeScorch), "long warning"},
		{"DEBUG", string(plog.TypeScorch), "short debug"},
		{"INFO", string(plog.TypeScorch), "part one\npart two"},
		{"INFO", string(plog.TypePhenixApp), "component stdout"},
	}

	found := func(level, logType, msg string) bool {
		mu.Lock()
		defer mu.Unlock()

		for _, entry := range entries {
			if entry.level == level && entry.logType == logType &&
				strings.Contains(entry.msg, msg) && strings.Contains(entry.msg, "component=test-component") {
				return true
			}
		}

		return false
	}

	for _, w := range want {
		polltest.Until(t, fmt.Sprintf("a %s %s entry with %q", w.level, w.logType, w.msg), func() bool {
			return found(w.level, w.logType, w.msg)
		})
	}
}
