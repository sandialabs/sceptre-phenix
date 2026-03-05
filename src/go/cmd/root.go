package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/api/config"
	_ "phenix/api/scorch"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web"
)

const (
	configDir            = "/etc/phenix"
	defaultMaxLogSize    = 100
	defaultMaxLogBackups = 3
	defaultMaxLogAge     = 90
)

var (
	phenixBase         string   //nolint:gochecknoglobals // global flag
	minimegaBase       string   //nolint:gochecknoglobals // global flag
	hostnameSuffixes   string   //nolint:gochecknoglobals // global flag
	storeEndpoint      string   //nolint:gochecknoglobals // global flag
	currentConsoleFile *os.File //nolint:gochecknoglobals // global state
)

//nolint:gochecknoglobals // root command
var rootCmd = &cobra.Command{
	Use:   "phenix",
	Short: "A cli application for phēnix",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		common.UnixSocket = getEffectiveString( //nolint:reassign // configuration injection
			"unix-socket",
			cmd.Flags().Changed("unix-socket"),
		)

		// Initialize bridge mode and use GRE mesh options with values set locally
		// by user. Later they will be forcefully enabled if they're enabled at the
		// server. This must be done before getting options from the server (unlike
		// deploy mode option).

		err := common.SetBridgeMode(
			getEffectiveString("bridge-mode", cmd.Flags().Changed("bridge-mode")),
		)
		if err != nil {
			return fmt.Errorf("setting user-specified bridge mode: %w", err)
		}

		common.UseGREMesh = getEffectiveBool( //nolint:reassign // configuration injection
			"use-gre-mesh",
			cmd.Flags().Changed("use-gre-mesh"),
		)

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
				defer func() { _ = resp.Body.Close() }()

				if body, err := io.ReadAll(resp.Body); err == nil {
					var options map[string]any
					_ = json.Unmarshal(body, &options)

					mode, _ := options["bridge-mode"].(string)

					// Only override value locally set by user (above) if auto mode is set
					// on the server.
					if mode == string(common.BridgeModeAuto) {
						err := common.SetBridgeMode(mode)
						if err != nil {
							return fmt.Errorf("setting server-specified bridge mode: %w", err)
						}
					}

					mode, _ = options["deploy-mode"].(string)
					err := common.SetDeployMode(mode)
					if err != nil {
						return fmt.Errorf("setting server-specified deploy mode: %w", err)
					}

					// Enable use GRE mesh if enabled either locally or at server.
					gre, _ := options["use-gre-mesh"].(bool)
					common.UseGREMesh = common.UseGREMesh || gre //nolint:reassign // configuration injection
				}
			}
		}

		// Override deploy mode option from UI server if set locally by user. This
		// must be done after getting options from the server (unlike use GRE mesh
		// option).
		err = common.SetDeployMode(
			getEffectiveString("deploy-mode", cmd.Flags().Changed("deploy-mode")),
		)
		if err != nil {
			return fmt.Errorf("setting user-specified deploy mode: %w", err)
		}

		var logOutput io.Writer = os.Stderr
		if out := getEffectiveString(
			"log.console",
			cmd.Flags().Changed("log.console"),
		); out != "stderr" {
			if out == "stdout" {
				logOutput = os.Stdout
			} else if out != "" {
				if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
					return fmt.Errorf("creating log output directory: %w", err)
				}
				f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				if err != nil {
					return fmt.Errorf("opening log output file: %w", err)
				}
				logOutput = f
				currentConsoleFile = f
			}
		}

		plog.NewPhenixHandler(logOutput)
		plog.SetLevelText(getEffectiveString("log.level", cmd.Flags().Changed("log.level")))

		common.PhenixBase = getEffectiveString( //nolint:reassign // configuration injection
			"base-dir.phenix",
			cmd.Flags().Changed("base-dir.phenix"),
		)
		common.MinimegaBase = getEffectiveString( //nolint:reassign // configuration injection
			"base-dir.minimega",
			cmd.Flags().Changed("base-dir.minimega"),
		)
		common.HostnameSuffixes = getEffectiveString( //nolint:reassign // configuration injection
			"hostname-suffixes",
			cmd.Flags().Changed("hostname-suffixes"),
		)

		endpoint := getEffectiveString("store.endpoint", cmd.Flags().Changed("store.endpoint"))

		common.StoreEndpoint = endpoint //nolint:reassign // configuration injection

		// Initialize storage backend if not already done
		if !store.IsInitialized(store.ComponentStore) {
			err := store.Init(store.Endpoint(endpoint))
			if err != nil {
				return fmt.Errorf("initializing storage: %w", err)
			}
		}

		// Initialize default configs if not already done
		if !store.IsInitialized(store.ComponentConfigs) {
			err := config.Init()
			if err != nil {
				return fmt.Errorf("unable to initialize default configs: %w", err)
			}
		}

		logFile := getEffectiveString("log.system.path", cmd.Flags().Changed("log.system.path"))
		fileHandlerOpts := plog.GetDefaultFileHandlerOpts()
		fileHandlerOpts.MaxSize = getEffectiveInt(
			"log.system.max-size",
			cmd.Flags().Changed("log.system.max-size"),
		)
		fileHandlerOpts.MaxBackups = getEffectiveInt(
			"log.system.max-backups",
			cmd.Flags().Changed("log.system.max-backups"),
		)
		fileHandlerOpts.MaxAge = getEffectiveInt(
			"log.system.max-age",
			cmd.Flags().Changed("log.system.max-age"),
		)
		go func() {
			plog.AddFileHandler(logFile, fileHandlerOpts)
		}()

		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		_ = plog.CloseFile()
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
	SilenceUsage: true, // don't print help when subcommands return an error
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

//nolint:funlen,gochecknoinits // init function
func init() {
	uid, home := getCurrentUserInfo()

	// Determine default paths based on user privileges
	var (
		defaultConfigDir string
		defaultLogPath   string
		defaultStore     string
	)

	if uid == "0" {
		defaultConfigDir = configDir
		defaultLogPath = "/var/log/phenix/phenix.log"
		defaultStore = "bolt:///etc/phenix/store.bdb"
	} else {
		defaultConfigDir = filepath.Join(home, ".config", "phenix")
		defaultLogPath = filepath.Join(home, "phenix_logs", "phenix.log")
		defaultStore = fmt.Sprintf("bolt://%s/.phenix.bdb", home)
	}

	viper.SetEnvPrefix("PHENIX")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv() // read in environment variables that match

	viper.SetConfigName("config")

	// Config paths - first look in current directory, then home directory (if
	// discoverable), then finally global config directory.
	viper.AddConfigPath(".")

	if uid != "0" {
		viper.AddConfigPath(defaultConfigDir)
	}

	viper.AddConfigPath("/etc/phenix")

	// If a config file is found, read it in.
	err := viper.ReadInConfig()
	if err == nil {
		viper.WatchConfig()
	} else if errors.As(err, &viper.ConfigFileNotFoundError{}) {
		// If the config file doesn't exist, create it so we can watch it for
		// changes at runtime.
		err := os.MkdirAll(defaultConfigDir, 0o750)
		if err == nil {
			targetFile := filepath.Join(defaultConfigDir, "config.yaml")
			_ = viper.SafeWriteConfigAs(targetFile)
			err := viper.ReadInConfig()
			if err == nil {
				viper.WatchConfig()
			}
		}
	}

	viper.SetConfigName("users")
	viper.AddConfigPath(".")

	if uid != "0" {
		viper.AddConfigPath(defaultConfigDir)
	}

	viper.AddConfigPath(configDir)

	// If a users config file is found, merge it in.
	err = viper.MergeInConfig()
	if err == nil {
		viper.WatchConfig() // Watch the users config if it exists
	}

	viper.SetConfigName("config")

	viper.OnConfigChange(func(e fsnotify.Event) {
		if strings.TrimSuffix(filepath.Base(e.Name), filepath.Ext(e.Name)) == "users" {
			_ = web.ConfigureUsers(viper.GetStringSlice("ui.users"))
		}
		plog.SetLevelText(
			getEffectiveString("log.level", rootCmd.PersistentFlags().Changed("log.level")),
		)
		plog.ChangeLogFile(
			getEffectiveString(
				"log.system.path",
				rootCmd.PersistentFlags().Changed("log.system.path"),
			),
		)
		plog.ChangeMaxLogFileSize(
			getEffectiveInt(
				"log.system.max-size",
				rootCmd.PersistentFlags().Changed("log.system.max-size"),
			),
		)
		plog.ChangeMaxLogFileBackups(
			getEffectiveInt(
				"log.system.max-backups",
				rootCmd.PersistentFlags().Changed("log.system.max-backups"),
			),
		)
		plog.ChangeMaxLogFileAge(
			getEffectiveInt(
				"log.system.max-age",
				rootCmd.PersistentFlags().Changed("log.system.max-age"),
			),
		)

		// Hot-swap console logger
		var logOutput io.Writer = os.Stderr
		var newFile *os.File
		if out := getEffectiveString(
			"log.console",
			rootCmd.PersistentFlags().Changed("log.console"),
		); out != "stderr" {
			if out == "stdout" {
				logOutput = os.Stdout
			} else if out != "" {
				err := os.MkdirAll(filepath.Dir(out), 0o750)
				if err == nil {
					if f, err := os.OpenFile(
						out,
						os.O_APPEND|os.O_CREATE|os.O_WRONLY,
						0o600,
					); err == nil {
						logOutput = f
						newFile = f
					}
				}
			}
		}
		plog.ChangeConsoleLogger(logOutput)
		if currentConsoleFile != nil {
			_ = currentConsoleFile.Close()
		}
		currentConsoleFile = newFile

		// Hot-swap UI log level
		uiLogsLevelChanged := false
		if uiCmd != nil {
			uiLogsLevelChanged = uiCmd.Flags().Changed("logs.level")
		}

		if level := getEffectiveString("ui.logs.level", uiLogsLevelChanged); level != "" {
			plog.AddHandler("ui-default", plog.NewUIHandler(level, web.PublishPhenixLog))
		} else {
			plog.AddHandler(
				"ui-default",
				plog.NewUIHandler(
					getEffectiveString("log.level", rootCmd.PersistentFlags().Changed("log.level")),
					web.PublishPhenixLog,
				),
			)
		}
	})

	rootCmd.PersistentFlags().
		StringVar(&phenixBase, "base-dir.phenix", "/phenix", "base phenix directory")
	rootCmd.PersistentFlags().
		StringVar(&minimegaBase, "base-dir.minimega", "/tmp/minimega", "base minimega directory")
	rootCmd.PersistentFlags().
		StringVar(&hostnameSuffixes, "hostname-suffixes", "-minimega,-phenix", "hostname suffixes to strip")
	rootCmd.PersistentFlags().String("log.level", "info", "level to log messages at")
	rootCmd.PersistentFlags().
		String("log.console", "stderr", "output for console logs (text format) (stderr, stdout, or file path)")
	rootCmd.PersistentFlags().
		Int("log.system.max-size", defaultMaxLogSize, "maximum size in megabytes of the log file before it gets rotated")
	rootCmd.PersistentFlags().
		Int("log.system.max-backups", defaultMaxLogBackups, "maximum number of old log files to retain")
	rootCmd.PersistentFlags().
		Int("log.system.max-age", defaultMaxLogAge, "maximum number of days to retain old log files")
	rootCmd.PersistentFlags().
		String(
			"bridge-mode",
			"",
			"bridge naming mode for experiments ('auto' uses experiment name for bridge; 'manual' uses user-specified bridge name, or 'phenix' if not specified) (options: manual | auto)",
		)
	rootCmd.PersistentFlags().
		String("deploy-mode", "", "deploy mode for minimega VMs (options: all | no-headnode | only-headnode)")
	rootCmd.PersistentFlags().
		Bool("use-gre-mesh", false, "use GRE tunnels between mesh nodes for VLAN trunking")
	rootCmd.PersistentFlags().
		String("unix-socket", "/tmp/phenix.sock", "phēnix unix socket to listen on (ui subcommand) or connect to")

	// Ensure default directories exist
	if uid == "0" {
		_ = os.MkdirAll(configDir, 0o750)
		_ = os.MkdirAll(filepath.Dir(defaultLogPath), 0o750)
	} else {
		_ = os.MkdirAll(filepath.Dir(defaultLogPath), 0o750)
	}

	rootCmd.PersistentFlags().
		StringVar(&storeEndpoint, "store.endpoint", defaultStore, "endpoint for storage service")
	rootCmd.PersistentFlags().
		String("log.system.path", defaultLogPath, "path to system log (JSON format)")

	_ = viper.BindPFlags(rootCmd.PersistentFlags())
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
			fmt.Fprintf(
				os.Stderr,
				"Could not find SUDO_USER %s. Looking for optional config file in sudo home directory\n",
				sudo,
			)
			return uid, home
		}

		// `uid` and `home` will now reflect the user ID and home directory of the
		// actual user that ran the sudo command.
		uid = u.Uid
		home = u.HomeDir
	}

	return uid, home
}

// getEffectiveString returns the configuration value with the following precedence:
// 1. Command Line Flag (if changed)
// 2. Config File
// 3. Environment Variable / Default (via Viper).
func getEffectiveString(key string, flagChanged bool) string {
	if flagChanged {
		return viper.GetString(key)
	}
	if v := getFileViper(); v != nil && v.IsSet(key) {
		return v.GetString(key)
	}
	return viper.GetString(key)
}

// getEffectiveInt returns the configuration value with the following precedence:
// 1. Command Line Flag (if changed)
// 2. Config File
// 3. Environment Variable / Default (via Viper).
func getEffectiveInt(key string, flagChanged bool) int {
	if flagChanged {
		return viper.GetInt(key)
	}
	if v := getFileViper(); v != nil && v.IsSet(key) {
		return v.GetInt(key)
	}
	return viper.GetInt(key)
}

// getEffectiveBool returns the configuration value with the following precedence:
// 1. Command Line Flag (if changed)
// 2. Config File
// 3. Environment Variable / Default (via Viper).
func getEffectiveBool(key string, flagChanged bool) bool {
	if flagChanged {
		return viper.GetBool(key)
	}
	if v := getFileViper(); v != nil && v.IsSet(key) {
		return v.GetBool(key)
	}
	return viper.GetBool(key)
}

// getFileViper creates a temporary Viper instance loaded ONLY with the
// config file, ignoring environment variables. This allows us to peek
// at what is explicitly set in the file.
func getFileViper() *viper.Viper {
	f := viper.ConfigFileUsed()
	if f == "" {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(f)
	_ = v.ReadInConfig()
	return v
}

// getEffectiveValue is a generic version of getEffectiveString/Int
// used by the 'settings get' command to show the actual active value.
func getEffectiveValue(key string) any {
	if v := getFileViper(); v != nil && v.IsSet(key) {
		return v.Get(key)
	}
	return viper.Get(key)
}

// getEffectiveSettings returns a map of all settings, prioritizing
// values in the config file over environment variables.
func getEffectiveSettings() map[string]any {
	// Start with base settings (Env > Config > Default)
	base := viper.AllSettings()

	// Overlay file settings (File > Env)
	vMerge := viper.New()
	_ = vMerge.MergeConfigMap(base)
	if vFile := getFileViper(); vFile != nil {
		_ = vMerge.MergeConfigMap(vFile.AllSettings())
	}

	return vMerge.AllSettings()
}
