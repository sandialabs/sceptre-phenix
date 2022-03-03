package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"phenix/internal/common"
	"phenix/util"
	"phenix/web"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newUiCmd() *cobra.Command {
	desc := `Run the phenix UI server

  Starts the UI server on the IP:port provided.`
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Run the phenix UI",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := web.Init(); err != nil {
				return fmt.Errorf("initializing web package: %w", err)
			}

			level, err := log.ParseLevel(viper.GetString("ui.log-level"))
			if err != nil {
				return err
			}

			if viper.GetBool("ui.log-verbose") {
				log.AddLogger("stderr", os.Stderr, level, true)
			}

			if path := viper.GetString("ui.logs.phenix-path"); path != "" {
				os.MkdirAll(filepath.Dir(path), 0755)

				logfile, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0660)
				if err != nil {
					return err
				}

				log.AddLogger("file", logfile, level, false)
				common.LogFile = path
			}

			opts := []web.ServerOption{
				web.ServeOnEndpoint(viper.GetString("ui.listen-endpoint")),
				web.ServeBasePath(viper.GetString("ui.base-path")),
				web.ServeWithJWTKey(viper.GetString("ui.jwt-signing-key")),
				web.ServeWithTLS(viper.GetString("ui.tls-key"), viper.GetString("ui.tls-cert")),
				web.ServePhenixLogs(viper.GetString("ui.logs.phenix-path")),
				web.ServeMinimegaLogs(viper.GetString("ui.logs.minimega-path")),
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

			if err := web.Start(opts...); err != nil {
				return util.HumanizeError(err, "Unable to serve UI").Humanized()
			}

			return nil
		},
	}

	cmd.Flags().StringP("listen-endpoint", "e", "0.0.0.0:3000", "endpoint to listen on")
	cmd.Flags().StringP("base-path", "b", "/", "base path to use for UI (must run behind proxy if not '/')")
	cmd.Flags().StringP("jwt-signing-key", "k", "", "Secret key used to sign JWT for authentication")
	cmd.Flags().String("tls-key", "", "path to TLS key file")
	cmd.Flags().String("tls-cert", "", "path to TLS cert file")
	cmd.Flags().Bool("unbundled", false, "serve local public files instead of bundled")
	cmd.Flags().String("log-level", "info", "log level for UI logs")
	cmd.Flags().Bool("log-verbose", true, "write UI logs to STDERR")
	cmd.Flags().String("logs.phenix-path", "", "path to phenix log file to publish to UI")
	cmd.Flags().String("logs.minimega-path", "", "path to minimega log file to publish to UI")

	viper.BindPFlag("ui.listen-endpoint", cmd.Flags().Lookup("listen-endpoint"))
	viper.BindPFlag("ui.base-path", cmd.Flags().Lookup("base-path"))
	viper.BindPFlag("ui.jwt-signing-key", cmd.Flags().Lookup("jwt-signing-key"))
	viper.BindPFlag("ui.tls-key", cmd.Flags().Lookup("tls-key"))
	viper.BindPFlag("ui.tls-cert", cmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("ui.log-level", cmd.Flags().Lookup("log-level"))
	viper.BindPFlag("ui.log-verbose", cmd.Flags().Lookup("log-verbose"))
	viper.BindPFlag("ui.logs.phenix-path", cmd.Flags().Lookup("logs.phenix-path"))
	viper.BindPFlag("ui.logs.minimega-path", cmd.Flags().Lookup("logs.minimega-path"))

	viper.BindEnv("ui.listen-endpoint")
	viper.BindEnv("ui.base-path")
	viper.BindEnv("ui.jwt-signing-key")
	viper.BindEnv("ui.tls-key")
	viper.BindEnv("ui.tls-cert")
	viper.BindEnv("ui.log-level")
	viper.BindEnv("ui.log-verbose")
	viper.BindEnv("ui.logs.phenix-path")
	viper.BindEnv("ui.logs.minimega-path")

	cmd.Flags().Bool("log-requests", false, "Log API requests")
	cmd.Flags().Bool("log-full", false, "Log API requests and responses")

	cmd.Flags().MarkHidden("log-requests")
	cmd.Flags().MarkHidden("log-full")

	return cmd
}

func init() {
	rootCmd.AddCommand(newUiCmd())
}
