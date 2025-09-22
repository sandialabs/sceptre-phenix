package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"phenix/api/config"
	_ "phenix/api/scorch"
	"phenix/api/settings"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	phenixBase       string
	minimegaBase     string
	hostnameSuffixes string
	storeEndpoint    string
	logFile          string
)

var rootCmd = &cobra.Command{
	Use:   "phenix",
	Short: "A cli application for phēnix",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		common.UnixSocket = viper.GetString("unix-socket")

		// Initialize bridge mode and use GRE mesh options with values set locally
		// by user. Later they will be forcefully enabled if they're enabled at the
		// server. This must be done before getting options from the server (unlike
		// deploy mode option).

		if err := common.SetBridgeMode(viper.GetString("bridge-mode")); err != nil {
			return fmt.Errorf("setting user-specified bridge mode: %w", err)
		}

		common.UseGREMesh = viper.GetBool("use-gre-mesh")

		// check for global options set by UI server
		if common.UnixSocket != "" {
			cli := http.Client{
				Transport: &http.Transport{
					DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
						return net.Dial("unix", common.UnixSocket)
					},
				},
			}

			if resp, err := cli.Get("http://unix/api/v1/options"); err == nil {
				defer resp.Body.Close()

				if body, err := io.ReadAll(resp.Body); err == nil {
					var options map[string]any
					json.Unmarshal(body, &options)

					mode, _ := options["bridge-mode"].(string)

					// Only override value locally set by user (above) if auto mode is set
					// on the server.
					if mode == string(common.BRIDGE_MODE_AUTO) {
						if err := common.SetBridgeMode(mode); err != nil {
							return fmt.Errorf("setting server-specified bridge mode: %w", err)
						}
					}

					mode, _ = options["deploy-mode"].(string)
					if err := common.SetDeployMode(mode); err != nil {
						return fmt.Errorf("setting server-specified deploy mode: %w", err)
					}

					// Enable use GRE mesh if enabled either locally or at server.
					gre, _ := options["use-gre-mesh"].(bool)
					common.UseGREMesh = common.UseGREMesh || gre
				}
			}
		}

		// Override deploy mode option from UI server if set locally by user. This
		// must be done after getting options from the server (unlike use GRE mesh
		// option).
		if err := common.SetDeployMode(viper.GetString("deploy-mode")); err != nil {
			return fmt.Errorf("setting user-specified deploy mode: %w", err)
		}

		plog.NewPhenixHandler()
		plog.SetLevelText(viper.GetString("log.level"))

		common.PhenixBase = viper.GetString("base-dir.phenix")
		common.MinimegaBase = viper.GetString("base-dir.minimega")
		common.HostnameSuffixes = viper.GetString("hostname-suffixes")

		var (
			endpoint = viper.GetString("store.endpoint")
		)

		common.StoreEndpoint = endpoint

		// Initialize storage backend if not already done
		if !store.IsInitialized(store.COMPONENT_STORE) {
			if err := store.Init(store.Endpoint(endpoint)); err != nil {
				return fmt.Errorf("initializing storage: %w", err)
			}
		}

		// Initialize default configs if not already done
		if !store.IsInitialized(store.COMPONENT_CONFIGS) {
			if err := config.Init(); err != nil {
				return fmt.Errorf("unable to initialize default configs: %w", err)
			}
		}

		// Add log file handler after bbolt is live
		logFile := viper.GetString("log.file.path")
		fileHandlerOpts := plog.GetDefaultFileHandlerOpts()
		fileLogSettings, err := settings.GetLoggingSettings()
		if err == nil {
			fileHandlerOpts.MaxSize = int(fileLogSettings.MaxFileSize)
			fileHandlerOpts.MaxBackups = int(fileLogSettings.MaxFileRotations)
			fileHandlerOpts.MaxAge = int(fileLogSettings.MaxFileAge)
			fileHandlerOpts.Level = plog.TextToLevel(viper.GetString("log.file.level"))
		}
		go func() {
			plog.AddFileHandler(logFile, fileHandlerOpts)
		}()

		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		plog.CloseFile()
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
	SilenceUsage: true, // don't print help when subcommands return an error
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	uid, home := getCurrentUserInfo()
	var homePath string

	if uid != "0" {
		homePath = fmt.Sprintf("%s/.config/phenix", home)
	}

	viper.SetEnvPrefix("PHENIX")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv() // read in environment variables that match

	viper.SetConfigName("config")

	// Config paths - first look in current directory, then home directory (if
	// discoverable), then finally global config directory.
	viper.AddConfigPath(".")

	if homePath != "" {
		viper.AddConfigPath(homePath)
	}

	viper.AddConfigPath("/etc/phenix")

	// If a config file is found, read it in.
	viper.ReadInConfig()

	viper.SetConfigName("users")
	viper.AddConfigPath(".")

	if homePath != "" {
		viper.AddConfigPath(homePath)
	}

	viper.AddConfigPath("/etc/phenix")

	// If a users config file is found, merge it in.
	if err := viper.MergeInConfig(); err == nil {
		viper.WatchConfig()

		viper.OnConfigChange(func(e fsnotify.Event) {
			if strings.TrimSuffix(filepath.Base(e.Name), filepath.Ext(e.Name)) == "users" {
				web.ConfigureUsers(viper.GetStringSlice("ui.users"))
			}
		})
	}

	rootCmd.PersistentFlags().StringVar(&phenixBase, "base-dir.phenix", "/phenix", "base phenix directory")
	rootCmd.PersistentFlags().StringVar(&minimegaBase, "base-dir.minimega", "/tmp/minimega", "base minimega directory")
	rootCmd.PersistentFlags().StringVar(&hostnameSuffixes, "hostname-suffixes", "-minimega,-phenix", "hostname suffixes to strip")
	rootCmd.PersistentFlags().Bool("log.error-stderr", true, "log fatal errors to STDERR - DEPRECATED (Determined by log.level)")
	rootCmd.PersistentFlags().String("log.level", "info", "level to log messages at")
	rootCmd.PersistentFlags().String("bridge-mode", "", "bridge naming mode for experiments ('auto' uses experiment name for bridge; 'manual' uses user-specified bridge name, or 'phenix' if not specified) (options: manual | auto)")
	rootCmd.PersistentFlags().String("deploy-mode", "", "deploy mode for minimega VMs (options: all | no-headnode | only-headnode)")
	rootCmd.PersistentFlags().Bool("use-gre-mesh", false, "use GRE tunnels between mesh nodes for VLAN trunking")
	rootCmd.PersistentFlags().String("unix-socket", "/tmp/phenix.sock", "phēnix unix socket to listen on (ui subcommand) or connect to")

	rootCmd.PersistentFlags().String("log.file.level", "info", "level to log messages at for log file (options: debug | info | warn | error | none)")

	errFile := ""
	rootCmd.PersistentFlags().StringVar(&errFile, "log.error-file", "", "log fatal errors to file - DEPRECATED (Determined by log.file.level)")
	if uid == "0" {
		os.MkdirAll("/etc/phenix", 0755)
		os.MkdirAll("/var/log/phenix", 0755)
		rootCmd.PersistentFlags().StringVar(&storeEndpoint, "store.endpoint", "bolt:///etc/phenix/store.bdb", "endpoint for storage service")
		rootCmd.PersistentFlags().String("log.file.path", "/var/log/phenix/phenix.log", "path to log to")

	} else {
		os.MkdirAll(fmt.Sprintf("%s/phenix_logs/", home), 0755)
		rootCmd.PersistentFlags().StringVar(&storeEndpoint, "store.endpoint", fmt.Sprintf("bolt://%s/.phenix.bdb", home), "endpoint for storage service")
		rootCmd.PersistentFlags().String("log.file.path", fmt.Sprintf("%s/phenix_logs/phenix.log", home), "path to log to")
	}

	viper.BindPFlags(rootCmd.PersistentFlags())
}

func getCurrentUserInfo() (string, string) {
	u, err := user.Current()
	if err != nil {
		panic("unable to determine current user: " + err.Error())
	}

	var (
		uid  = u.Uid
		home = u.HomeDir
		sudo = os.Getenv("SUDO_USER")
	)

	// Only trust `SUDO_USER` env variable if we're currently running as root and,
	// if set, use it to lookup the actual user that ran the sudo command.
	if u.Uid == "0" && sudo != "" {
		u, err := user.Lookup(sudo)
		if err != nil {
			// fall back to sudo if we couldn't get actual user
			fmt.Fprintf(os.Stderr, "Could not find SUDO_USER %s. Looking for optional config file in sudo home directory\n", sudo)
			return uid, home
		}

		// `uid` and `home` will now reflect the user ID and home directory of the
		// actual user that ran the sudo command.
		uid = u.Uid
		home = u.HomeDir
	}

	return uid, home
}
