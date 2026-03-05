package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/util"
	"phenix/util/plog"
	"phenix/web"
)

const defaultJWTLifetime = 24 * time.Hour

var uiCmd *cobra.Command //nolint:gochecknoglobals // ui command

//nolint:funlen // command definition
func newUICmd() *cobra.Command {
	desc := `Run the phenix UI server

  Starts the UI server on the IP:port provided.`
	uiCmd = &cobra.Command{
		Use:   "ui",
		Short: "Run the phenix UI",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := web.Init()
			if err != nil {
				return fmt.Errorf("initializing web package: %w", err)
			}

			opts := []web.ServerOption{
				web.ServeOnEndpoint(viper.GetString("ui.listen-endpoint")),
				web.ServeBasePath(viper.GetString("ui.base-path")),
				web.ServeWithJWTKey(viper.GetString("ui.jwt-signing-key")),
				web.ServeWithJWTLifetime(viper.GetDuration("ui.jwt-lifetime")),
				web.ServeWithUsers(viper.GetStringSlice("ui.users")),
				web.ServeWithTLS(viper.GetString("ui.tls-key"), viper.GetString("ui.tls-cert")),
				web.ServeMinimegaLogs(viper.GetString("ui.logs.minimega-path")),
				web.ServeWithFeatures(viper.GetStringSlice("ui.features")),
				web.ServeWithProxyAuthHeader(viper.GetString("ui.proxy-auth-header")),
				web.ServeWithUnixSocketGID(viper.GetInt("unix-socket-gid")),
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

			if MustGetBool(cmd.Flags(), "log-requests") {
				opts = append(opts, web.ServeWithMiddlewareLogging("requests"))
			}

			if MustGetBool(cmd.Flags(), "log-full") {
				opts = append(opts, web.ServeWithMiddlewareLogging("full"))
			}

			if MustGetBool(cmd.Flags(), "unbundled") {
				opts = append(opts, web.ServeUnbundled())
			}

			err = web.Start(opts...)
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
	uiCmd.Flags().
		String("proxy-auth-header", "", "header containing username when using proxy authentication")
	uiCmd.Flags().StringSlice("users", nil, "pipe-delimited list of initial users to add")
	uiCmd.Flags().String("tls-key", "", "path to TLS key file")
	uiCmd.Flags().String("tls-cert", "", "path to TLS cert file")
	uiCmd.Flags().Bool("unbundled", false, "serve local public files instead of bundled")
	uiCmd.Flags().String("logs.minimega-path", "", "path to minimega log file to publish to UI")
	uiCmd.Flags().String("logs.level", "", "log level to publish to UI. Defaults to file level")
	uiCmd.Flags().StringSlice("features", nil, "list of features to enable (options: vm-mount)")
	uiCmd.Flags().Bool("minimega-console", false, "enable minimega console access in UI")

	_ = viper.BindPFlag("ui.listen-endpoint", uiCmd.Flags().Lookup("listen-endpoint"))
	_ = viper.BindPFlag("ui.base-path", uiCmd.Flags().Lookup("base-path"))
	_ = viper.BindPFlag("ui.jwt-signing-key", uiCmd.Flags().Lookup("jwt-signing-key"))
	_ = viper.BindPFlag("ui.jwt-lifetime", uiCmd.Flags().Lookup("jwt-lifetime"))
	_ = viper.BindPFlag("ui.proxy-auth-header", uiCmd.Flags().Lookup("proxy-auth-header"))
	_ = viper.BindPFlag("ui.users", uiCmd.Flags().Lookup("users"))
	_ = viper.BindPFlag("ui.tls-key", uiCmd.Flags().Lookup("tls-key"))
	_ = viper.BindPFlag("ui.tls-cert", uiCmd.Flags().Lookup("tls-cert"))
	_ = viper.BindPFlag("ui.logs.minimega-path", uiCmd.Flags().Lookup("logs.minimega-path"))
	_ = viper.BindPFlag("ui.logs.level", uiCmd.Flags().Lookup("logs.level"))
	_ = viper.BindPFlag("ui.features", uiCmd.Flags().Lookup("features"))
	_ = viper.BindPFlag("ui.minimega-console", uiCmd.Flags().Lookup("minimega-console"))

	_ = viper.BindEnv("ui.listen-endpoint")
	_ = viper.BindEnv("ui.base-path")
	_ = viper.BindEnv("ui.jwt-signing-key")
	_ = viper.BindEnv("ui.jwt-lifetime")
	_ = viper.BindEnv("ui.proxy-auth-header")
	_ = viper.BindEnv("ui.users")
	_ = viper.BindEnv("ui.tls-key")
	_ = viper.BindEnv("ui.tls-cert")
	_ = viper.BindEnv("ui.logs.minimega-path")
	_ = viper.BindEnv("ui.logs.level")
	_ = viper.BindEnv("ui.features")
	_ = viper.BindEnv("ui.minimega-console")

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

func init() { //nolint:gochecknoinits // cobra command
	rootCmd.AddCommand(newUICmd())
}
