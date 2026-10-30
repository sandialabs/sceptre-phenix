package cmd

import (
	"context"
	"errors"
	"fmt"
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
)

// The groups --skip takes in place of a check id.
const (
	doctorGroupConfig   = "config"
	doctorGroupEnv      = "env"
	doctorGroupSettings = "settings"
	doctorGroupFiles    = "files"
	doctorGroupDirs     = "dirs"
	doctorGroupUI       = "ui"
	doctorGroupMinimega = "minimega"
)

// doctorInput is everything the checks and the report header read, resolved
// once, so the checks read no global state and tests can build their own.
type doctorInput struct {
	env      doctorEnv
	config   doctorConfig
	known    []string
	settings map[string]resolvedSetting
	probe    func(base string, timeout time.Duration) (string, error)
	timeout  time.Duration
	now      func() time.Time
	hostname func() (string, error)
	system   doctorSystem
	uid, gid int
	userName string
	cwd      string
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
	used   string // the config file read, or ""
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

	return &doctorInput{
		env:      env,
		config:   config,
		known:    known,
		settings: resolveSettings(flags, config.keys, known, env.lookup),
		probe:    probeMinimega,
		timeout:  timeout,
		now:      time.Now,
		hostname: os.Hostname,
		system:   osDoctorSystem(),
		uid:      uid,
		gid:      os.Getgid(),
		userName: name,
		cwd:      cwd,
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

// resolveSettings reads each known setting as phenix does: a global setting
// prefers the config file to the environment, as getEffectiveString does.
func resolveSettings(
	flags *pflag.FlagSet,
	fileKeys map[string]bool,
	known []string,
	lookupEnv func(string) (string, bool),
) map[string]resolvedSetting {
	var (
		fileViper = getFileViper()
		settings  = make(map[string]resolvedSetting, len(known))
	)

	for _, key := range known {
		var (
			changed = flags.Lookup(key) != nil && flags.Changed(key)
			root    = rootCmd.PersistentFlags().Lookup(key) != nil
			s       = resolvedSetting{value: viper.Get(key), text: viper.GetString(key), source: ""}
		)

		fileFirst := root && !changed && fileViper != nil && fileViper.IsSet(key)
		if fileFirst {
			s.value, s.text = fileViper.Get(key), fileViper.GetString(key)
		}

		s.source = settingSource(key, changed, fileFirst, fileKeys[key], lookupEnv)
		settings[key] = s
	}

	return settings
}

// doctorConfigFiles parses the config and users files found in paths, as the root
// command does, and returns their checks and keys; a missing users file gets no check.
func doctorConfigFiles(paths []string) doctorConfig {
	config := doctorConfig{checks: make(map[string]doctorCheck), keys: make(map[string]bool), used: ""}

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
			}
		case errors.As(err, &viper.ConfigFileNotFoundError{}):
			if name == usersConfigName {
				continue
			}

			check.Status = doctorWarn
			check.Detail = fmt.Sprintf("none found in %s, so phenix uses environment variables and defaults", strings.Join(paths, ", "))
		default:
			check.Status = doctorFail
			check.Detail = fmt.Sprintf("%s does not parse, so phenix ignores it: %v", v.ConfigFileUsed(), err)
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
//nolint:funlen // one entry per check
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
			id: "env", group: doctorGroupEnv, name: "environment variables",
			hint: func(c doctorCheck) string { return "fix or unset " + c.Name },
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return envChecks(in.env, in.sources())
			},
		},
		settingDef("bridge-mode", "set bridge-mode to manual or auto; every phenix command refuses another value"),
		settingDef("deploy-mode",
			"set deploy-mode to all, no-headnode or only-headnode, or unset it; every phenix command refuses another value"),
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
			id: "ui.jwt-signing-key", group: doctorGroupUI, name: "ui.jwt-signing-key",
			hint: hintText("set ui.jwt-signing-key to a long random secret to require sign-in"),
			run: func(_ context.Context, in *doctorInput) []doctorCheck {
				return one(jwtKeyCheck(in.text("ui.jwt-signing-key")))
			},
		},
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
			id: minimegaCheckName, group: doctorGroupMinimega, name: minimegaCheckName,
			hint: hintText("start minimega, or set base-dir.minimega to the directory that holds its socket"),
			run: func(ctx context.Context, in *doctorInput) []doctorCheck {
				base := in.text("base-dir.minimega")
				probe := func(timeout time.Duration) (string, error) { return in.probe(base, timeout) }

				return one(minimegaCheck(ctx, base, in.timeout, probe))
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

// runDoctorChecks runs each check in defs against in, in order, and gives a
// skip row for each one skip names.
func runDoctorChecks(ctx context.Context, in *doctorInput, defs []doctorCheckDef, skip map[string]bool) []doctorResult {
	var results []doctorResult

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

	return results
}
