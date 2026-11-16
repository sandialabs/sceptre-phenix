package notes_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/notes"
	"phenix/util/plog/plogtest"
)

// taken lists, never as nil, the records logged since the last call as "LEVEL message".
func taken(t *testing.T, logs *plogtest.Logs) []string {
	t.Helper()

	records := logs.Take(t, nil)
	out := make([]string, 0, len(records))

	for _, record := range records {
		out = append(out, fmt.Sprintf("%v %v", record["level"], record["msg"]))
	}

	return out
}

func TestFlushes(t *testing.T) {
	var noParent context.Context

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // the context under test
		want bool
	}{
		{name: "flushing context", ctx: notes.Context(context.Background(), true), want: true},
		{name: "keeping context", ctx: notes.Context(context.Background(), false), want: false},
		{name: "nil parent", ctx: notes.Context(noParent, true), want: true},
		{name: "context without notes", ctx: context.Background(), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notes.Flushes(tt.ctx); got != tt.want {
				t.Errorf("Flushes(%s) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

// A context without notes keeps and logs nothing.
func TestContextWithoutNotes(t *testing.T) {
	logs := plogtest.Capture(t)
	ctx := context.Background()

	notes.AddErrors(ctx, true, errors.New("e"))
	notes.AddWarnings(ctx, true, errors.New("w"))
	notes.AddInfo(ctx, true, "i")
	notes.PrettyPrint(ctx, true)

	if got := notes.Warnings(ctx, true); got != nil {
		t.Errorf("Warnings(background, true) = %v, want nil", got)
	}

	if got := notes.Info(ctx, true); got != nil {
		t.Errorf("Info(background, true) = %v, want nil", got)
	}

	if got := taken(t, logs); len(got) != 0 {
		t.Errorf("notes on a context without notes logged %q, want nothing", got)
	}
}

// Each context keeps notes of its own.
func TestContextsAreSeparate(t *testing.T) {
	a := notes.Context(context.Background(), false)
	b := notes.Context(a, false)

	notes.AddInfo(a, false, "a")
	notes.AddInfo(b, false, "b")

	if diff := cmp.Diff([]string{"a"}, notes.Info(a, true)); diff != "" {
		t.Errorf("Info(a, true) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"b"}, notes.Info(b, true)); diff != "" {
		t.Errorf("Info(b, true) mismatch (-want +got):\n%s", diff)
	}
}

// A note is logged at once only when both the context and the call flush. Warnings and Info with all false give each
// note that is not yet logged or read one time; with all true, they give every note each time.
func TestAddFlushAndRead(t *testing.T) {
	for _, tc := range []struct {
		ctxFlush, addFlush bool
	}{
		{ctxFlush: true, addFlush: true},
		{ctxFlush: true, addFlush: false},
		{ctxFlush: false, addFlush: true},
		{ctxFlush: false, addFlush: false},
	} {
		t.Run(fmt.Sprintf("context %t/add %t", tc.ctxFlush, tc.addFlush), func(t *testing.T) {
			logs := plogtest.Capture(t)
			ctx := notes.Context(context.Background(), tc.ctxFlush)
			logged := tc.ctxFlush && tc.addFlush

			w1, w2 := errors.New("warning 1"), errors.New("warning 2")

			notes.AddErrors(ctx, tc.addFlush, errors.New("error 1"))
			notes.AddWarnings(ctx, tc.addFlush, w1, w2)
			notes.AddInfo(ctx, tc.addFlush, "info 1", "info 2")

			wantLogged := []string{}
			if logged {
				wantLogged = []string{"ERROR error 1", "WARN warning 1", "WARN warning 2", "INFO info 1", "INFO info 2"}
			}

			if diff := cmp.Diff(wantLogged, taken(t, logs)); diff != "" {
				t.Errorf("records logged by the Add calls mismatch (-want +got):\n%s", diff)
			}

			wantWarnings, wantInfo := []error{w1, w2}, []string{"info 1", "info 2"}
			if logged {
				wantWarnings, wantInfo = nil, nil
			}

			if diff := cmp.Diff(wantWarnings, notes.Warnings(ctx, false), cmp.Comparer(errors.Is)); diff != "" {
				t.Errorf("first Warnings(ctx, false) mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(wantInfo, notes.Info(ctx, false)); diff != "" {
				t.Errorf("first Info(ctx, false) mismatch (-want +got):\n%s", diff)
			}

			if got := notes.Warnings(ctx, false); got != nil {
				t.Errorf("second Warnings(ctx, false) = %v, want nil", got)
			}

			if got := notes.Info(ctx, false); got != nil {
				t.Errorf("second Info(ctx, false) = %q, want nil", got)
			}

			for range 2 {
				if diff := cmp.Diff([]error{w1, w2}, notes.Warnings(ctx, true), cmp.Comparer(errors.Is)); diff != "" {
					t.Errorf("Warnings(ctx, true) mismatch (-want +got):\n%s", diff)
				}

				if diff := cmp.Diff([]string{"info 1", "info 2"}, notes.Info(ctx, true)); diff != "" {
					t.Errorf("Info(ctx, true) mismatch (-want +got):\n%s", diff)
				}
			}

			if got := taken(t, logs); len(got) != 0 {
				t.Errorf("Warnings and Info logged %q, want nothing", got)
			}
		})
	}
}

// Warnings and Info with all true mark every note as read, so a later read with all false gives only newer notes.
func TestReadAllMarksSeen(t *testing.T) {
	ctx := notes.Context(context.Background(), false)
	old, newer := errors.New("old"), errors.New("newer")

	notes.AddWarnings(ctx, false, old)
	notes.AddInfo(ctx, false, "old")
	_ = notes.Warnings(ctx, true)
	_ = notes.Info(ctx, true)

	notes.AddWarnings(ctx, false, newer)
	notes.AddInfo(ctx, false, "newer")

	if diff := cmp.Diff([]error{newer}, notes.Warnings(ctx, false), cmp.Comparer(errors.Is)); diff != "" {
		t.Errorf("Warnings(ctx, false) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"newer"}, notes.Info(ctx, false)); diff != "" {
		t.Errorf("Info(ctx, false) mismatch (-want +got):\n%s", diff)
	}
}

// PrettyPrint logs errors, then warnings, then info. With all false it skips the notes already logged or read.
func TestPrettyPrint(t *testing.T) {
	logs := plogtest.Capture(t)
	ctx := notes.Context(context.Background(), true)

	notes.AddErrors(ctx, true, errors.New("logged error"))
	notes.AddErrors(ctx, false, errors.New("kept error"))
	notes.AddWarnings(ctx, false, errors.New("read warning"))
	notes.AddInfo(ctx, true, "logged info")
	notes.AddInfo(ctx, false, "kept info")
	notes.AddWarnings(ctx, false, errors.New("kept warning"))

	_ = taken(t, logs)

	notes.AddWarnings(ctx, false) // no notes adds nothing

	if diff := cmp.Diff([]string{"read warning", "kept warning"}, errorTexts(notes.Warnings(ctx, false))); diff != "" {
		t.Fatalf("Warnings(ctx, false) mismatch (-want +got):\n%s", diff)
	}

	notes.AddWarnings(ctx, false, errors.New("late warning"))

	notes.PrettyPrint(ctx, false)

	want := []string{"ERROR kept error", "WARN late warning", "INFO kept info"}
	if diff := cmp.Diff(want, taken(t, logs)); diff != "" {
		t.Errorf("PrettyPrint(ctx, false) logged mismatch (-want +got):\n%s", diff)
	}

	notes.PrettyPrint(ctx, true)

	want = []string{
		"ERROR logged error", "ERROR kept error",
		"WARN read warning", "WARN kept warning", "WARN late warning",
		"INFO logged info", "INFO kept info",
	}
	if diff := cmp.Diff(want, taken(t, logs)); diff != "" {
		t.Errorf("PrettyPrint(ctx, true) logged mismatch (-want +got):\n%s", diff)
	}
}

func errorTexts(errs []error) []string {
	out := make([]string, 0, len(errs))

	for _, err := range errs {
		out = append(out, err.Error())
	}

	return out
}
