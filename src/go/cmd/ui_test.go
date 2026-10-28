package cmd

import (
	"context"
	"errors"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"
	"pgregory.net/rapid"

	"phenix/web"
)

// ui.shutdown-grace-period is a whole number of seconds from the environment
// variable, a flag or the config file. A duration string, a fraction, a
// negative or an overflowing value is refused; the flag parser refuses a
// value that is not an integer before the server starts.
func TestUIShutdownGracePeriod(t *testing.T) {
	tests := map[string]struct {
		env, arg, config string
		want             time.Duration
		wantErr          string
		parseErr         bool
	}{
		"default":            {want: 5 * time.Second},
		"env":                {env: "7", want: 7 * time.Second},
		"env do not wait":    {env: "0", want: 0},
		"env duration":       {env: "5s", wantErr: "whole number of seconds"},
		"env fraction":       {env: "1.5", wantErr: "whole number of seconds"},
		"env word":           {env: "abc", wantErr: "whole number of seconds"},
		"env blank":          {env: " ", wantErr: "whole number of seconds"},
		"env negative":       {env: "-1", wantErr: "must not be negative"},
		"env overflows":      {env: "9223372036854775808", wantErr: "at most"},
		"env too large":      {env: "9300000000", wantErr: "at most"},
		"env very negative":  {env: "-9223372036854775809", wantErr: "must not be negative"},
		"env largest":        {env: "9223372036", want: 9223372036 * time.Second},
		"env names setting":  {env: "5s", wantErr: "ui.shutdown-grace-period"},
		"flag":               {arg: "7", want: 7 * time.Second},
		"flag do not wait":   {arg: "0", want: 0},
		"flag negative":      {arg: "-1", wantErr: "must not be negative"},
		"flag duration":      {arg: "5s", parseErr: true},
		"flag fraction":      {arg: "1.5", parseErr: true},
		"config integer":     {config: "7", want: 7 * time.Second},
		"config string":      {config: `"7"`, want: 7 * time.Second},
		"config do not wait": {config: "0", want: 0},
		"config duration":    {config: "5s", wantErr: "whole number of seconds"},
		"config fraction":    {config: "1.5", wantErr: "whole number of seconds"},
		"config boolean":     {config: "true", wantErr: "whole number of seconds"},
		"config empty":       {config: `""`, wantErr: "whole number of seconds"},
		"config negative":    {config: "-3", wantErr: "must not be negative"},
	}

	flag := uiCmd.Flags().Lookup("shutdown-grace-period")

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v, input := viper.GetViper(), "the default"

			switch {
			case tt.env != "":
				t.Setenv("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", tt.env)
				input = "PHENIX_UI_SHUTDOWN_GRACE_PERIOD=" + tt.env
			case tt.arg != "":
				t.Cleanup(func() {
					_ = flag.Value.Set(flag.DefValue)
					flag.Changed = false
				})

				input = "--shutdown-grace-period=" + tt.arg

				err := uiCmd.Flags().Parse([]string{input})
				if tt.parseErr {
					if err == nil {
						t.Errorf("parsing %s succeeded, want an error", input)
					}

					return
				}

				if err != nil {
					t.Fatalf("parsing %s: %v", input, err)
				}
			case tt.config != "":
				v = viper.New()
				v.SetConfigType("yaml")

				input = "shutdown-grace-period: " + tt.config
				if err := v.ReadConfig(strings.NewReader("ui:\n  " + input + "\n")); err != nil {
					t.Fatalf("reading config %q: %v", input, err)
				}
			}

			got, err := shutdownGracePeriod(v)
			checkGracePeriod(t, input, got, err, tt.want, tt.wantErr)
		})
	}
}

// uiParseModel is the setting's value for the decimal text s, and the
// error it gives, from its value alone: negative or above maxValue fails.
func uiParseModel(s string, maxValue int64) (int64, string) {
	n, ok := new(big.Int).SetString(s, 10)

	switch {
	case !ok:
		return 0, "whole number of"
	case n.Sign() < 0:
		return 0, "must not be negative"
	case n.Cmp(big.NewInt(maxValue)) > 0:
		return 0, "at most"
	}

	return n.Int64(), ""
}

// uiSettingText draws the text of a numeric setting: a decimal number of any
// size, or text that is not one.
func uiSettingText(t *rapid.T) string {
	return rapid.OneOf(
		rapid.Map(rapid.Int64(), func(n int64) string { return strconv.FormatInt(n, 10) }),
		rapid.StringMatching(`-?[0-9]{1,30}`),
		rapid.StringMatching(`[0-9]{0,3}[a-z. _][0-9a-z]{0,4}`),
	).Draw(t, "value")
}

// Whatever text ui.shutdown-grace-period and ui.netflow.max-size hold, a
// number from 0 to the largest that fits is read in seconds or MiB, and any
// other text fails with the reason its value gives.
func TestUISettingParseProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		value := uiSettingText(t)
		v := viper.New()
		v.Set("ui.shutdown-grace-period", value)
		v.Set("ui.netflow.max-size", value)

		seconds, wantErr := uiParseModel(value, maxShutdownGraceSeconds)

		grace, err := shutdownGracePeriod(v)
		if wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("shutdownGracePeriod(%q) = %s, %v; want an error containing %q", value, grace, err, wantErr)
			}
		} else if err != nil || grace != time.Duration(seconds)*time.Second {
			t.Fatalf("shutdownGracePeriod(%q) = %s, %v; want %ds", value, grace, err, seconds)
		}

		mib, wantErr := uiParseModel(value, maxNetflowMaxSizeMiB)

		size, err := netflowMaxSize(v)
		if wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("netflowMaxSize(%q) = %d, %v; want an error containing %q", value, size, err, wantErr)
			}
		} else if err != nil || size != mib<<20 {
			t.Fatalf("netflowMaxSize(%q) = %d, %v; want %d MiB", value, size, err, mib)
		}
	})
}

// phenix ui refuses a bad ui.shutdown-grace-period or ui.netflow.max-size
// before it starts anything.
func TestUIRunRefusesBadSettings(t *testing.T) {
	tests := map[string]struct {
		grace, netflow string
		wantErr        string
	}{
		"grace period": {grace: "5s", wantErr: `invalid ui.shutdown-grace-period "5s"`},
		"netflow size": {grace: "5", netflow: "-1", wantErr: `invalid ui.netflow.max-size "-1"`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PHENIX_UI_SHUTDOWN_GRACE_PERIOD", tt.grace)
			t.Setenv("PHENIX_UI_NETFLOW_MAX_SIZE", tt.netflow)

			err := uiCmd.RunE(uiCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("phenix ui error = %v, want one containing %q", err, tt.wantErr)
			}
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

// stop cancels the context without a signal, and a later call, or many at
// once, does nothing more. A canceled parent cancels the context too.
func TestUINotifyShutdownStop(t *testing.T) {
	exited := make(chan int, 1)
	exit := func(code int) { exited <- code }

	ctx, stop := notifyShutdown(context.Background(), exit)
	stop()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("notifyShutdown() context not canceled by stop, want canceled")
	}

	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		t.Errorf("context.Cause() after stop = %v, want %v", cause, context.Canceled)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()
			stop()
		}()
	}

	wg.Wait()

	parent, cancel := context.WithCancel(context.Background())
	ctx, stop = notifyShutdown(parent, exit)

	defer stop()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("notifyShutdown() context not canceled 5s after its parent, want canceled")
	}

	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		t.Errorf("context.Cause() after the parent ends = %v, want %v", cause, context.Canceled)
	}

	select {
	case code := <-exited:
		t.Errorf("notifyShutdown() called exit(%d) with no signal", code)
	default:
	}
}

// After the first signal, stop ends the wait for a second one without an
// exit.
func TestUINotifyShutdownStopAfterSignal(t *testing.T) {
	exited := make(chan int, 1)

	ctx, stop := notifyShutdown(context.Background(), func(code int) { exited <- code })

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		stop()
		t.Fatalf("sending SIGINT to the test: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		stop()
		t.Fatal("notifyShutdown() context not canceled 5s after SIGINT, want canceled")
	}

	stop()

	var got web.SignalError
	if !errors.As(context.Cause(ctx), &got) || got.Signal != os.Interrupt {
		t.Errorf("context.Cause() after SIGINT = %v, want a web.SignalError for %s", context.Cause(ctx), os.Interrupt)
	}

	select {
	case code := <-exited:
		t.Errorf("notifyShutdown() called exit(%d) after stop, want no exit", code)
	case <-time.After(50 * time.Millisecond):
	}
}

// uiTestSignal is an [os.Signal] that is not a [syscall.Signal].
type uiTestSignal struct{}

func (uiTestSignal) String() string { return "test" }
func (uiTestSignal) Signal()        {}

// A signal exits with 128 plus its number, as a shell reports it, and a
// signal without a number exits with 1.
func TestSignalExitCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sig  os.Signal
		want int
	}{
		"interrupt":   {sig: os.Interrupt, want: 130},
		"terminate":   {sig: syscall.SIGTERM, want: 143},
		"kill":        {sig: syscall.SIGKILL, want: 137},
		"hang up":     {sig: syscall.SIGHUP, want: 129},
		"not numeric": {sig: uiTestSignal{}, want: 1},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := signalExitCode(tt.sig); got != tt.want {
				t.Errorf("signalExitCode(%s) = %d, want %d", tt.sig, got, tt.want)
			}
		})
	}
}

// ui.netflow.max-size is a whole number of MiB from the environment, a flag or
// the config file; a negative, non-numeric or overflowing value is refused.
func TestNetflowMaxSize(t *testing.T) {
	const maxMiB = int64(1<<43 - 1)

	tests := map[string]struct {
		env, arg, config string
		want             int64
		wantErr          string
	}{
		"default":          {want: 100 << 20},
		"env":              {env: "7", want: 7 << 20},
		"env off":          {env: "0", want: 0},
		"env fraction":     {env: "1.5", wantErr: "whole number of megabytes"},
		"env word":         {env: "lots", wantErr: "whole number of megabytes"},
		"env negative":     {env: "-1", wantErr: "must not be negative"},
		"env largest":      {env: "8796093022207", want: maxMiB << 20},
		"env too large":    {env: "8796093022208", wantErr: "at most"},
		"env overflows":    {env: "9223372036854775808", wantErr: "at most"},
		"flag":             {arg: "250", want: 250 << 20},
		"flag negative":    {arg: "-5", wantErr: "must not be negative"},
		"config":           {config: "12", want: 12 << 20},
		"config string":    {config: `"12"`, want: 12 << 20},
		"config duration":  {config: "5s", wantErr: "whole number of megabytes"},
		"config negative":  {config: "-3", wantErr: "must not be negative"},
		"names setting":    {env: "x", wantErr: "ui.netflow.max-size"},
		"config overflows": {config: "9300000000000", wantErr: "at most"},
	}

	flag := uiCmd.Flags().Lookup("netflow.max-size")

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			v := viper.GetViper()

			switch {
			case tt.env != "":
				t.Setenv("PHENIX_UI_NETFLOW_MAX_SIZE", tt.env)
			case tt.arg != "":
				t.Cleanup(func() {
					_ = flag.Value.Set(flag.DefValue)
					flag.Changed = false
				})

				if err := uiCmd.Flags().Parse([]string{"--netflow.max-size=" + tt.arg}); err != nil {
					t.Fatalf("parsing --netflow.max-size=%s: %v", tt.arg, err)
				}
			case tt.config != "":
				v = viper.New()
				v.SetConfigType("yaml")

				config := "ui:\n  netflow:\n    max-size: " + tt.config + "\n"
				if err := v.ReadConfig(strings.NewReader(config)); err != nil {
					t.Fatalf("reading config %q: %v", config, err)
				}
			}

			got, err := netflowMaxSize(v)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("netflowMaxSize() = %d, %v; want an error containing %q", got, err, tt.wantErr)
				}

				return
			}

			if err != nil || got != tt.want {
				t.Errorf("netflowMaxSize() = %d, %v; want %d, nil", got, err, tt.want)
			}
		})
	}
}
