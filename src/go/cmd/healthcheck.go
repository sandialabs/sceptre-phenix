package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/web/health"
)

const (
	defaultHealthcheckTimeout = 5 * time.Second

	// anyIPv4 is the listen host for every IPv4 address.
	anyIPv4 = "0.0.0.0"
)

var errUnhealthy = errors.New("phenix is not healthy")

func newHealthcheckCmd() *cobra.Command {
	desc := `Check the health of the phenix UI server

  Requests the server's liveness endpoint (/healthz), or with --ready its
  readiness endpoint (/readyz), and exits 0 when it answers 200 and 1
  otherwise. The URL comes from ui.listen-endpoint and ui.tls-key/tls-cert
  (the config file or PHENIX_UI_* environment variables), with a wildcard
  host replaced by loopback; --url overrides it. Flags given only on the
  phenix ui command line are not seen here: pass --url, or set
  PHENIX_UI_LISTEN_ENDPOINT, PHENIX_UI_TLS_KEY and PHENIX_UI_TLS_CERT.

  Unlike other subcommands, healthcheck opens no store, unix socket or log
  file, so it is cheap enough for a container health check.`

	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Check the health of the phenix UI server",
		Long:  desc,
		Example: `
  # Liveness, as the Docker and Podman health checks run it
  phenix healthcheck

  # Readiness (minimega, store, disk space, ...), with every check
  phenix healthcheck --ready -o json

  # A server listening elsewhere
  phenix healthcheck --url https://phenix.example.com/readyz`,
		Args: cobra.NoArgs,
		// The root command's hooks read settings from the server, open the
		// store and the system log, none of which a health check needs.
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := mustGet(cmd.Flags().GetString, "url")

			if target == "" {
				var err error

				target, err = healthURL(
					viper.GetString("ui.listen-endpoint"),
					viper.GetString("ui.tls-key") != "" && viper.GetString("ui.tls-cert") != "",
					mustGet(cmd.Flags().GetBool, "ready"),
				)
				if err != nil {
					return err
				}
			}

			timeout, err := cmd.Flags().GetDuration("timeout")
			if err != nil {
				return fmt.Errorf("getting timeout flag: %w", err)
			}

			return runHealthcheck(cmd, target, timeout, mustGet(cmd.Flags().GetString, "output"))
		},
	}

	cmd.Flags().Bool("ready", false, "check readiness (/readyz) rather than liveness (/healthz)")
	cmd.Flags().String("url", "", "URL to check, instead of the one derived from the UI settings")
	cmd.Flags().Duration("timeout", defaultHealthcheckTimeout, "how long to wait for an answer")
	cmd.Flags().StringP("output", "o", "text", "output format ('text' or 'json')")

	return cmd
}

// healthURL is the health endpoint of a UI server listening on endpoint
// (host:port), on loopback when it listens on every address.
func healthURL(endpoint string, useTLS, ready bool) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("parsing ui.listen-endpoint %q: %w", endpoint, err)
	}

	switch host {
	case "", anyIPv4:
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}

	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/healthz"}

	if useTLS {
		u.Scheme = "https"
	}

	if ready {
		u.Path = "/readyz"
	}

	return u.String(), nil
}

// isLoopback reports whether a URL's host is this machine, whose certificate
// (usually issued for its public name) the check need not verify.
func isLoopback(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}

	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}

	ip := net.ParseIP(u.Hostname())

	return ip != nil && ip.IsLoopback()
}

func runHealthcheck(cmd *cobra.Command, target string, timeout time.Duration, output string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ //nolint:exhaustruct // defaults
				InsecureSkipVerify: isLoopback(target), //nolint:gosec // G402 only for this machine
			},
		},
	}

	resp, err := client.Do(req) //nolint:gosec // G704 the user names the target
	if err != nil {
		return fmt.Errorf("%w: %w", errUnhealthy, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", errUnhealthy, target, err)
	}

	out := cmd.OutOrStdout()

	if output == "json" {
		_, _ = fmt.Fprintln(out, string(body))
	} else {
		printHealth(out, resp.StatusCode, body)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s answered %d", errUnhealthy, target, resp.StatusCode)
	}

	return nil
}

// printHealth prints the overall status and every check that did not pass.
func printHealth(out io.Writer, code int, body []byte) {
	var resp health.Response
	if err := json.Unmarshal(body, &resp); err != nil || resp.Status == "" {
		_, _ = fmt.Fprintf(out, "unknown (%d)\n", code) //nolint:gosec // G705 prints to the terminal

		return
	}

	_, _ = fmt.Fprintf(out, "%s (%d)\n", resp.Status, code) //nolint:gosec // G705 prints to the terminal

	keys := make([]string, 0, len(resp.Checks))
	for key := range resp.Checks {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		for _, r := range resp.Checks[key] {
			if r.Status == health.Pass {
				continue
			}

			name := key
			if r.ComponentID != "" {
				name += " [" + r.ComponentID + "]"
			}

			line := fmt.Sprintf("  %s: %s", name, r.Status)
			if r.Output != "" {
				line += ": " + r.Output
			}

			_, _ = fmt.Fprintln(out, line)
		}
	}
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newHealthcheckCmd(), true)
}
