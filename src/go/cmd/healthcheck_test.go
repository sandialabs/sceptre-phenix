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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"pgregory.net/rapid"
)

// healthURL derives the health endpoint from ui.listen-endpoint, on loopback for a wildcard host, and fails for an
// endpoint without a port.
func TestHealthURL(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint   string
		tls, ready bool
		want       string
		wantErr    string
	}{
		"IPv4 wildcard":    {endpoint: "0.0.0.0:3000", want: "http://127.0.0.1:3000/healthz"},
		"empty host":       {endpoint: ":3000", ready: true, want: "http://127.0.0.1:3000/readyz"},
		"IPv6 wildcard":    {endpoint: "[::]:3443", tls: true, want: "https://[::1]:3443/healthz"},
		"address":          {endpoint: "10.0.0.5:8080", want: "http://10.0.0.5:8080/healthz"},
		"name, TLS, ready": {endpoint: "phenix.example.com:443", tls: true, ready: true, want: "https://phenix.example.com:443/readyz"},
		"no port": {
			endpoint: "3000",
			wantErr:  `parsing ui.listen-endpoint "3000": address 3000: missing port in address`,
		},
		"empty": {endpoint: "", wantErr: `parsing ui.listen-endpoint "": missing port in address`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := healthURL(tc.endpoint, tc.tls, tc.ready)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("healthURL(%q) = %q, %v; want error %q", tc.endpoint, got, err, tc.wantErr)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("healthURL(%q, %t, %t) = %q, %v; want %q, nil", tc.endpoint, tc.tls, tc.ready, got, err, tc.want)
			}
		})
	}
}

// isLoopback is true only for localhost and loopback addresses, and false for a URL that does not parse.
func TestIsLoopback(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"https://127.0.0.1:3000/healthz":    true,
		"https://[::1]:3000/healthz":        true,
		"https://localhost/healthz":         true,
		"https://LocalHost:3000/healthz":    true,
		"https://phenix.example.com/readyz": false,
		"https://10.0.0.5/readyz":           false,
		"http://[::1/healthz":               false,
		"://127.0.0.1":                      false,
		"":                                  false,
	}

	for target, want := range tests {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			if got := isLoopback(target); got != want {
				t.Errorf("isLoopback(%q) = %v, want %v", target, got, want)
			}
		})
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

// healthcheckRun runs runHealthcheck against target, and returns the output and the error.
func healthcheckRun(t *testing.T, target, output string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&out)

	err := runHealthcheck(cmd, target, time.Second, output)

	return out.String(), err
}

// runHealthcheck fails for a URL it cannot request and for a body cut short, and trusts the certificate of a
// server on loopback without checking it.
func TestRunHealthcheck(t *testing.T) {
	t.Parallel()

	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}

		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	t.Cleanup(truncated.Close)

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pass"}`))
	}))
	t.Cleanup(tlsServer.Close)

	tests := map[string]struct {
		target, output string
		want           string
		wantErr        string // a prefix
		unhealthy      bool
	}{
		"bad URL":         {target: "://nowhere", wantErr: "creating request: "},
		"body cut short":  {target: truncated.URL, wantErr: "phenix is not healthy: reading " + truncated.URL + ": ", unhealthy: true},
		"TLS on loopback": {target: tlsServer.URL, want: "pass (200)\n"},
		"TLS, JSON":       {target: tlsServer.URL, output: FormatJSON, want: `{"status":"pass"}` + "\n"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := healthcheckRun(t, tc.target, tc.output)

			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantErr)) {
				t.Fatalf("runHealthcheck(%s) error = %v, want prefix %q", tc.target, err, tc.wantErr)
			}

			if errors.Is(err, errUnhealthy) != tc.unhealthy {
				t.Errorf("runHealthcheck(%s) error = %v, errors.Is(errUnhealthy) want %t", tc.target, err, tc.unhealthy)
			}

			if out != tc.want {
				t.Errorf("runHealthcheck(%s) output = %q, want %q", tc.target, out, tc.want)
			}
		})
	}
}

// printHealth prints "unknown" for a body that is not a health response, and otherwise the status and each check
// that does not pass, sorted by key, with its component and output when it has them.
func TestPrintHealth(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		code int
		body string
		want string
	}{
		"not JSON":     {code: http.StatusBadGateway, body: "<html>bad gateway</html>", want: "unknown (502)\n"},
		"no status":    {code: http.StatusOK, body: `{"checks":{}}`, want: "unknown (200)\n"},
		"empty body":   {code: http.StatusServiceUnavailable, want: "unknown (503)\n"},
		"every pass":   {code: http.StatusOK, body: `{"status":"pass","checks":{"uptime":[{"status":"pass"}]}}`, want: "pass (200)\n"},
		"wrong layout": {code: http.StatusOK, body: `{"status":"pass","checks":[]}`, want: "unknown (200)\n"},
		"problems sorted": {
			code: http.StatusServiceUnavailable,
			body: `{"status":"fail","checks":{` +
				`"store":[{"status":"fail","output":"locked"}],` +
				`"disk":[{"componentId":"files-dir","status":"warn","output":"91% used"},{"componentId":"store","status":"pass"}],` +
				`"minimega":[{"status":"warn"}]}}`,
			want: "fail (503)\n  disk [files-dir]: warn: 91% used\n  minimega: warn\n  store: fail: locked\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			printHealth(&out, tc.code, []byte(tc.body))

			if diff := cmp.Diff(tc.want, out.String()); diff != "" {
				t.Errorf("printHealth(%d, %q) mismatch (-want +got):\n%s", tc.code, tc.body, diff)
			}
		})
	}
}

// The command gives the healthURL error for a ui.listen-endpoint without a port, and opens no store.
//
//nolint:paralleltest // runs the root command and sets the environment
func TestHealthcheckBadListenEndpoint(t *testing.T) {
	t.Setenv("PHENIX_UI_LISTEN_ENDPOINT", "3000")

	_, err, created := runRootHealthcheck(t)

	if want := `parsing ui.listen-endpoint "3000": `; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("phenix healthcheck with PHENIX_UI_LISTEN_ENDPOINT=3000 error = %v, want prefix %q", err, want)
	}

	if created {
		t.Error("phenix healthcheck with a bad endpoint created the store, want the root command's hooks skipped")
	}
}
