//nolint:testpackage // testing internals
package scorch

import (
	"sync"
	"testing"
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
