package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"

	"phenix/api/disk"
	"phenix/store"
	"phenix/web"
	"phenix/web/health"
)

// The groups --skip takes in place of a check id.
const (
	doctorGroupConfig   = "config"
	doctorGroupEnv      = "env"
	doctorGroupSettings = "settings"
	doctorGroupFiles    = "files"
	doctorGroupDirs     = "dirs"
	doctorGroupUI       = "ui"
	doctorGroupAuth     = "auth"
	doctorGroupTLS      = "tls"
	doctorGroupSocket   = "socket"
	doctorGroupServer   = "server"
	doctorGroupMinimega = "minimega"
	doctorGroupTools    = "tools"
	doctorGroupImages   = "images"
	doctorGroupFeatures = "features"
)

// doctorInput is everything the checks and the report header read, resolved
// once, so the checks read no global state and tests can build their own.
type doctorInput struct {
	env      doctorEnv
	config   doctorConfig
	known    []string
	settings map[string]resolvedSetting
	probe    func(base string, timeout time.Duration) (string, error)
	minimega minimegaRunner
	server   func(socket string, timeout time.Duration) *http.Client
	qemuImg  func(ctx context.Context, path string) (string, error) // its version
	statfs   func(path string) (health.DiskStat, error)
	images   func(ctx context.Context, dir string) (disk.ChainScan, error)
	command  func(ctx context.Context, name string, args ...string) ([]byte, error) // stdout and stderr
	// storeConfigs reads the configs of a kind from the store file, waiting at most timeout for its lock
	storeConfigs func(ctx context.Context, path, kind string, timeout time.Duration) (store.Configs, error)
	timeout      time.Duration
	now          func() time.Time
	hostname     func() (string, error)
	addrs        func() ([]string, error) // the host's interface addresses, which --report redacts
	system       doctorSystem
	uid, gid     int
	userName     string
	cwd          string
	found        *doctorFound // set for each run by runDoctorChecks

	configPaths  []string // searched for the config and users files, in order
	settingsUsed string   // the config file the settings commands change, before the default
	phenixUID    string   // the user phenix resolves: SUDO_USER's under sudo phenix
	home         string   // phenixUID's home directory
	systemStore  string   // the store file of a phenix ui that runs as root
}

// resolvedSetting is a setting's value as phenix reads it and where it came
// from. value may be a secret, so the report redacts it.
type resolvedSetting struct {
	value  any
	text   string
	source string
}

// doctorConfig is what doctor read from the config and users files.
type doctorConfig struct {
	checks map[string]doctorCheck // by file name; no users entry when it is missing
	keys   map[string]bool
	values map[string]fileSetting // by key; the users file's win, as phenix merges it last
	used   string                 // the config file read, or ""
}

// fileSetting is a setting's value in a config file. value may be a secret.
type fileSetting struct {
	file  string
	value any
}

// doctorSystem is the operating system doctor runs on.
type doctorSystem struct {
	os, kernel, arch string
}

// doctorCheckDef is one registered check: run returns its rows, and hint
// what to do about a row that warns or fails.
type doctorCheckDef struct {
	id, group, name string
	hint            func(doctorCheck) string
	run             func(context.Context, *doctorInput) []doctorCheck
}

// newDoctorInput resolves the settings from flags, viper and the config files,
// as the root command would, and reads the rest from the operating system.
func newDoctorInput(flags *pflag.FlagSet, timeout time.Duration) *doctorInput {
	env := osDoctorEnv()
	config := doctorConfigFiles(configSearchPaths)
	known := knownSettings()
	uid := os.Getuid()

	var name string
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		name = u.Username
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown: " + err.Error()
	}

	phenixUID, home := getCurrentUserInfo()
	systemStore, _ := store.BoltPath(rootDefaultStore)

	return &doctorInput{
		env:      env,
		config:   config,
		known:    known,
		settings: resolveSettings(flags, config.keys, known, env.lookup),
		probe:    probeMinimega,
		minimega: runMinimegaAt,
		server:   doctorServerClient,
		qemuImg:  health.QemuImgVersion,
		statfs:   health.Statfs,
		images:   disk.BrokenImages,
		command:  runDoctorCommand,
		timeout:  timeout,

		storeConfigs: readStoreConfigs,
		now:          time.Now,
		hostname:     os.Hostname,
		addrs:        interfaceAddrs,
		system:       osDoctorSystem(),
		uid:          uid,
		gid:          os.Getgid(),
		userName:     name,
		cwd:          cwd,
		found:        nil,

		configPaths:  configSearchPaths,
		settingsUsed: viper.ConfigFileUsed(),
		phenixUID:    phenixUID,
		home:         home,
		systemStore:  systemStore,
	}
}

func osDoctorSystem() doctorSystem {
	system := doctorSystem{os: runtime.GOOS, kernel: "unknown", arch: runtime.GOARCH}

	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		system.kernel = unix.ByteSliceToString(uts.Release[:])
	}

	return system
}

// text is the value of setting key as phenix reads it.
func (in *doctorInput) text(key string) string {
	return in.settings[key].text
}

// sources maps each setting to where its value came from.
func (in *doctorInput) sources() map[string]string {
	sources := make(map[string]string, len(in.settings))
	for key, s := range in.settings {
		sources[key] = s.source
	}

	return sources
}

// resolveSettings reads each known setting as phenix does, through viper: a set
// flag, then the PHENIX_ variable, then the config files, then the default.
func resolveSettings(
	flags *pflag.FlagSet,
	fileKeys map[string]bool,
	known []string,
	lookupEnv func(string) (string, bool),
) map[string]resolvedSetting {
	settings := make(map[string]resolvedSetting, len(known))

	for _, key := range known {
		changed := flags.Lookup(key) != nil && flags.Changed(key)

		settings[key] = resolvedSetting{
			value:  viper.Get(key),
			text:   viper.GetString(key),
			source: settingSource(key, changed, fileKeys[key], lookupEnv),
		}
	}

	return settings
}

// doctorConfigFiles parses the config and users files found in paths, as the root
// command does, and returns their checks and keys; a missing users file gets no check.
func doctorConfigFiles(paths []string) doctorConfig {
	config := doctorConfig{
		checks: make(map[string]doctorCheck), keys: make(map[string]bool), values: make(map[string]fileSetting), used: "",
	}

	for _, name := range []string{"config", usersConfigName} {
		v := viper.New()
		v.SetConfigName(name)

		for _, path := range paths {
			v.AddConfigPath(path)
		}

		check := doctorCheck{Name: name + " file", Status: doctorPass, Detail: ""}
		err := v.ReadInConfig()

		switch {
		case err == nil:
			check.Detail = v.ConfigFileUsed()

			for _, key := range v.AllKeys() {
				config.keys[key] = true
				config.values[key] = fileSetting{file: v.ConfigFileUsed(), value: v.Get(key)}
			}
		case errors.As(err, &viper.ConfigFileNotFoundError{}):
			if name == usersConfigName {
				continue
			}

			check.Status = doctorWarn
			check.Detail = fmt.Sprintf("none found in %s, so phenix uses environment variables and defaults", strings.Join(paths, ", "))
		default:
			check.Status = doctorFail
			check.Detail = fmt.Sprintf("%s %s, so phenix ignores it", v.ConfigFileUsed(), configReadProblem(err))
		}

		if name == "config" {
			config.used = v.ConfigFileUsed()
		}

		config.checks[name] = check
	}

	return config
}

// configFileRows is the check of the file name, if doctor read one.
func configFileRows(in *doctorInput, name string) []doctorCheck {
	if check, ok := in.config.checks[name]; ok {
		return []doctorCheck{check}
	}

	return nil
}

func hintText(text string) func(doctorCheck) string {
	return func(doctorCheck) string { return text }
}

// baseDirDef registers the base directory check of key; sub, when set, is the
// directory under base-dir.phenix it defaults to.
func baseDirDef(key, sub string, needWrite bool) doctorCheckDef {
	return doctorCheckDef{
		id: key, group: doctorGroupDirs, name: key,
		hint: hintText("create the directory with access for this user, or set " + key + " to one it can use"),
		run: func(_ context.Context, in *doctorInput) []doctorCheck {
			dir := in.text(key)
			if sub != "" {
				dir = derivedBaseDir(dir, in.text("base-dir.phenix"), sub)
			}

			return []doctorCheck{in.env.baseDirCheck(key, dir, needWrite)}
		},
	}
}

// envOverridesCheck warns about each PHENIX_ variable that phenix reads in place
// of a different value in a config file. It names both, never either value.
func envOverridesCheck(
	settings map[string]resolvedSetting,
	files map[string]fileSetting,
	lookupEnv func(string) (string, bool),
) doctorCheck {
	var overrides []string

	for _, key := range slices.Sorted(maps.Keys(files)) {
		if settings[key].source != sourceEnv {
			continue
		}

		name := settingEnvVar(key)
		value, _ := lookupEnv(name)

		if f := files[key]; !sameSettingValue(value, f.value) {
			overrides = append(overrides, fmt.Sprintf("%s overrides %s in %s", name, key, f.file))
		}
	}

	if len(overrides) == 0 {
		return passCheck("no PHENIX_ variable overrides a different config file value")
	}

	return warnCheck(strings.Join(overrides, ". "))
}

// sameSettingValue reports whether env, a variable's value, is the config
// file's value; viper splits a list variable on spaces.
func sameSettingValue(env string, file any) bool {
	switch v := file.(type) {
	case nil:
		return env == ""
	case []any:
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = fmt.Sprint(p)
		}

		return slices.Equal(strings.Fields(env), parts)
	}

	return env == fmt.Sprint(file)
}

// settingDef registers the check of key's value, from wherever it comes, by
// the rules of its env check. Every phenix command reads key before it runs.
func settingDef(key, hint string) doctorCheckDef {
	return doctorCheckDef{
		id: key, group: doctorGroupSettings, name: key, hint: hintText(hint),
		run: func(_ context.Context, in *doctorInput) []doctorCheck {
			check, _ := settingValueCheck(in.env, key, in.text(key))

			return []doctorCheck{check}
		},
	}
}

// doctorChecks is every check doctor runs, in the order it runs them.
//
//nolint:funlen,maintidx // one entry per check
func doctorChecks() []doctorCheckDef {
	one := func(c doctorCheck) []doctorCheck { return []doctorCheck{c} }
	parseHint := hintText("fix the YAML syntax in the file it names")

	return []doctorCheckDef{
		{
			id: "config-file", group: doctorGroupConfig, name: "config file",
			hint: func(c doctorCheck) string {
				if c.Status == doctorWarn {
					return "phenix creates config.yaml when it can write its config directory, or you can use environment variables in its place"
				}

				return parseHint(c)
			},
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return configFileRows(in, "config") },
		},
		{
			id: "users-file", group: doctorGroupConfig, name: "users file", hint: parseHint,
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return configFileRows(in, usersConfigName) },
		},
		{
			id: "config-keys", group: doctorGroupConfig, name: "config keys",
			hint: hintText("rename or remove the keys (phenix settings list shows the valid ones)"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(unknownKeysCheck(in.config.keys, in.known))
			},
		},
		{
			id: "config-paths", group: doctorGroupConfig, name: "config paths",
			hint: hintText("merge the files phenix ignores into the one it reads, then remove them"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.configPathsCheck(in.configPaths, in.cwd))
			},
		},
		{
			id: "settings-file", group: doctorGroupConfig, name: "settings file",
			hint: hintText("make the config file phenix reads a YAML file this user can write, or run phenix settings as its owner"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				target := settingsFile(in.settingsUsed, in.phenixUID, in.home)

				return one(in.env.settingsFileCheck(target, in.config.used))
			},
		},
		{
			id: "env", group: doctorGroupEnv, name: "environment variables",
			hint: func(c doctorCheck) string { return "fix or unset " + c.Name },
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return envChecks(in.env, in.sources())
			},
		},
		{
			id: "env-overrides", group: doctorGroupEnv, name: "env overrides",
			hint: hintText("unset the variable to use the config file's value, or change the file to match it"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(envOverridesCheck(in.settings, in.config.values, in.env.lookup))
			},
		},
		settingDef("bridge-mode", "set bridge-mode to manual or auto, because every phenix command refuses another value"),
		settingDef("deploy-mode",
			"set deploy-mode to all, no-headnode or only-headnode, or unset it, because every phenix command refuses another value"),
		settingDef("experiment.disk-check",
			"set experiment.disk-check to error, warn or off, or unset it, because every phenix command refuses another value"),
		settingDef(colorFlag, "set color to auto, on or off, or unset it, because every phenix command refuses another value"),
		settingDef("log.level", "set log.level to debug, info, warn, error or none"),
		settingDef("log.system.max-size", "set log.system.max-size to a whole number of megabytes, or 0 for 100"),
		settingDef("log.system.max-backups", "set log.system.max-backups to a whole number, or 0 to keep every old log"),
		settingDef("log.system.max-age", "set log.system.max-age to a whole number of days, or 0 for no limit"),
		{
			id: "store.endpoint", group: doctorGroupFiles, name: "store.endpoint",
			hint: hintText("create the directory, or set store.endpoint to a bolt:// path this user can write"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.storeCheck(in.text("store.endpoint")))
			},
		},
		{
			id: "store-file", group: doctorGroupFiles, name: "store file",
			hint: func(c doctorCheck) string {
				if c.Status == doctorWarn {
					return "copy the store to a backup while phenix ui is stopped"
				}

				return "restore the store from a backup, or move it so that phenix creates an empty one"
			},
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				path, info := in.storeFile()
				if info == nil {
					return nil
				}

				return one(in.env.storeFileCheck(path, info.Size()))
			},
		},
		{
			id: "store-owner", group: doctorGroupFiles, name: "store owner",
			hint: hintText("run phenix as the user that owns the store, or give the store to the user that runs phenix ui"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				path, info := in.storeFile()
				if info == nil {
					return nil
				}

				return in.env.storeOwnerCheck(path, info, in.uid, in.phenixUID)
			},
		},
		{
			id: "store-root", group: doctorGroupFiles, name: "store root",
			hint: func(doctorCheck) string {
				return "to use the store of a phenix ui that runs as root, run phenix in a root shell (sudo -i), " +
					"or set store.endpoint to " + rootDefaultStore
			},
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				s := in.settings["store.endpoint"]

				return in.env.storeRootCheck(s.text, s.source, in.phenixUID, in.systemStore)
			},
		},
		{
			id: "log.system.path", group: doctorGroupFiles, name: "log.system.path",
			hint: hintText("set log.system.path to a file or directory this user can write"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.systemLogCheck("log.system.path", in.text("log.system.path")))
			},
		},
		{
			id: "log.console", group: doctorGroupFiles, name: "log.console",
			hint: hintText("set log.console to stderr, stdout or a file this user can write"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				console := in.text("log.console")
				if console == "" || console == consoleStderr || console == consoleStdout {
					return nil
				}

				return one(in.env.writableFileCheck("log.console", console))
			},
		},
		baseDirDef("base-dir.phenix", "", true),
		// minimega owns its base directory; phenix only uses its socket
		baseDirDef("base-dir.minimega", "", false),
		baseDirDef("base-dir.injects", "injects", true),
		baseDirDef("base-dir.topologies", "topologies", false),
		baseDirDef("mount-dir", "mounts", true),
		{
			id: "ui.shutdown-grace-period", group: doctorGroupUI, name: "ui.shutdown-grace-period",
			hint: hintText("set ui.shutdown-grace-period to a whole number of seconds, such as 5"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				key := "ui.shutdown-grace-period"

				return one(gracePeriodCheck(viperWith(key, in.text(key))))
			},
		},
		{
			id: "ui.netflow.max-size", group: doctorGroupUI, name: "ui.netflow.max-size",
			hint: hintText("set ui.netflow.max-size to a whole number of MiB, such as 100"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				key := "ui.netflow.max-size"

				return one(netflowMaxSizeCheck(viperWith(key, in.text(key))))
			},
		},
		{
			id: "ui.jwt-signing-key", group: doctorGroupAuth, name: "ui.jwt-signing-key",
			hint: func(c doctorCheck) string {
				if c.Status == doctorFail {
					return "write a dev| key as dev|<user>|<role>, such as dev|alice|global-viewer"
				}

				return "set ui.jwt-signing-key to a long random secret to require sign-in"
			},
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(jwtKeyCheck(in.text("ui.jwt-signing-key"), in.env.roles))
			},
		},
		{
			id: "ui.jwt-lifetime", group: doctorGroupAuth, name: "ui.jwt-lifetime",
			hint: hintText("set ui.jwt-lifetime to a duration with a unit, such as 24h"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(jwtLifetimeCheck(in.text("ui.jwt-lifetime")))
			},
		},
		{
			id: "ui.users", group: doctorGroupAuth, name: "ui.users",
			hint: func(c doctorCheck) string {
				if strings.HasPrefix(c.Detail, "empty") {
					return "set ui.users to the users phenix ui creates, or change the default user's password"
				}

				return "write each user as name:password:role[:resource...], with a built-in role or one in the store"
			},
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				// as phenix ui reads it, whatever type the source gives
				v := viper.New()
				v.Set("ui.users", in.settings["ui.users"].value)
				users := v.GetStringSlice("ui.users")

				return usersSettingCheck(users, in.text("ui.jwt-signing-key"), in.env.roles)
			},
		},
		{
			id: proxyJWTKey, group: doctorGroupAuth, name: proxyJWTKey,
			hint: hintText("set ui.listen-endpoint to an address only the proxy reaches, such as 127.0.0.1:3000"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				if in.text("ui.jwt-signing-key") != proxyJWTKey {
					return nil
				}

				return one(proxyJWTCheck(in.text("ui.listen-endpoint")))
			},
		},
		{
			id: "ui.proxy-auth-header", group: doctorGroupAuth, name: "ui.proxy-auth-header",
			hint: hintText("set ui.jwt-signing-key to require sign-in, or unset ui.proxy-auth-header"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				header := in.text("ui.proxy-auth-header")
				if header == "" {
					return nil
				}

				return one(proxyHeaderCheck(in.env, header, in.text("ui.jwt-signing-key")))
			},
		},
		{
			id: "ui.tls", group: doctorGroupTLS, name: "ui.tls",
			hint: hintText("set both ui.tls-key and ui.tls-cert to a readable key and its current certificate, or neither"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.tlsCheck(in.text("ui.tls-key"), in.text("ui.tls-cert"), in.now()))
			},
		},
		{
			id: "ui.listen-endpoint", group: doctorGroupTLS, name: "ui.listen-endpoint",
			hint: hintText("set ui.listen-endpoint to host:port, such as 0.0.0.0:3000"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(listenEndpointCheck(in.env.lookupPort, in.text("ui.listen-endpoint")))
			},
		},
		{
			id: "ui.file-server-endpoint", group: doctorGroupTLS, name: "ui.file-server-endpoint",
			hint: hintText("set ui.file-server-endpoint to a port, host:port, or 0 for no file server"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(fileServerCheck(in.text("ui.file-server-endpoint")))
			},
		},
		{
			id: "unix-socket", group: doctorGroupSocket, name: "unix-socket",
			hint: hintText("set unix-socket to a path in an existing directory this user can write"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.socketCheck(in.text("unix-socket")))
			},
		},
		{
			id: "unix-socket-gid", group: doctorGroupSocket, name: "unix-socket-gid",
			hint: hintText("set unix-socket-gid to a group this user is in, run phenix ui as root, or set it to -1"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				if in.text("unix-socket") == "" {
					return one(passCheck("unused, because unix-socket is empty"))
				}

				return one(gidCheck(in.env, in.text("unix-socket-gid")))
			},
		},
		{
			id: serverCheckName, group: doctorGroupServer, name: serverCheckName, hint: serverHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck {
				bridge := in.text("bridge-mode")
				if in.settings["bridge-mode"].source == sourceDefault {
					bridge = ""
				}

				return serverChecks(ctx, in.text("unix-socket"), in.timeout, in.server, in.env.lstat, bridge, in.text("deploy-mode"))
			},
		},
		{
			id: minimegaCheckName, group: doctorGroupMinimega, name: minimegaCheckName, hint: minimegaHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck {
				base := in.text("base-dir.minimega")
				probe := func(timeout time.Duration) (string, error) { return in.probe(base, timeout) }

				check := minimegaCheck(ctx, base, in.timeout, probe)
				in.found.minimegaUp = check.Status == doctorPass

				return one(check)
			},
		},
		{
			id: "minimega-env", group: doctorGroupMinimega, name: "minimega-env", hint: minimegaEnvHint,
			run: minimegaEnvChecks,
		},
		{
			id: minimegaLimitsName, group: doctorGroupMinimega, name: minimegaLimitsName, hint: minimegaLimitsHint,
			run: minimegaLimitsChecks,
		},
		{
			id: stateDriftName, group: doctorGroupMinimega, name: stateDriftName, hint: stateDriftHint, run: stateDriftChecks,
		},
		{id: vlansName, group: doctorGroupMinimega, name: vlansName, hint: vlansHint, run: vlansChecks},
		{
			id: bridgeTrunkName, group: doctorGroupMinimega, name: bridgeTrunkName, hint: bridgeTrunkHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(bridgeTrunkCheck(ctx, in)) },
		},
		{
			id: scheduleHostsName, group: doctorGroupMinimega, name: scheduleHostsName, hint: scheduleHostsHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(scheduleHostsCheck(ctx, in)) },
		},
		{
			id: ccClientsName, group: doctorGroupMinimega, name: ccClientsName, hint: ccClientsHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(ccClientsCheck(ctx, in)) },
		},
		{
			id: "qemu-img", group: doctorGroupTools, name: "qemu-img",
			hint: hintText("install qemu-img (the qemu-utils package on Debian and Ubuntu)"),
			run: func(ctx context.Context, in *doctorInput) []doctorCheck {
				return one(qemuImgCheck(ctx, in.timeout, in.env.lookPath, in.qemuImg))
			},
		},
		{
			id: "vmdb2", group: doctorGroupTools, name: "vmdb2",
			hint: hintText("install vmdb2 to build images with phenix image build, or skip this check with --skip vmdb2"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(toolCheck(in.env.lookPath, "vmdb2", doctorFail, "phenix image build fails"))
			},
		},
		{
			id: "filebeat", group: doctorGroupTools, name: "filebeat",
			hint: hintText("install filebeat for the SCORCH components that enable it"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(toolCheck(in.env.lookPath, "filebeat", doctorWarn,
					"SCORCH starts no Filebeat, even for a component that enables it"))
			},
		},
		{
			id: "apps", group: doctorGroupTools, name: "apps", hint: nil,
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return one(in.env.appsCheck()) },
		},
		{
			id: "disk", group: doctorGroupTools, name: "disk",
			hint: hintText("increase the free space on the filesystem that the row names"),
			run:  diskChecks,
		},
		{
			id: kernelModulesName, group: doctorGroupTools, name: kernelModulesName, hint: kernelModulesHint,
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return one(kernelModulesCheck(in)) },
		},
		{
			id: nbdName, group: doctorGroupTools, name: nbdName, hint: nbdHint,
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return one(nbdCheck(in)) },
		},
		{
			id: openvswitchName, group: doctorGroupTools, name: openvswitchName, hint: openvswitchHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(openvswitchCheck(ctx, in)) },
		},
		{
			id: imageBuildName, group: doctorGroupTools, name: imageBuildName, hint: imageBuildHint,
			run: imageBuildChecks,
		},
		{
			id: "disk-chains", group: doctorGroupImages, name: diskChainsName, hint: diskChainsHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(diskChainsCheck(ctx, in)) },
		},
		{
			id: "disk-formats", group: doctorGroupImages, name: diskFormatsName, hint: diskFormatsHint,
			run: diskFormatsChecks,
		},
		{
			id: writableImagesName, group: doctorGroupImages, name: writableImagesName, hint: writableImagesHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(writableImagesCheck(ctx, in)) },
		},
		{
			id: leftoversName, group: doctorGroupImages, name: leftoversName, hint: leftoversHint, run: leftoversChecks,
		},
		{
			id: diskSpaceName, group: doctorGroupImages, name: diskSpaceName, hint: diskSpaceHint, run: diskSpaceChecks,
		},
		{
			id: imageLocksName, group: doctorGroupImages, name: imageLocksName, hint: imageLocksHint,
			run: func(ctx context.Context, in *doctorInput) []doctorCheck { return one(imageLocksCheck(ctx, in)) },
		},
		{
			id: web.FeatureTunnelerDownload, group: doctorGroupFeatures, name: web.FeatureTunnelerDownload,
			hint: hintText("make " + web.TunnelerDownloadDir + " a directory of phenix-tunneler builds, or remove it"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.tunnelerDownloadCheck(in.cwd))
			},
		},
		{
			id: web.FeatureWebShark, group: doctorGroupFeatures, name: web.FeatureWebShark, hint: nil,
			run: func(_ context.Context, in *doctorInput) []doctorCheck { return one(in.env.webSharkCheck(in.cwd)) },
		},
		{
			id: "ui.features", group: doctorGroupFeatures, name: "ui.features", hint: featuresHint,
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				// as phenix ui reads it, whatever type the source gives
				v := viper.New()
				v.Set("ui.features", in.settings["ui.features"].value)

				return one(in.env.featuresSettingCheck(
					v.GetStringSlice("ui.features"), in.cwd, in.text("ui.file-server-endpoint"),
				))
			},
		},
		{
			id: "ui.logs.minimega-path", group: doctorGroupFeatures, name: "ui.logs.minimega-path",
			hint: hintText("set ui.logs.minimega-path to the file minimega logs to, readable by this user"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(in.env.minimegaLogCheck(in.text("ui.logs.minimega-path"), in.found.minimegaArgs["logfile"]))
			},
		},
	}
}

// parseDoctorSkip returns the ids of the checks names picks out, each a check
// id or a group, and fails for a name that is neither.
func parseDoctorSkip(names []string, defs []doctorCheckDef) (map[string]bool, error) {
	skip := make(map[string]bool)

	var unknown []string

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		found := false

		for _, def := range defs {
			if def.id == name || def.group == name {
				skip[def.id], found = true, true
			}
		}

		if !found {
			unknown = append(unknown, strconv.Quote(name))
		}
	}

	if len(unknown) > 0 {
		ids, groups := make([]string, 0, len(defs)), make([]string, 0, len(defs))

		for _, def := range defs {
			ids = append(ids, def.id)
			groups = append(groups, def.group)
		}

		return nil, fmt.Errorf( //nolint:err113 // lists the valid names
			"--skip: unknown check %s. Checks: %s. Groups: %s",
			strings.Join(unknown, ", "), strings.Join(ids, ", "), strings.Join(slices.Compact(groups), ", "),
		)
	}

	return skip, nil
}

// runDoctorChecks runs each check in defs against a copy of input, in order,
// and gives a skip row for each one skip names.
func runDoctorChecks(ctx context.Context, input *doctorInput, defs []doctorCheckDef, skip map[string]bool) []doctorResult {
	results, _ := runDoctorChecksFound(ctx, input, defs, skip)

	return results
}

// runDoctorChecksFound is runDoctorChecks, and also returns what the checks
// found, which the report's redactor reads.
func runDoctorChecksFound(
	ctx context.Context, input *doctorInput, defs []doctorCheckDef, skip map[string]bool,
) ([]doctorResult, *doctorFound) {
	var results []doctorResult

	run := *input
	run.found = &doctorFound{
		minimegaUp: false, minimegaArgs: nil, images: nil, experiments: nil, inventory: nil, ovsShow: nil, leftovers: nil,
		mesh: nil,
	}
	in := &run

	for _, def := range defs {
		if skip[def.id] {
			results = append(results, doctorResult{
				ID: def.id, Group: def.group, Name: def.name, Status: doctorSkip, Detail: "skipped by --skip", Hint: "",
			})

			continue
		}

		for _, check := range def.run(ctx, in) {
			if check.Name == "" {
				check.Name = def.name
			}

			result := doctorResult{
				ID: def.id, Group: def.group, Name: check.Name, Status: check.Status, Detail: check.Detail, Hint: "",
			}
			if check.Status != doctorPass && def.hint != nil {
				result.Hint = def.hint(check)
			}

			results = append(results, result)
		}
	}

	return results, run.found
}
