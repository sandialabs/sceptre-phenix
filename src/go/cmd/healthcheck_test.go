package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestHealthURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		endpoint   string
		tls, ready bool
		want       string
	}{
		{"0.0.0.0:3000", false, false, "http://127.0.0.1:3000/healthz"},
		{":3000", false, true, "http://127.0.0.1:3000/readyz"},
		{"[::]:3443", true, false, "https://[::1]:3443/healthz"},
		{"10.0.0.5:8080", false, false, "http://10.0.0.5:8080/healthz"},
		{"phenix.example.com:443", true, true, "https://phenix.example.com:443/readyz"},
	}

	for _, test := range tests {
		got, err := healthURL(test.endpoint, test.tls, test.ready)
		if err != nil || got != test.want {
			t.Errorf("healthURL(%q, %v, %v) = %q, %v, want %q", test.endpoint, test.tls, test.ready, got, err, test.want)
		}
	}

	if _, err := healthURL("3000", false, false); err == nil {
		t.Error("healthURL(3000) succeeded, want an error for an endpoint without a port")
	}
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"https://127.0.0.1:3000/healthz":    true,
		"https://[::1]:3000/healthz":        true,
		"https://localhost/healthz":         true,
		"https://phenix.example.com/readyz": false,
		"https://10.0.0.5/readyz":           false,
	}

	for target, want := range tests {
		if got := isLoopback(target); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", target, got, want)
		}
	}
}

// runRootHealthcheck runs "phenix healthcheck" through the root command with a temporary store
// endpoint, returning the output, the error and whether the store was created.
func runRootHealthcheck(t *testing.T, args ...string) (string, error, bool) {
	t.Helper()

	storePath := filepath.Join(t.TempDir(), "store.bdb")
	flags := rootCmd.PersistentFlags()
	original := flags.Lookup("store.endpoint").Value.String()

	t.Cleanup(func() {
		_ = flags.Set("store.endpoint", original)

		// cobra keeps flag values between runs of the same command
		if cmd, _, err := rootCmd.Find([]string{"healthcheck"}); err == nil {
			cmd.Flags().VisitAll(func(f *pflag.Flag) {
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			})
		}

		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	var output bytes.Buffer

	rootCmd.SetArgs(append([]string{"healthcheck", "--store.endpoint", "bolt://" + storePath}, args...))
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)

	_, err := rootCmd.ExecuteC()

	_, statErr := os.Stat(storePath)

	return output.String(), err, statErr == nil
}

// healthServer answers every request with code and body.
func healthServer(t *testing.T, code int, body string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/health+json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

//nolint:paralleltest // runs the root command
func TestHealthcheckCommand(t *testing.T) {
	const (
		passing = `{"status":"pass","serviceId":"phenix","description":"phēnix liveness","checks":{}}`
		failing = `{"status":"fail","serviceId":"phenix","description":"phēnix readiness","checks":{` +
			`"minimega:responseTime":[{"componentId":"minimega","status":"fail","time":"2026-01-01T00:00:00Z",` +
			`"output":"minimega did not answer"}],"uptime":[{"status":"pass","time":"2026-01-01T00:00:00Z"}]}}`
	)

	out, err, created := runRootHealthcheck(t, "--url", healthServer(t, http.StatusOK, passing))

	if err != nil || strings.TrimSpace(out) != "pass (200)" {
		t.Errorf("healthy server: %q, %v, want pass (200) and no error", out, err)
	}

	if created {
		t.Error("the root command's hooks ran: the store was created")
	}

	out, err, _ = runRootHealthcheck(t, "--url", healthServer(t, http.StatusServiceUnavailable, failing))

	if !errors.Is(err, errUnhealthy) {
		t.Errorf("failing server: error %v, want it to be unhealthy", err)
	}

	for _, want := range []string{"fail (503)", "minimega:responseTime [minimega]: fail: minimega did not answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("failing server: output %q, want it to contain %q", out, want)
		}
	}

	if strings.Contains(out, "uptime") {
		t.Errorf("failing server: output %q lists a passing check", out)
	}

	out, _, _ = runRootHealthcheck(t, "--url", healthServer(t, http.StatusServiceUnavailable, failing), "-o", "json")

	if !strings.Contains(out, `"minimega:responseTime"`) {
		t.Errorf("json output = %q, want the body", out)
	}

	// nothing listening
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	if _, err, _ := runRootHealthcheck(t, "--url", srv.URL, "--timeout", "1s"); !errors.Is(err, errUnhealthy) {
		t.Errorf("no server: error %v, want it to be unhealthy", err)
	}
}

//nolint:paralleltest // runs the root command and sets the environment
func TestHealthcheckFromEnvironment(t *testing.T) {
	srv, err := url.Parse(healthServer(t, http.StatusOK, `{"status":"pass"}`))
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("PHENIX_UI_LISTEN_ENDPOINT", srv.Host)

	if out, err, _ := runRootHealthcheck(t); err != nil || strings.TrimSpace(out) != "pass (200)" {
		t.Errorf("endpoint from PHENIX_UI_LISTEN_ENDPOINT: %q, %v, want pass (200) and no error", out, err)
	}
}
