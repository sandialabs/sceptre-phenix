package cmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/web"
)

func TestShutdownGracePeriod(t *testing.T) {
	tests := map[string]struct {
		env     string
		want    time.Duration
		wantErr string
	}{
		"default":     {env: "", want: 5 * time.Second},
		"longer":      {env: "30s", want: 30 * time.Second},
		"do not wait": {env: "0", want: 0},
		"negative":    {env: "-1s", wantErr: "must not be negative"},
		"no unit":     {env: "5", wantErr: "missing unit"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", tt.env)
			}

			got, err := shutdownGracePeriod()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("shutdownGracePeriod() with PHENIX_UI_SHUTDOWN_GRACE_PERIOD=%q = %s, %v; want an error containing %q",
						tt.env, got, err, tt.wantErr)
				}

				return
			}

			if err != nil || got != tt.want {
				t.Errorf("shutdownGracePeriod() with PHENIX_UI_SHUTDOWN_GRACE_PERIOD=%q = %s, %v; want %s, nil",
					tt.env, got, err, tt.want)
			}
		})
	}
}

// The first SIGTERM cancels the context with the signal as its cause; the
// second exits at once with 143, as a shell reports a process SIGTERM ended.
func TestNotifyShutdownCancelsOnSignal(t *testing.T) {
	exited := make(chan int, 1)

	ctx, stop := notifyShutdown(context.Background(), func(code int) { exited <- code })
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM to the test: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("notifyShutdown() context not canceled 5s after SIGTERM, want canceled")
	}

	var got web.SignalError
	if !errors.As(context.Cause(ctx), &got) {
		t.Fatalf("context.Cause() after SIGTERM = %v, want a web.SignalError", context.Cause(ctx))
	}

	if diff := cmp.Diff(web.SignalError{Signal: syscall.SIGTERM}, got); diff != "" {
		t.Errorf("context.Cause() after SIGTERM mismatch (-want +got):\n%s", diff)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending a second SIGTERM to the test: %v", err)
	}

	select {
	case code := <-exited:
		if code != 143 {
			t.Errorf("exit code after a second SIGTERM = %d, want 143", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notifyShutdown() did not exit 5s after a second SIGTERM, want exit 143")
	}
}

func TestSignalExitCode(t *testing.T) {
	for sig, want := range map[os.Signal]int{os.Interrupt: 130, syscall.SIGTERM: 143} {
		if got := signalExitCode(sig); got != want {
			t.Errorf("signalExitCode(%s) = %d, want %d", sig, got, want)
		}
	}
}
