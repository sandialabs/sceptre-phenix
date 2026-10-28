package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"pgregory.net/rapid"
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
			t.Errorf("healthURL(%q, %t, %t) = %q, %v; want %q, nil", test.endpoint, test.tls, test.ready, got, err, test.want)
		}
	}

	if got, err := healthURL("3000", false, false); err == nil {
		t.Errorf("healthURL(%q, false, false) = %q, nil; want an error for an endpoint without a port", "3000", got)
	}
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"https://127.0.0.1:3000/healthz":    true,
		"https://[::1]:3000/healthz":        true,
		"https://localhost/healthz":         true,
		"https://LocalHost:3000/healthz":    true,
		"https://phenix.example.com/readyz": false,
		"https://10.0.0.5/readyz":           false,
	}

	for target, want := range tests {
		if got := isLoopback(target); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", target, got, want)
		}
	}
}

// healthcheckHost is a listen host and whether it names this machine.
type healthcheckHost struct {
	host     string
	loopback bool
}

// genHealthcheckHost draws listen hosts: IPv4 addresses, DNS names and IPv6 edge cases.
func genHealthcheckHost() *rapid.Generator[healthcheckHost] {
	ipv4 := rapid.Custom(func(t *rapid.T) healthcheckHost {
		first := rapid.SampledFrom([]int{127, 10, 0, 128, 126, 255}).Draw(t, "first")
		rest := rapid.SliceOfN(rapid.IntRange(0, 255), 3, 3).Draw(t, "rest")
		ip := fmt.Sprintf("%d.%d.%d.%d", first, rest[0], rest[1], rest[2])

		// 0.0.0.0 alone is every address, which the check reaches on loopback
		return healthcheckHost{host: ip, loopback: first == 127 || ip == anyIPv4}
	})
	name := rapid.Map(rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9-]{0,8}(\.[a-zA-Z]{2,4}){0,2}`), func(host string) healthcheckHost {
		return healthcheckHost{host: host, loopback: strings.EqualFold(host, "localhost")}
	})
	fixed := rapid.SampledFrom([]healthcheckHost{
		{"", true}, {anyIPv4, true}, {"::", true}, {"::1", true}, {"localhost", true}, {"LOCALHOST", true}, {"::ffff:127.0.0.2", true},
		{"fe80::1", false}, {"2001:db8::7", false}, {"::2", false}, {"localhost.example.com", false},
	})

	return rapid.OneOf(ipv4, name, fixed)
}

func TestHealthURLProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		host := genHealthcheckHost().Draw(t, "host")
		port := strconv.Itoa(rapid.IntRange(1, 65535).Draw(t, "port"))
		useTLS, ready := rapid.Bool().Draw(t, "tls"), rapid.Bool().Draw(t, "ready")
		endpoint := net.JoinHostPort(host.host, port)

		got, err := healthURL(endpoint, useTLS, ready)
		if err != nil {
			t.Fatalf("healthURL(%q, %t, %t) error = %v", endpoint, useTLS, ready, err)
		}

		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("healthURL(%q, %t, %t) = %q, which does not parse: %v", endpoint, useTLS, ready, got, err)
		}

		want := url.URL{Scheme: "http", Host: u.Host, Path: "/healthz"}
		if useTLS {
			want.Scheme = "https"
		}

		if ready {
			want.Path = "/readyz"
		}

		if diff := cmp.Diff(want, *u); diff != "" || u.Port() != port {
			t.Fatalf("healthURL(%q, %t, %t) = %q, want port %s (-want +got):\n%s", endpoint, useTLS, ready, got, port, diff)
		}

		// a wildcard becomes loopback; any other host is kept as it was
		switch host.host {
		case "", anyIPv4, "::":
		default:
			if u.Hostname() != host.host {
				t.Fatalf("healthURL(%q, %t, %t) = %q, want host %q", endpoint, useTLS, ready, got, host.host)
			}
		}

		if loopback := isLoopback(got); loopback != host.loopback {
			t.Fatalf("isLoopback(%q) = %t, want %t", got, loopback, host.loopback)
		}
	})
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
		t.Errorf("phenix healthcheck --url <passing 200> = %q, %v; want %q, nil", out, err, "pass (200)")
	}

	if created {
		t.Error("phenix healthcheck --url <passing 200> created the store, want the root command's hooks skipped")
	}

	out, err, _ = runRootHealthcheck(t, "--url", healthServer(t, http.StatusServiceUnavailable, failing))

	if !errors.Is(err, errUnhealthy) {
		t.Errorf("phenix healthcheck --url <failing 503> error = %v, want %v", err, errUnhealthy)
	}

	for _, want := range []string{"fail (503)", "minimega:responseTime [minimega]: fail: minimega did not answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("phenix healthcheck --url <failing 503> = %q, want it to contain %q", out, want)
		}
	}

	if strings.Contains(out, "uptime") {
		t.Errorf("phenix healthcheck --url <failing 503> = %q, want the passing uptime check left out", out)
	}

	out, _, _ = runRootHealthcheck(t, "--url", healthServer(t, http.StatusServiceUnavailable, failing), "-o", "json")

	var gotJSON, wantJSON any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&gotJSON); err != nil {
		t.Errorf("phenix healthcheck --url <failing 503> -o json = %q, want JSON: %v", out, err)
	}

	if err := json.Unmarshal([]byte(failing), &wantJSON); err != nil {
		t.Fatalf("json.Unmarshal(failing) error = %v", err)
	}

	if diff := cmp.Diff(wantJSON, gotJSON); diff != "" {
		t.Errorf("phenix healthcheck --url <failing 503> -o json mismatch (-want +got):\n%s", diff)
	}

	// nothing listening
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	if _, err, _ := runRootHealthcheck(t, "--url", srv.URL, "--timeout", "1s"); !errors.Is(err, errUnhealthy) {
		t.Errorf("phenix healthcheck --url <closed server> error = %v, want %v", err, errUnhealthy)
	}
}

//nolint:paralleltest // runs the root command and sets the environment
func TestHealthcheckFromEnvironment(t *testing.T) {
	srv, err := url.Parse(healthServer(t, http.StatusOK, `{"status":"pass"}`))
	if err != nil {
		t.Fatalf("url.Parse(test server URL) error = %v", err)
	}

	t.Setenv("PHENIX_UI_LISTEN_ENDPOINT", srv.Host)

	if out, err, _ := runRootHealthcheck(t); err != nil || strings.TrimSpace(out) != "pass (200)" {
		t.Errorf("phenix healthcheck with PHENIX_UI_LISTEN_ENDPOINT=%s = %q, %v; want %q, nil", srv.Host, out, err, "pass (200)")
	}
}
