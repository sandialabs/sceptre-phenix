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
	"regexp"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"phenix/api/config"
	_ "phenix/api/scorch"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/util/termcolor"
	"phenix/web"
)

const (
	appName              = "phenix"
	configDir            = "/etc/phenix"
	defaultMaxLogSize    = 100
	defaultMaxLogBackups = 3
	defaultMaxLogAge     = 90
	rootDefaultLogPath   = "/var/log/phenix/phenix.log"
	rootDefaultStore     = "bolt:///etc/phenix/store.bdb"

	// DocDefaultAnnotation on a flag holds the default the CLI reference shows in place of
	// one that depends on the user running phenix.
	DocDefaultAnnotation = "phenix_doc_default"

	consoleStderr   = "stderr"
	consoleStdout   = "stdout"
	usersConfigName = "users"
)

var (
	// configSearchPaths are the directories searched, in order, for the config
	// and users files.
	configSearchPaths []string //nolint:gochecknoglobals // set once by init

	phenixBase         string   //nolint:gochecknoglobals // global flag
	minimegaBase       string   //nolint:gochecknoglobals // global flag
	mountDir           string   //nolint:gochecknoglobals // global flag
	hostnameSuffixes   string   //nolint:gochecknoglobals // global flag
	storeEndpoint      string   //nolint:gochecknoglobals // global flag
	currentConsoleFile *os.File //nolint:gochecknoglobals // global state
)

//nolint:gochecknoglobals // root command
var rootCmd = &cobra.Command{
	Use:   appName,
	Short: "A cli application for phēnix",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		common.UnixSocket = viper.GetString("unix-socket") //nolint:reassign // configuration injection

		// Initialize bridge mode and use GRE mesh options with values set locally
		// by user. Later they will be forcefully enabled if they're enabled at the
		// server. This must be done before getting options from the server (unlike
		// deploy mode option).

		err := common.SetBridgeMode(viper.GetString("bridge-mode"))
		if err != nil {
			return fmt.Errorf("setting user-specified bridge mode: %w", err)
		}

		common.UseGREMesh = viper.GetBool("use-gre-mesh") //nolint:reassign // configuration injection

		// check for global options set by UI server
		if common.UnixSocket != "" {
			cli := serverOptionsClient(common.UnixSocket)

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
		err = common.SetDeployMode(viper.GetString("deploy-mode"))
		if err != nil {
			return fmt.Errorf("setting user-specified deploy mode: %w", err)
		}

		if err := common.SetDiskCheck(viper.GetString("experiment.disk-check")); err != nil {
			return fmt.Errorf("%w (options: error | warn | off)", err)
		}

		if _, err := colorMode(); err != nil {
			return err
		}

		var logOutput io.Writer = os.Stderr
		if out := viper.GetString("log.console"); out != consoleStderr {
			if out == consoleStdout {
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

		plog.NewPhenixHandler(logOutput, outputPalette(logOutput).On())
		plog.SetLevelText(viper.GetString("log.level"))

		common.PhenixBase = viper.GetString("base-dir.phenix") //nolint:reassign // configuration injection

		common.InjectsBase = derivedBaseDir( //nolint:reassign // configuration injection
			viper.GetString("base-dir.injects"),
			common.PhenixBase,
			"injects",
		)
		common.TopologiesBase = derivedBaseDir( //nolint:reassign // configuration injection
			viper.GetString("base-dir.topologies"),
			common.PhenixBase,
			"topologies",
		)
		common.MinimegaBase = viper.GetString("base-dir.minimega")     //nolint:reassign // configuration injection
		common.MountBase = viper.GetString("mount-dir")                //nolint:reassign // configuration injection
		common.HostnameSuffixes = viper.GetString("hostname-suffixes") //nolint:reassign // configuration injection

		endpoint := viper.GetString("store.endpoint")

		common.StoreEndpoint = endpoint //nolint:reassign // configuration injection

		if err := store.Init(store.Endpoint(endpoint)); err != nil {
			return fmt.Errorf("initializing storage: %w", err)
		}

		// Initialize default configs if not already done
		if !store.IsInitialized(store.ComponentConfigs) {
			err := config.Init()
			if err != nil {
				return fmt.Errorf("unable to initialize default configs: %w", err)
			}
		}

		logFile := viper.GetString("log.system.path")
		fileHandlerOpts := plog.GetDefaultFileHandlerOpts()
		fileHandlerOpts.MaxSize = viper.GetInt("log.system.max-size")
		fileHandlerOpts.MaxBackups = viper.GetInt("log.system.max-backups")
		fileHandlerOpts.MaxAge = viper.GetInt("log.system.max-age")
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
	SilenceUsage:  true, // don't print help when subcommands return an error
	SilenceErrors: true, // Execute prints the error, with its color
}

// serverOptionsClient returns the client with which the root command reads
// the options of the phenix server at socket. Each request is bounded by
// preflightRequestTimeout, so a server that accepts the connection and never
// answers is treated like one that is not there.
func serverOptionsClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer

				return dialer.DialContext(ctx, "unix", socket)
			},
		},
		Timeout: preflightRequestTimeout,
	}
}

func argsWithUsage(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return fmt.Errorf("%w\n\n%s", err, strings.TrimSpace(cmd.UsageString()))
		}

		return nil
	}
}

// Root returns the phenix root command, for tools that walk the command tree.
func Root() *cobra.Command {
	return rootCmd
}

// Execute runs the phenix command that [os.Args] names, and exits with its
// error's code after it prints the error.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		printCommandError(os.Stderr, os.Args[1:], err)
		os.Exit(exitCode(err))
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
		defaultLogPath = rootDefaultLogPath
		defaultStore = rootDefaultStore
	} else {
		defaultConfigDir = filepath.Join(home, ".config", appName)
		defaultLogPath = filepath.Join(home, "phenix_logs", "phenix.log")
		defaultStore = fmt.Sprintf("bolt://%s/.phenix.bdb", home)
	}

	viper.SetEnvPrefix("PHENIX")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv() // read in environment variables that match

	// Config paths - first look in current directory, then home directory (if
	// discoverable), then finally global config directory.
	configSearchPaths = []string{"."}

	if uid != "0" {
		configSearchPaths = append(configSearchPaths, defaultConfigDir)
	}

	configSearchPaths = append(configSearchPaths, configDir)

	for _, path := range configSearchPaths {
		viper.AddConfigPath(path)
	}

	watchConfig, usersFile := loadConfigFiles(viper.GetViper(), configSearchPaths, defaultConfigDir, os.Stderr)

	// both watchers run onChange, so a change to either file reloads both
	var reloadMu sync.Mutex

	onChange := func(e fsnotify.Event) {
		reloadMu.Lock()
		defer reloadMu.Unlock()

		reloadConfigFiles(viper.GetViper(), configSearchPaths, os.Stderr)
		applyConfigChange(e)
	}

	viper.OnConfigChange(onChange)

	if watchConfig {
		viper.WatchConfig()
	}

	if usersFile != "" {
		users := viper.New()
		users.SetConfigFile(usersFile)
		users.OnConfigChange(onChange)
		users.WatchConfig()
	}

	rootCmd.PersistentFlags().
		StringVar(&phenixBase, "base-dir.phenix", "/phenix", "base phenix directory")
	rootCmd.PersistentFlags().
		StringVar(&minimegaBase, "base-dir.minimega", "/tmp/minimega", "base minimega directory")
	rootCmd.PersistentFlags().
		String(
			"base-dir.injects", "",
			"base directory for staged workflow injects "+
				"(default: <base-dir.phenix>/injects; workflow apply uses the server's value unless this flag is given)",
		)
	rootCmd.PersistentFlags().
		String("base-dir.topologies", "", "base directory for topology directories (default: <base-dir.phenix>/topologies)")
	rootCmd.PersistentFlags().
		StringVar(&mountDir, "mount-dir", "", "base directory for VM filesystem mounts (default: <base-dir.phenix>/mounts)")
	rootCmd.PersistentFlags().
		StringVar(&hostnameSuffixes, "hostname-suffixes", "-minimega,-phenix", "hostname suffixes to strip")
	rootCmd.PersistentFlags().String("log.level", "info", "level to log messages at")
	rootCmd.PersistentFlags().
		String("log.console", consoleStderr, "output for console logs (text format) (stderr, stdout, or file path)")
	rootCmd.PersistentFlags().
		Int("log.system.max-size", defaultMaxLogSize, "maximum size in megabytes of the log file before it gets rotated")
	rootCmd.PersistentFlags().
		Int("log.system.max-backups", defaultMaxLogBackups, "maximum number of old log files to retain")
	rootCmd.PersistentFlags().
		Int("log.system.max-age", defaultMaxLogAge, "maximum number of days to retain old log files")
	rootCmd.PersistentFlags().
		String(
			"bridge-mode",
			string(common.BridgeModeManual),
			"bridge naming mode for experiments ('auto' uses experiment name for bridge; 'manual' uses user-specified bridge name, or 'phenix' if not specified; a phenix ui in 'auto' mode overrides it) (options: manual | auto)",
		)
	rootCmd.PersistentFlags().
		String("deploy-mode", "", "deploy mode for minimega VMs (options: all | no-headnode | only-headnode)")
	rootCmd.PersistentFlags().
		Bool("use-gre-mesh", false, "use GRE tunnels between mesh nodes for VLAN trunking")
	rootCmd.PersistentFlags().
		String(
			"experiment.disk-check", string(common.DiskCheckError),
			"what a start does when a VM disk image or CD-ROM image is missing or its backing chain is broken "+
				"('error' refuses the start, 'warn' starts with a warning, 'off' does not check) (options: error | warn | off)",
		)
	rootCmd.PersistentFlags().
		String("unix-socket", "/tmp/phenix.sock", "phēnix unix socket to listen on (ui subcommand) or connect to")
	rootCmd.PersistentFlags().
		String(
			colorFlag, string(termcolor.Auto),
			"when to color the output: 'auto' colors only a terminal, and not when NO_COLOR is set or TERM is dumb. "+
				"JSON and YAML output never have color ("+colorOptions+")",
		)

	// Ensure default directories exist
	if uid == "0" {
		_ = os.MkdirAll(configDir, 0o750)
		_ = os.MkdirAll(filepath.Dir(defaultLogPath), 0o750)
	} else {
		_ = os.MkdirAll(filepath.Dir(defaultLogPath), 0o750)
	}

	rootCmd.PersistentFlags().
		StringVar(&storeEndpoint, "store.endpoint", defaultStore, "BoltDB store endpoint (bolt://<path>)")
	rootCmd.PersistentFlags().
		String("log.system.path", defaultLogPath, "path to system log (JSON format)")

	_ = rootCmd.PersistentFlags().SetAnnotation("store.endpoint", DocDefaultAnnotation, []string{
		rootDefaultStore + " as root, bolt://$HOME/.phenix.bdb otherwise",
	})
	_ = rootCmd.PersistentFlags().SetAnnotation("log.system.path", DocDefaultAnnotation, []string{
		rootDefaultLogPath + " as root, $HOME/phenix_logs/phenix.log otherwise",
	})

	_ = viper.BindPFlags(rootCmd.PersistentFlags())
}

// applyConfigChange applies the settings phenix ui changes without a restart,
// after the config file named by e or the users file changed.
func applyConfigChange(e fsnotify.Event) {
	if strings.TrimSuffix(filepath.Base(e.Name), filepath.Ext(e.Name)) == usersConfigName {
		_ = web.ConfigureUsers(viper.GetStringSlice("ui.users"))
	}
	plog.SetLevelText(viper.GetString("log.level"))
	plog.ChangeLogFile(viper.GetString("log.system.path"))
	plog.ChangeMaxLogFileSize(viper.GetInt("log.system.max-size"))
	plog.ChangeMaxLogFileBackups(viper.GetInt("log.system.max-backups"))
	plog.ChangeMaxLogFileAge(viper.GetInt("log.system.max-age"))

	if err := common.SetDiskCheck(viper.GetString("experiment.disk-check")); err != nil {
		plog.Warn(plog.TypeSystem, "keeping the current experiment.disk-check", "mode", common.CurrentDiskCheck(), "err", err)
	}

	// Hot-swap console logger
	var logOutput io.Writer = os.Stderr
	var newFile *os.File
	if out := viper.GetString("log.console"); out != consoleStderr {
		if out == consoleStdout {
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
	plog.ChangeConsoleLogger(logOutput, outputPalette(logOutput).On())
	if currentConsoleFile != nil {
		_ = currentConsoleFile.Close()
	}
	currentConsoleFile = newFile

	// Hot-swap UI log level
	if level := viper.GetString("ui.logs.level"); level != "" {
		plog.AddHandler("ui-default", plog.NewUIHandler(level, web.PublishPhenixLog))
	} else {
		plog.AddHandler(
			"ui-default",
			plog.NewUIHandler(
				viper.GetString("log.level"),
				web.PublishPhenixLog,
			),
		)
	}

	warnRestartSettings()
}

// readConfig reads the config file into v, creating an empty one in createDir if none
// exists so it can be watched. Logging is not set up yet, so problems go to warn.
func readConfig(v *viper.Viper, createDir string, warn io.Writer) bool {
	err := v.ReadInConfig()
	if err == nil {
		return true
	}

	if !errors.As(err, &viper.ConfigFileNotFoundError{}) {
		configWarningf(warn, "ignoring config file %s: it %s", v.ConfigFileUsed(), configReadProblem(err))

		return false
	}

	if err := os.MkdirAll(createDir, 0o750); err != nil {
		configWarningf(warn, "creating config directory: %v", err)

		return false
	}

	target := filepath.Join(createDir, "config.yaml")

	if err := v.SafeWriteConfigAs(target); err != nil {
		configWarningf(warn, "creating config file %s: %v", target, err)

		return false
	}

	if err := v.ReadInConfig(); err != nil {
		configWarningf(warn, "ignoring config file %s: it %s", target, configReadProblem(err))

		return false
	}

	return true
}

// loadConfigFiles reads the config file into v, as readConfig does, and merges
// the users file in paths over it. It reports whether there is a config file to
// watch, and the users file found, or "".
func loadConfigFiles(v *viper.Viper, paths []string, createDir string, warn io.Writer) (bool, string) {
	v.SetConfigName("config")

	watch := readConfig(v, createDir, warn)

	return watch, mergeUsersConfig(v, paths, warn)
}

// reloadConfigFiles reads the config file v read again, which drops what the
// users file added, and merges the users file over it again.
func reloadConfigFiles(v *viper.Viper, paths []string, warn io.Writer) {
	if err := v.ReadInConfig(); err != nil {
		configWarningf(warn, "ignoring config file %s: it %s", v.ConfigFileUsed(), configReadProblem(err))
	}

	mergeUsersConfig(v, paths, warn)
}

// mergeUsersConfig merges the users file found in paths into v, through a viper
// of its own so v keeps the config file it read, and returns the file's path.
// A missing file is silent and gives ""; one that does not parse goes to warn.
func mergeUsersConfig(v *viper.Viper, paths []string, warn io.Writer) string {
	users := viper.New()
	users.SetConfigName(usersConfigName)

	for _, path := range paths {
		users.AddConfigPath(path)
	}

	err := users.ReadInConfig()

	switch {
	case errors.As(err, &viper.ConfigFileNotFoundError{}):
		return ""
	case err != nil:
		configWarningf(warn, "ignoring config file %s: it %s", users.ConfigFileUsed(), configReadProblem(err))
	default:
		_ = v.MergeConfigMap(users.AllSettings())
	}

	return users.ConfigFileUsed()
}

// parseErrorLine finds the line number in a YAML ("line 3") or TOML ("(3, 5)")
// parse error.
var parseErrorLine = regexp.MustCompile(`\bline (\d+)|\((\d+), \d+\)`)

// configReadProblem describes err, the failure to read a config or users
// file, as configParseError does when the file does not parse.
func configReadProblem(err error) string {
	if errors.As(err, &viper.ConfigParseError{}) {
		return configParseError(err)
	}

	return "is not readable: " + err.Error()
}

// configParseError describes err, a config or users file's parse error, by
// the line it names alone, since the error's text quotes the file and so a
// password or key.
func configParseError(err error) string {
	m := parseErrorLine.FindStringSubmatch(err.Error())
	if m == nil {
		return "does not parse"
	}

	return "does not parse at line " + m[1] + m[2]
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

	// `SUDO_USER` (and `SUDO_COMMAND`) remain set in the environment even after
	// a user has escalated to a root shell (eg. via `sudo su` or `sudo -i`), so
	// checking `SUDO_USER` alone isn't enough to determine whether phenix was
	// invoked directly via sudo (eg. `sudo phenix ...`) versus indirectly from
	// a root shell that was itself started via sudo. Only trust `SUDO_USER` if
	// `SUDO_COMMAND` indicates phenix was the command actually run via sudo.
	if u.Uid == "0" && sudo != "" && sudoRanPhenix() {
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

// sudoRanPhenix returns true if the `SUDO_COMMAND` env variable indicates the
// phenix executable was the command directly invoked via sudo (eg.
// `sudo phenix ...`). It returns false if `SUDO_COMMAND` refers to something
// else, such as `su` or `/bin/bash`, which happens when a user obtains a root
// shell via sudo (eg. `sudo su` or `sudo -i`) and then runs phenix from that
// shell -- in that case `SUDO_USER` should not be used to determine the
// config/store paths, since the command is truly being run as root.
func sudoRanPhenix() bool {
	return sudoCommandNamesPhenix(os.Getenv("SUDO_COMMAND"))
}

// sudoCommandNamesPhenix reports whether cmd, a SUDO_COMMAND value, ran phenix.
func sudoCommandNamesPhenix(cmd string) bool {
	if cmd == "" {
		return false
	}

	// `SUDO_COMMAND` may include arguments, so only consider the command name.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}

	name := filepath.Base(fields[0])

	exe, err := os.Executable()
	if err == nil && name == filepath.Base(exe) {
		return true
	}

	return name == appName
}

// derivedBaseDir returns value when it is set. Otherwise it returns the
// directory sub under phenixBase, which is the default for base-dir settings
// that derive from base-dir.phenix.
func derivedBaseDir(value, phenixBase, sub string) string {
	if value != "" {
		return value
	}

	return filepath.Join(phenixBase, sub)
}

// hideInheritedPersistentFlags marks every inherited persistent flag of cmd
// hidden and returns a function that shows them again. The flags belong to the
// parent and are shared with every other command, so leaving them hidden would
// hide them there too.
func hideInheritedPersistentFlags(cmd *cobra.Command) func() {
	var hidden []*pflag.Flag

	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}

		f.Hidden = true
		hidden = append(hidden, f)
	})

	return func() {
		for _, f := range hidden {
			f.Hidden = false
		}
	}
}

// hideInheritedHelp wraps the default help behavior so inherited persistent
// flags are hidden in the help it generates, and only there.
func hideInheritedHelp(cmd *cobra.Command) {
	defaultHelp := cmd.HelpFunc()

	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		restore := hideInheritedPersistentFlags(c)
		defer restore()

		defaultHelp(c, args)
	})
}

func addCommandToRoot(cmd *cobra.Command, excludeGlobalFlags bool) {
	if excludeGlobalFlags {
		hideInheritedHelp(cmd)
	}

	rootCmd.AddCommand(cmd)
}
