package plog

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

type uiEntry struct {
	LogType string
	Message string
}

func newTestUIHandler(entries *[]uiEntry) slog.Handler {
	return NewUIHandler("info", func(_ time.Time, _, logtype, message string) {
		*entries = append(*entries, uiEntry{LogType: logtype, Message: message})
	})
}

// TestUIHandlerReadsBoundType checks that the UI handler takes the log type
// from an attribute bound with With, that a type on the record takes
// precedence, and that the type is not repeated in the message.
func TestUIHandlerReadsBoundType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		log  func(*slog.Logger)
		want uiEntry
	}{
		{
			name: "bound type",
			log: func(l *slog.Logger) {
				l.With(logAttrType, TypeSoh).With("exp", "exp-1").Info("no flow data found", "host", "rtu")
			},
			want: uiEntry{LogType: string(TypeSoh), Message: "no flow data found exp=exp-1 host=rtu"},
		},
		{
			name: "record type takes precedence",
			log: func(l *slog.Logger) {
				l.With(logAttrType, TypeSoh).Info("component done", logAttrType, TypeScorch)
			},
			want: uiEntry{LogType: string(TypeScorch), Message: "component done"},
		},
		{
			name: "no type",
			log:  func(l *slog.Logger) { l.Info("started") },
			want: uiEntry{LogType: string(TypeSystem), Message: "started"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var entries []uiEntry

			tt.log(slog.New(newTestUIHandler(&entries)))

			if diff := cmp.Diff([]uiEntry{tt.want}, entries); diff != "" {
				t.Errorf("UI log entries mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUIHandlerKeepsSiblingAttrs checks that two loggers made from one parent
// each keep their own attributes when the parent's attributes have spare
// capacity.
func TestUIHandlerKeepsSiblingAttrs(t *testing.T) {
	t.Parallel()

	var entries []uiEntry

	parent := slog.New(newTestUIHandler(&entries)).With("a", 1).With("b", 2).With("c", 3)
	first := parent.With("host", "first")
	_ = parent.With("host", "second")

	first.Info("check")

	want := []uiEntry{{LogType: string(TypeSystem), Message: "check a=1 b=2 c=3 host=first"}}
	if diff := cmp.Diff(want, entries); diff != "" {
		t.Errorf("UI log entries mismatch (-want +got):\n%s", diff)
	}
}

// TestScorchSohHandlerKeepsSiblingAttrs checks that two loggers made from one
// parent each keep their own attributes in the scorch SoH output.
func TestScorchSohHandlerKeepsSiblingAttrs(t *testing.T) {
	t.Parallel()

	var outputs []string

	h := NewScorchSohHandler("exp-1-0-soh", "info", func(out string) { outputs = append(outputs, out) })
	parent := slog.New(h).With(ScorchSohKey, "exp-1-0-soh").With("b", 2).With("c", 3)
	first := parent.With("host", "first")
	_ = parent.With("host", "second")

	first.Info("check")

	if len(outputs) != 1 {
		t.Fatalf("scorch SoH outputs = %q, want 1", outputs)
	}

	if want := "] check b=2 c=3 host=first\n"; !strings.HasSuffix(outputs[0], want) {
		t.Errorf("scorch SoH output = %q, want suffix %q", outputs[0], want)
	}
}
