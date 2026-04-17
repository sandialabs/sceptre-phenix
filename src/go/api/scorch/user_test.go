//nolint:testpackage // testing internals
package scorch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
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

func TestUserComponentRunRoutesStdoutAndStructuredStderr(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "component.sh")
	script := strings.Join([]string{
		"#!/bin/sh",
		"printf 'component stdout\\n'",
		"printf '{\"level\":\"INFO\",\"msg\":\"component stderr\",\"traceback\":\"trace line\"}\\n' >&2",
		"cat >/dev/null",
	}, "\n") + "\n"

	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("writing temp script: %v", err)
	}

	plog.NewPhenixHandler(io.Discard)

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
	defer plog.RemoveHandler(handlerName)

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

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		mu.Lock()
		count := len(entries)
		mu.Unlock()

		if count >= 2 || time.Now().After(deadline) {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(entries) < 2 {
		t.Fatalf("expected at least 2 log entries, got %d", len(entries))
	}

	var (
		foundScorchLog bool
		foundStdoutLog bool
	)

	for _, entry := range entries {
		if entry.logType == string(plog.TypeScorch) &&
			strings.Contains(entry.msg, "component stderr\ntrace line") &&
			strings.Contains(entry.msg, "component=test-component") {
			foundScorchLog = true
		}

		if entry.logType == string(plog.TypePhenixApp) &&
			strings.Contains(entry.msg, "component stdout") &&
			strings.Contains(entry.msg, "component=test-component") {
			foundStdoutLog = true
		}
	}

	if !foundScorchLog {
		t.Fatalf("did not find routed structured stderr log in %+v", entries)
	}

	if !foundStdoutLog {
		t.Fatalf("did not find routed stdout log in %+v", entries)
	}
}
