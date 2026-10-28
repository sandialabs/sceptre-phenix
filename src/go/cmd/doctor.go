package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"

	"phenix/store"
	"phenix/util/common"
	"phenix/util/mm/mmcli"
)

const (
	defaultDoctorTimeout = 3 * time.Second

	doctorPass doctorStatus = "pass"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"

	sourceFlag    = "flag"
	sourceEnv     = "env"
	sourceFile    = "config file"
	sourceDefault = "default"

	redacted = "<redacted>"

	minimegaCheckName = "minimega"
)

var errDoctorFailed = errors.New("phenix doctor found a problem")

// secretSettings are the settings whose values doctor never prints.
//
//nolint:gochecknoglobals // constant list
var secretSettings = []string{"ui.jwt-signing-key", "ui.users"}

type doctorStatus string

// doctorCheck is the outcome of one doctor check.
type doctorCheck struct {
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail"`
}

// doctorSetting is one setting's effective value and where it came from.
type doctorSetting struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// doctorReport is everything phenix doctor found.
type doctorReport struct {
	Status   doctorStatus    `json:"status"`
	Checks   []doctorCheck   `json:"checks"`
	Settings []doctorSetting `json:"settings"`
}

func newDoctorCmd() *cobra.Command {
	desc := `Check the phenix configuration without a running server

  Reads the config and users files, reports unknown keys, checks that the
  store and log files can be written, that the base directories exist, and
  that minimega answers on its socket, then prints every setting with its
  effective value and where that value came from (flag, env, config file or
  default). Secret values, such as the JWT signing key and initial users, are
  redacted. It exits 0 when no check fails, and 1 otherwise.

  Like healthcheck, doctor opens no store, unix socket or log file, so it
  works while phenix ui is stopped. Run it with the same flags, environment
  and user as the phenix command you are checking.`

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the phenix configuration without a running server",
		Long:  desc,
		Example: `
  # In the Docker deployment
  docker exec phenix phenix doctor

  # As JSON, waiting up to 10 seconds for minimega
  phenix doctor -o json --timeout 10s`,
		Args: cobra.NoArgs,
		// The root command's hooks read settings from the server, open the
		// store and the system log, which doctor checks rather than uses.
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			output := mustGet(cmd.Flags().GetString, "output")
			if output != "text" && output != FormatJSON {
				return fmt.Errorf("unsupported output format: %s", output)
			}

			timeout, err := cmd.Flags().GetDuration("timeout")
			if err != nil {
				return fmt.Errorf("getting timeout flag: %w", err)
			}

			report := runDoctor(cmd.Context(), cmd.Flags(), timeout)

			if output == FormatJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")

				if err := enc.Encode(report); err != nil {
					return fmt.Errorf("encoding report: %w", err)
				}
			} else {
				printDoctorReport(cmd.OutOrStdout(), report)
			}

			if report.Status == doctorFail {
				return errDoctorFailed
			}

			return nil
		},
	}

	cmd.Flags().Duration("timeout", defaultDoctorTimeout, "how long to wait for minimega to answer")
	cmd.Flags().StringP("output", "o", "text", "output format ('text' or 'json')")

	return cmd
}

// runDoctor runs every check against the settings flags and the environment
// give, as the root command would resolve them.
func runDoctor(ctx context.Context, flags *pflag.FlagSet, timeout time.Duration) doctorReport {
	effective := func(key string) string { return getEffectiveString(key, flags.Changed(key)) }

	checks, fileKeys := doctorConfigFiles(configSearchPaths)
	checks = append(checks, unknownKeysCheck(fileKeys, knownSettings()))

	phenixBase := effective("base-dir.phenix")
	minimegaBase := effective("base-dir.minimega")

	checks = append(checks,
		storeCheck(effective("store.endpoint")),
		writableFileCheck("log.system.path", effective("log.system.path")),
	)

	if console := effective("log.console"); console != "" && console != consoleStderr && console != consoleStdout {
		checks = append(checks, writableFileCheck("log.console", console))
	}

	checks = append(checks,
		baseDirCheck("base-dir.phenix", phenixBase, true),
		// minimega owns its base directory; phenix only uses its socket
		baseDirCheck("base-dir.minimega", minimegaBase, false),
		baseDirCheck("base-dir.injects", derivedBaseDir(effective("base-dir.injects"), phenixBase, "injects"), true),
		baseDirCheck("base-dir.topologies", derivedBaseDir(effective("base-dir.topologies"), phenixBase, "topologies"), false),
		baseDirCheck("mount-dir", derivedBaseDir(effective("mount-dir"), phenixBase, "mounts"), true),
		jwtKeyCheck(viper.GetString("ui.jwt-signing-key")),
		gracePeriodCheck(),
	)

	common.MinimegaBase = minimegaBase //nolint:reassign // configuration injection

	checks = append(checks, minimegaCheck(ctx, minimegaBase, timeout, probeMinimega))

	return doctorReport{
		Status:   worstStatus(checks),
		Checks:   checks,
		Settings: doctorSettings(flags, fileKeys, os.LookupEnv),
	}
}

// knownSettings are the settings phenix reads: the global flags, the
// phenix ui settings and unix-socket-gid.
func knownSettings() []string {
	var keys []string

	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { keys = append(keys, f.Name) })

	for _, name := range uiSettings {
		keys = append(keys, "ui."+name)
	}

	keys = append(keys, "unix-socket-gid")
	slices.Sort(keys)

	return keys
}

// doctorConfigFiles parses the config and users files found in paths, as the root
// command does, and returns their checks and keys; a missing users file gets no check.
func doctorConfigFiles(paths []string) ([]doctorCheck, map[string]bool) {
	var (
		names  = []string{"config", usersConfigName}
		checks = make([]doctorCheck, 0, len(names))
		keys   = make(map[string]bool)
	)

	for _, name := range names {
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
				keys[key] = true
			}
		case errors.As(err, &viper.ConfigFileNotFoundError{}):
			if name == usersConfigName {
				continue
			}

			check.Status = doctorWarn
			check.Detail = fmt.Sprintf("none found in %s; using environment variables and defaults", strings.Join(paths, ", "))
		default:
			check.Status = doctorFail
			check.Detail = fmt.Sprintf("%s does not parse, so phenix ignores it: %v", v.ConfigFileUsed(), err)
		}

		checks = append(checks, check)
	}

	return checks, keys
}

// unknownKeysCheck warns about keys in the config files that phenix does not
// read, such as misspellings, which are otherwise silently ignored.
func unknownKeysCheck(fileKeys map[string]bool, known []string) doctorCheck {
	var unknown []string

	for key := range fileKeys {
		if !slices.Contains(known, key) {
			unknown = append(unknown, key)
		}
	}

	if len(unknown) == 0 {
		return doctorCheck{Name: "config keys", Status: doctorPass, Detail: "every key is a phenix setting"}
	}

	slices.Sort(unknown)

	return doctorCheck{
		Name:   "config keys",
		Status: doctorWarn,
		Detail: "not phenix settings, so phenix ignores them: " + strings.Join(unknown, ", "),
	}
}

// storeCheck checks that the store endpoint names a file phenix can write.
// BoltDB creates the file but not its directory, so that must exist.
func storeCheck(endpoint string) doctorCheck {
	path, err := store.BoltPath(endpoint)
	if err != nil {
		return doctorCheck{Name: "store.endpoint", Status: doctorFail, Detail: err.Error()}
	}

	if dir := filepath.Dir(path); dirMissing(dir) {
		return doctorCheck{
			Name:   "store.endpoint",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot create %s: %s does not exist", path, dir),
		}
	}

	return writableFileCheck("store.endpoint", path)
}

func dirMissing(dir string) bool {
	_, err := os.Stat(dir)

	return errors.Is(err, os.ErrNotExist)
}

// writableFileCheck checks that this user can write path, or create it in its
// nearest existing parent directory.
func writableFileCheck(name, path string) doctorCheck {
	check := doctorCheck{Name: name, Status: doctorPass, Detail: path}

	info, err := os.Stat(path)

	switch {
	case err == nil && !info.Mode().IsRegular():
		check.Status, check.Detail = doctorFail, path+" is not a regular file"
	case err == nil:
		if err := unix.Access(path, unix.W_OK); err != nil {
			check.Status, check.Detail = doctorFail, fmt.Sprintf("%s is not writable: %v", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		dir := existingParent(filepath.Dir(path))

		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			check.Status, check.Detail = doctorFail, fmt.Sprintf("cannot create %s: %s is not a directory", path, dir)
		} else if err := unix.Access(dir, unix.W_OK); err != nil {
			check.Status, check.Detail = doctorFail, fmt.Sprintf("cannot create %s: %s is not writable: %v", path, dir, err)
		} else {
			check.Detail = path + " (created on first use)"
		}
	default:
		check.Status, check.Detail = doctorFail, err.Error()
	}

	return check
}

// existingParent returns dir, or the nearest directory above it that exists.
func existingParent(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}

		dir = parent
	}
}

// baseDirCheck checks that dir exists and, with needWrite, is writable. phenix
// creates some base directories on first use, so a problem only warns.
func baseDirCheck(name, dir string, needWrite bool) doctorCheck {
	check := doctorCheck{Name: name, Status: doctorPass, Detail: dir}

	info, err := os.Stat(dir)

	switch {
	case errors.Is(err, os.ErrNotExist):
		check.Status, check.Detail = doctorWarn, dir+" does not exist"
	case err != nil:
		check.Status, check.Detail = doctorFail, err.Error()
	case !info.IsDir():
		check.Status, check.Detail = doctorFail, dir+" is not a directory"
	case needWrite:
		if err := unix.Access(dir, unix.W_OK); err != nil {
			check.Status, check.Detail = doctorWarn, fmt.Sprintf("%s is not writable by this user: %v", dir, err)
		}
	}

	return check
}

// jwtKeyCheck reports the authentication mode the JWT signing key selects,
// never the key.
func jwtKeyCheck(key string) doctorCheck {
	check := doctorCheck{Name: "ui.jwt-signing-key", Status: doctorPass, Detail: "set; phenix ui requires sign-in"}

	switch {
	case key == "":
		check.Status, check.Detail = doctorWarn, "empty; phenix ui serves without authentication"
	case strings.HasPrefix(key, "dev|"):
		check.Status, check.Detail = doctorWarn, "a dev| key; phenix ui uses development authentication"
	case key == "proxy-jwt":
		check.Detail = "proxy-jwt; phenix ui trusts JWTs from the proxy"
	}

	return check
}

// gracePeriodCheck fails for a ui.shutdown-grace-period phenix ui refuses.
func gracePeriodCheck() doctorCheck {
	grace, err := shutdownGracePeriod(viper.GetViper())
	if err != nil {
		return doctorCheck{Name: "ui.shutdown-grace-period", Status: doctorFail, Detail: err.Error()}
	}

	return doctorCheck{Name: "ui.shutdown-grace-period", Status: doctorPass, Detail: grace.String()}
}

// minimegaCheck asks minimega, through probe, for its version, waiting at
// most timeout.
func minimegaCheck(
	ctx context.Context,
	base string,
	timeout time.Duration,
	probe func(context.Context, time.Duration) (string, error),
) doctorCheck {
	socket := filepath.Join(base, minimegaCheckName)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type answer struct {
		version string
		err     error
	}

	answers := make(chan answer, 1)

	go func() {
		version, err := probe(ctx, timeout)
		answers <- answer{version: version, err: err}
	}()

	select {
	case a := <-answers:
		if a.err != nil {
			return doctorCheck{
				Name:   minimegaCheckName,
				Status: doctorFail,
				Detail: fmt.Sprintf("no answer on %s: %v", socket, a.err),
			}
		}

		return doctorCheck{Name: minimegaCheckName, Status: doctorPass, Detail: a.version}
	case <-ctx.Done():
		return doctorCheck{
			Name:   minimegaCheckName,
			Status: doctorFail,
			Detail: fmt.Sprintf("no answer on %s within %s", socket, timeout),
		}
	}
}

// probeMinimega sends minimega's version command on a connection of its own,
// bounded by timeout.
func probeMinimega(_ context.Context, timeout time.Duration) (string, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = "version"
	cmd.Timeout = timeout

	var (
		version string
		err     error
	)

	// every response is read, so the connection is released
	for resp := range mmcli.Run(cmd) {
		for _, r := range resp.Resp {
			if r.Error != "" {
				if err == nil {
					err = errors.New(r.Error) //nolint:err113 // minimega's error text
				}

				continue
			}

			if version == "" {
				version = strings.TrimSpace(r.Response)
			}
		}
	}

	return version, err
}

// doctorSettings lists every known setting, with secrets redacted, and where
// its value came from.
func doctorSettings(
	flags *pflag.FlagSet,
	fileKeys map[string]bool,
	lookupEnv func(string) (string, bool),
) []doctorSetting {
	var (
		fileViper = getFileViper()
		known     = knownSettings()
		settings  = make([]doctorSetting, 0, len(known))
	)

	for _, key := range known {
		var (
			changed = flags.Lookup(key) != nil && flags.Changed(key)
			root    = rootCmd.PersistentFlags().Lookup(key) != nil
			value   = viper.Get(key)
		)

		// the global settings prefer the file, as getEffectiveValue does
		fileFirst := root && !changed && fileViper != nil && fileViper.IsSet(key)
		if fileFirst {
			value = fileViper.Get(key)
		}

		settings = append(settings, doctorSetting{
			Key:    key,
			Value:  redactSetting(key, value),
			Source: settingSource(key, changed, fileFirst, fileKeys[key], lookupEnv),
		})
	}

	return settings
}

// settingSource names where key's value comes from, in viper's order: a set flag,
// the file when fileFirst, the PHENIX_ variable, the file, then the default.
func settingSource(key string, flagChanged, fileFirst, inFile bool, lookupEnv func(string) (string, bool)) string {
	switch {
	case flagChanged:
		return sourceFlag
	case fileFirst:
		return sourceFile
	}

	if value, ok := lookupEnv(settingEnvVar(key)); ok && value != "" {
		return sourceEnv
	}

	if inFile {
		return sourceFile
	}

	return sourceDefault
}

// settingEnvVar is the environment variable viper reads key from.
func settingEnvVar(key string) string {
	return "PHENIX_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(key))
}

// redactSetting hides the value of a secret setting that is set, and leaves
// an unset one as it is, so an empty JWT signing key still shows.
func redactSetting(key string, value any) any {
	if !slices.Contains(secretSettings, key) {
		return value
	}

	switch v := value.(type) {
	case nil:
		return value
	case string:
		if v == "" {
			return value
		}
	case []string:
		if len(v) == 0 {
			return value
		}
	case []any:
		if len(v) == 0 {
			return value
		}
	}

	return redacted
}

// worstStatus is fail if any check failed, warn if any warned, else pass.
func worstStatus(checks []doctorCheck) doctorStatus {
	status := doctorPass

	for _, c := range checks {
		switch c.Status {
		case doctorFail:
			return doctorFail
		case doctorWarn:
			status = doctorWarn
		case doctorPass:
		}
	}

	return status
}

func printDoctorReport(out io.Writer, report doctorReport) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0) //nolint:mnd // column padding

	_, _ = fmt.Fprintln(w, "CHECK\tSTATUS\tDETAIL")

	for _, c := range report.Checks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, strings.ToUpper(string(c.Status)), c.Detail)
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE")

	for _, s := range report.Settings {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, formatSettingValue(s.Value), s.Source)
	}

	_ = w.Flush()

	_, _ = fmt.Fprintf(out, "\n%s\n", strings.ToUpper(string(report.Status)))
}

// formatSettingValue prints a list as comma-separated values and an empty
// value as "".
func formatSettingValue(value any) string {
	var text string

	switch v := value.(type) {
	case nil:
	case []string:
		text = strings.Join(v, ",")
	case []any:
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = fmt.Sprint(p)
		}

		text = strings.Join(parts, ",")
	default:
		text = fmt.Sprint(v)
	}

	if text == "" {
		return `""`
	}

	return text
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newDoctorCmd(), true)
}
