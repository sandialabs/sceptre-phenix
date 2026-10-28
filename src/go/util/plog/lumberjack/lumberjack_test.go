package lumberjack

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// useTime makes currentTime give now for the rest of the test.
func useTime(t *testing.T, now time.Time) {
	t.Helper()

	original := currentTime
	t.Cleanup(func() { currentTime = original })

	currentTime = func() time.Time { return now }
}

// timeFromName gives an error, and does not panic, for a name that has the
// prefix and the extension but is shorter than both together.
func TestTimeFromNameShortName(t *testing.T) {
	l := &Logger{Filename: filepath.Join(t.TempDir(), "a..-")}
	prefix, ext := l.prefixAndExt()

	if prefix != "a.-" || ext != ".-" {
		t.Fatalf("prefixAndExt() = %q, %q, want %q, %q", prefix, ext, "a.-", ".-")
	}

	if got, err := l.timeFromName("a.-", prefix, ext); err == nil {
		t.Errorf("timeFromName(%q, %q, %q) = %v, nil, want an error", "a.-", prefix, ext, got)
	}
}

// timeFromName reads back the time in the name that backupName gives. The
// clock is in UTC, so the local and UTC names hold the same time.
func TestTimeFromNameRoundTrip(t *testing.T) {
	now := time.Date(2016, 11, 4, 18, 30, 0, 123_000_000, time.UTC)
	useTime(t, now)

	for _, name := range []string{"server.log", "server", "a.b.log", ".log", "a..-"} {
		for _, local := range []bool{false, true} {
			l := &Logger{Filename: filepath.Join(t.TempDir(), name)}
			prefix, ext := l.prefixAndExt()
			backup := filepath.Base(backupName(l.Filename, local))

			got, err := l.timeFromName(backup, prefix, ext)
			if err != nil {
				t.Errorf("timeFromName(%q, %q, %q) error = %v, want nil", backup, prefix, ext, err)

				continue
			}

			if !got.Equal(now) {
				t.Errorf("timeFromName(%q, %q, %q) = %s, want %s", backup, prefix, ext, got, now)
			}
		}
	}
}

// useMegabyte makes a megabyte n bytes for the rest of the test, so a small
// write rotates the log file.
func useMegabyte(t *testing.T, n int) {
	t.Helper()

	original := megabyte
	t.Cleanup(func() { megabyte = original })

	megabyte = n
}

// The limits change while other goroutines write and rotate the log file and
// the mill removes old files. Run with -race: an unlocked read or write of a
// limit is a data race. MaxAge stays 0, so the mill, which outlives the test,
// does not read currentTime while another test changes it.
func TestLimitsChangeWhileWriting(t *testing.T) {
	useMegabyte(t, 1)

	dir := t.TempDir()
	l := &Logger{Filename: filepath.Join(dir, "phenix.log"), MaxSize: 64, MaxBackups: 2}

	t.Cleanup(func() { _ = l.Close() })

	line := append(bytes.Repeat([]byte("x"), 31), '\n')

	const (
		writers = 4
		writes  = 200
	)

	var wg sync.WaitGroup

	for range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range writes {
				if _, err := l.Write(line); err != nil {
					t.Errorf("Write() error = %v", err)

					return
				}
			}
		}()
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		for i := range writes {
			l.SetMaxSize(32 + i%64)
			l.SetMaxBackups(1 + i%3)
			l.SetMaxAge(0)
			_ = l.Limits()
		}
	}()

	wg.Wait()

	l.SetMaxSize(48)
	l.SetMaxBackups(5)

	want := Limits{MaxSize: 48, MaxAge: 0, MaxBackups: 5}
	if diff := cmp.Diff(want, l.Limits()); diff != "" {
		t.Errorf("Limits() after the last changes mismatch (-want +got):\n%s", diff)
	}

	if _, err := os.Stat(l.Filename); err != nil {
		t.Errorf("log file after the writes: %v", err)
	}
}

// A write longer than MaxSize is refused with the size set by SetMaxSize.
func TestSetMaxSizeLimitsWrite(t *testing.T) {
	useMegabyte(t, 1)

	l := &Logger{Filename: filepath.Join(t.TempDir(), "phenix.log"), MaxSize: 100}

	t.Cleanup(func() { _ = l.Close() })

	if _, err := l.Write(make([]byte, 50)); err != nil {
		t.Fatalf("Write(50 bytes) with MaxSize 100 error = %v", err)
	}

	l.SetMaxSize(10)

	if n, err := l.Write(make([]byte, 50)); err == nil {
		t.Errorf("Write(50 bytes) after SetMaxSize(10) = %d, nil; want an error", n)
	}
}
