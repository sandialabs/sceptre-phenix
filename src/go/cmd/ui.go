package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/util"
	"phenix/util/plog"
	"phenix/web"
)

const (
	defaultJWTLifetime = 24 * time.Hour
	// a process ended by signal n exits with signalExitBase + n.
	signalExitBase = 128
	// the longest ui.shutdown-grace-period a [time.Duration] holds.
	maxShutdownGraceSeconds = int64(math.MaxInt64 / time.Second)
)

var uiCmd *cobra.Command //nolint:gochecknoglobals // ui command

// uiSettings are the phenix ui flags also read from the ui.<flag> setting and
// the PHENIX_UI_<FLAG> environment variable.
//
//nolint:gochecknoglobals // constant list
var uiSettings = []string{
	"listen-endpoint", "base-path", "jwt-signing-key", "jwt-lifetime", "file-server-endpoint",
	"proxy-auth-header", "users", "tls-key", "tls-cert", "logs.minimega-path", "logs.level",
	"features", "minimega-console", "shutdown-grace-period",
}

//nolint:funlen // command definition
func newUICmd() *cobra.Command {
	desc := `Run the phenix UI server

  Starts the UI server on the IP:port provided.`
	uiCmd = &cobra.Command{
		Use:   "ui",
		Short: "Run the phenix UI",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			grace, err := shutdownGracePeriod(viper.GetViper())
			if err != nil {
				return err
			}

			watchRestartSettings()

			err = web.Init()
			if err != nil {
				return fmt.Errorf("initializing web package: %w", err)
			}

			opts := []web.ServerOption{
				web.ServeOnEndpoint(viper.GetString("ui.listen-endpoint")),
				web.ServeBasePath(viper.GetString("ui.base-path")),
				web.ServeFileServerEndpoint(viper.GetString("ui.file-server-endpoint")),
				web.ServeWithJWTKey(viper.GetString("ui.jwt-signing-key")),
				web.ServeWithJWTLifetime(viper.GetDuration("ui.jwt-lifetime")),
				web.ServeWithUsers(viper.GetStringSlice("ui.users")),
				web.ServeWithTLS(viper.GetString("ui.tls-key"), viper.GetString("ui.tls-cert")),
				web.ServeMinimegaLogs(viper.GetString("ui.logs.minimega-path")),
				web.ServeWithFeatures(viper.GetStringSlice("ui.features")),
				web.ServeWithProxyAuthHeader(viper.GetString("ui.proxy-auth-header")),
				web.ServeWithUnixSocketGID(viper.GetInt("unix-socket-gid")),
				web.ServeWithShutdownGracePeriod(grace),
			}

			if level := viper.GetString("ui.logs.level"); level != "" {
				plog.AddHandler("ui-default", plog.NewUIHandler(level, web.PublishPhenixLog))
			} else {
				plog.AddHandler(
					"ui-default",
					plog.NewUIHandler(viper.GetString("log.level"), web.PublishPhenixLog),
				)
			}

			if viper.GetBool("ui.minimega-console") {
				opts = append(opts, web.ServeMinimegaConsole(true))
			}

			if mustGet(cmd.Flags().GetBool, "log-requests") {
				opts = append(opts, web.ServeWithMiddlewareLogging("requests"))
			}

			if mustGet(cmd.Flags().GetBool, "log-full") {
				opts = append(opts, web.ServeWithMiddlewareLogging("full"))
			}

			if mustGet(cmd.Flags().GetBool, "unbundled") {
				opts = append(opts, web.ServeUnbundled())
			}

			ctx, stop := notifyShutdown(cmd.Context(), os.Exit)
			defer stop()

			err = web.Start(ctx, opts...)
			if err != nil {
				return util.HumanizeError(err, "Unable to serve UI").Humanized()
			}

			return nil
		},
	}

	uiCmd.Flags().StringP("listen-endpoint", "e", "0.0.0.0:3000", "endpoint to listen on")
	uiCmd.Flags().
		StringP("base-path", "b", "/", "base path to use for UI (must run behind proxy if not '/')")
	uiCmd.Flags().
		StringP("jwt-signing-key", "k", "", "Secret key used to sign JWT for authentication")
	uiCmd.Flags().Duration("jwt-lifetime", defaultJWTLifetime, "Lifetime of JWT authentication tokens")
	uiCmd.Flags().String("file-server-endpoint", "0", "port or host:port to serve experiment file uploads on; port-only binds 127.0.0.1")
	uiCmd.Flags().
		String("proxy-auth-header", "", "header containing username when using proxy authentication")
	uiCmd.Flags().StringSlice(
		"users",
		nil,
		"initial users to add, each as <username>:<password>:<role>[:<resource name>...] (comma-separated or repeated)",
	)
	uiCmd.Flags().String("tls-key", "", "path to TLS key file")
	uiCmd.Flags().String("tls-cert", "", "path to TLS cert file")
	uiCmd.Flags().Bool("unbundled", false, "serve local public files instead of bundled")
	uiCmd.Flags().String("logs.minimega-path", "", "path to minimega log file to publish to UI")
	uiCmd.Flags().String("logs.level", "", "log level to publish to UI. Defaults to file level")
	uiCmd.Flags().StringSlice("features", nil, "list of features to enable (options: vm-mount)")
	uiCmd.Flags().Bool("minimega-console", false, "enable minimega console access in UI")
	uiCmd.Flags().Int(
		"shutdown-grace-period",
		int(web.DefaultShutdownGracePeriod/time.Second),
		"how many seconds stopping the server may take to close connections and finish work (0 does not wait)",
	)

	for _, name := range uiSettings {
		_ = viper.BindPFlag("ui."+name, uiCmd.Flags().Lookup(name))
		_ = viper.BindEnv("ui." + name)
	}

	uiCmd.Flags().Bool("log-requests", false, "Log HTTP requests")
	uiCmd.Flags().
		Bool("log-full", false, "Log HTTP requests and responses. Will log sensitive data")

	_ = uiCmd.Flags().MarkHidden("log-full")

	uiCmd.Flags().Int("unix-socket-gid", -1, "group id to allow writes to the unix socket")
	_ = uiCmd.Flags().MarkHidden("unix-socket-gid")
	_ = viper.BindPFlag("unix-socket-gid", uiCmd.Flags().Lookup("unix-socket-gid"))
	_ = viper.BindEnv("unix-socket-gid")

	return uiCmd
}

// shutdownGracePeriod returns ui.shutdown-grace-period from v, which must be a
// whole number of seconds, such as 5, and not negative.
func shutdownGracePeriod(v *viper.Viper) (time.Duration, error) {
	value := v.GetString("ui.shutdown-grace-period")

	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, fmt.Errorf(
			"invalid ui.shutdown-grace-period %q: it must be a whole number of seconds, such as 5", value,
		)
	}

	if seconds < 0 {
		return 0, fmt.Errorf("invalid ui.shutdown-grace-period %q: it must not be negative", value)
	}

	if seconds > maxShutdownGraceSeconds {
		return 0, fmt.Errorf(
			"invalid ui.shutdown-grace-period %q: it must be at most %d seconds", value, maxShutdownGraceSeconds,
		)
	}

	return time.Duration(seconds) * time.Second, nil
}

// notifyShutdown returns a copy of parent canceled, with a web.SignalError, by
// the first SIGINT or SIGTERM; a second one calls exit with 128 + its number.
func notifyShutdown(parent context.Context, exit func(code int)) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)

	var (
		signals = make(chan os.Signal, 1)
		stopped = make(chan struct{})
		once    sync.Once
	)

	// kept after the first signal: PID 1 in a container, or a process started
	// with SIGINT ignored, has no fatal default to fall back on
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-signals:
			cancel(web.SignalError{Signal: sig})
		case <-stopped:
			return
		}

		select {
		case sig := <-signals:
			plog.Warn(plog.TypeSystem, "exiting immediately on a second signal", "signal", sig.String())
			exit(signalExitCode(sig))
		case <-stopped:
		}
	}()

	return ctx, func() {
		once.Do(func() {
			signal.Stop(signals)
			close(stopped)
			cancel(nil)
		})
	}
}

// signalExitCode is the exit status of a process ended by sig, as a shell
// reports it.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return signalExitBase + int(s)
	}

	return 1
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newUICmd(), false)
}
