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
	"github.com/spf13/viper"

	"phenix/web"
)

// A whole number of seconds is accepted from the environment variable; a
// duration string, a fraction, a negative or an overflowing value is refused.
func TestShutdownGracePeriodFromEnv(t *testing.T) {
	tests := map[string]struct {
		env     string
		want    time.Duration
		wantErr string
	}{
		"default":       {env: "", want: 5 * time.Second},
		"seconds":       {env: "7", want: 7 * time.Second},
		"do not wait":   {env: "0", want: 0},
		"duration":      {env: "5s", wantErr: "whole number of seconds"},
		"fraction":      {env: "1.5", wantErr: "whole number of seconds"},
		"word":          {env: "abc", wantErr: "whole number of seconds"},
		"blank":         {env: " ", wantErr: "whole number of seconds"},
		"negative":      {env: "-1", wantErr: "must not be negative"},
		"overflows":     {env: "9223372036854775808", wantErr: "at most"},
		"too large":     {env: "9300000000", wantErr: "at most"},
		"very negative": {env: "-9223372036854775809", wantErr: "must not be negative"},
		"largest":       {env: "9223372036", want: 9223372036 * time.Second},
		"names setting": {env: "5s", wantErr: "ui.shutdown-grace-period"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", tt.env)
			}

			got, err := shutdownGracePeriod(viper.GetViper())
			checkGracePeriod(t, "PHENIX_UI_SHUTDOWN_GRACE_PERIOD="+tt.env, got, err, tt.want, tt.wantErr)
		})
	}
}

// --shutdown-grace-period takes an integer; a negative one parses but is
// refused when the server starts.
func TestShutdownGracePeriodFromFlag(t *testing.T) {
	tests := map[string]struct {
		arg      string
		want     time.Duration
		wantErr  string
		parseErr bool
	}{
		"seconds":     {arg: "7", want: 7 * time.Second},
		"do not wait": {arg: "0", want: 0},
		"negative":    {arg: "-1", wantErr: "must not be negative"},
		"duration":    {arg: "5s", parseErr: true},
		"fraction":    {arg: "1.5", parseErr: true},
	}

	flag := uiCmd.Flags().Lookup("shutdown-grace-period")

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() {
				_ = flag.Value.Set(flag.DefValue)
				flag.Changed = false
			})

			err := uiCmd.Flags().Parse([]string{"--shutdown-grace-period=" + tt.arg})
			if tt.parseErr {
				if err == nil {
					t.Errorf("parsing --shutdown-grace-period=%s succeeded, want an error", tt.arg)
				}

				return
			}

			if err != nil {
				t.Fatalf("parsing --shutdown-grace-period=%s: %v", tt.arg, err)
			}

			got, err := shutdownGracePeriod(viper.GetViper())
			checkGracePeriod(t, "--shutdown-grace-period="+tt.arg, got, err, tt.want, tt.wantErr)
		})
	}
}

// A config file value must be a YAML integer or a string holding one.
func TestShutdownGracePeriodFromConfig(t *testing.T) {
	tests := map[string]struct {
		value   string
		want    time.Duration
		wantErr string
	}{
		"integer":     {value: "7", want: 7 * time.Second},
		"string":      {value: `"7"`, want: 7 * time.Second},
		"do not wait": {value: "0", want: 0},
		"duration":    {value: "5s", wantErr: "whole number of seconds"},
		"fraction":    {value: "1.5", wantErr: "whole number of seconds"},
		"boolean":     {value: "true", wantErr: "whole number of seconds"},
		"empty":       {value: `""`, wantErr: "whole number of seconds"},
		"negative":    {value: "-3", wantErr: "must not be negative"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.SetConfigType("yaml")

			config := "ui:\n  shutdown-grace-period: " + tt.value + "\n"
			if err := v.ReadConfig(strings.NewReader(config)); err != nil {
				t.Fatalf("reading config %q: %v", config, err)
			}

			got, err := shutdownGracePeriod(v)
			checkGracePeriod(t, "shutdown-grace-period: "+tt.value, got, err, tt.want, tt.wantErr)
		})
	}
}

func checkGracePeriod(t *testing.T, input string, got time.Duration, err error, want time.Duration, wantErr string) {
	t.Helper()

	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("shutdownGracePeriod() with %s = %s, %v; want an error containing %q", input, got, err, wantErr)
		}

		return
	}

	if err != nil || got != want {
		t.Errorf("shutdownGracePeriod() with %s = %s, %v; want %s, nil", input, got, err, want)
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
