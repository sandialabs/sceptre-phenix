package plog

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lmittmann/tint"
)

// consoleLine logs msg with log to a console handler colored as color says,
// and returns the line it wrote without its time stamp.
func consoleLine(t *testing.T, color bool, log func(LogType, string, ...any), msg string) string {
	t.Helper()

	var out bytes.Buffer

	NewPhenixHandler(&out, color)
	t.Cleanup(func() { NewPhenixHandler(os.Stderr, false) })

	log(TypeSystem, msg, "k", "v")

	line := strings.TrimSuffix(out.String(), "\n")
	if _, rest, ok := strings.Cut(line, " "); ok {
		if _, rest, ok = strings.Cut(rest, " "); ok {
			return rest
		}
	}

	return line
}

// A colored console shows a warning's message yellow, and an error's or an
// alarm's message red; a plain console has no escape codes, and an alarm is a
// warning there.
func TestConsoleColors(t *testing.T) { //nolint:paralleltest // replaces the console handler
	tests := map[string]struct {
		color bool
		log   func(LogType, string, ...any)
		want  string
	}{
		"plain info":    {color: false, log: Info, want: "INF hello type=SYSTEM k=v"},
		"plain warning": {color: false, log: Warn, want: "WRN hello type=SYSTEM k=v"},
		"plain alarm":   {color: false, log: WarnAlarm, want: "WRN hello type=SYSTEM k=v"},
		"colored info": {
			color: true, log: Info,
			want: "\x1b[92mINF\x1b[0m hello \x1b[2mtype=\x1b[0mSYSTEM \x1b[2mk=\x1b[0mv",
		},
		"colored warning": {
			color: true, log: Warn,
			want: "\x1b[93mWRN\x1b[0m \x1b[33mhello\x1b[0m \x1b[2mtype=\x1b[0mSYSTEM \x1b[2mk=\x1b[0mv",
		},
		"colored alarm": {
			color: true, log: WarnAlarm,
			want: "\x1b[93mWRN\x1b[0m \x1b[31mhello\x1b[0m \x1b[2mtype=\x1b[0mSYSTEM \x1b[2mk=\x1b[0mv",
		},
		"colored error": {
			color: true, log: Error,
			want: "\x1b[91mERR\x1b[0m \x1b[31mhello\x1b[0m \x1b[2mtype=\x1b[0mSYSTEM \x1b[2mk=\x1b[0mv",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := consoleLine(t, tc.color, tc.log, "hello"); got != tc.want {
				t.Errorf("console line = %q, want %q", got, tc.want)
			}
		})
	}
}

// The web UI handler shows a tinted attribute, which colors it only on a
// console, by its plain value.
func TestUIHandlerShowsTintedValue(t *testing.T) {
	t.Parallel()

	var got string

	h := NewUIHandler("DEBUG", func(_ time.Time, _, _, message string) { got = message })
	slog.New(h).Info("applied", tint.Attr(2, slog.String("action", "create"))) //nolint:sloglint // a handler of its own

	if want := "applied action=create"; got != want {
		t.Errorf("UI message = %q, want %q", got, want)
	}
}
