package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"

	"phenix/store"
	"phenix/util/termcolor"
)

const (
	defaultDoctorTimeout = 3 * time.Second

	doctorPass doctorStatus = "pass"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
	doctorSkip doctorStatus = "skip"

	// doctorSchemaVersion is the version of the JSON report's layout.
	doctorSchemaVersion = 1

	sourceFlag    = "flag"
	sourceEnv     = "env"
	sourceFile    = "config file"
	sourceDefault = "default"

	redacted = "<redacted>"

	minimegaCheckName = "minimega"
)

var (
	errDoctorFailed = errors.New("phenix doctor found a problem")
	errDoctorWarned = errors.New("phenix doctor found a warning, which --strict makes an error")
)

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

// doctorResult is one row of the report: a check's outcome, the registered
// check that gave it, and what to do when it warns or fails.
type doctorResult struct {
	ID     string       `json:"id"`
	Group  string       `json:"group"`
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail"`
	Hint   string       `json:"hint,omitempty"`
}

// doctorSetting is one setting's effective value and where it came from.
type doctorSetting struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// doctorVersion names the phenix build and the minimega that answered.
type doctorVersion struct {
	Phenix   string `json:"phenix"`
	Label    string `json:"label"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Minimega string `json:"minimega"`
}

// doctorEnvironment describes where doctor ran, for a bug report.
type doctorEnvironment struct {
	OS            string `json:"os"`
	Kernel        string `json:"kernel"`
	Arch          string `json:"arch"`
	UID           int    `json:"uid"`
	GID           int    `json:"gid"`
	User          string `json:"user"`
	WorkingDir    string `json:"workingDir"`
	ConfigFile    string `json:"configFile"`
	Container     bool   `json:"container"`
	ContainerHint string `json:"containerHint,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
}

// doctorReport is everything phenix doctor found.
type doctorReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	Status        doctorStatus      `json:"status"`
	Time          string            `json:"time"`
	Version       doctorVersion     `json:"version"`
	Environment   doctorEnvironment `json:"environment"`
	Checks        []doctorResult    `json:"checks"`
	Settings      []doctorSetting   `json:"settings"`
}

// doctorOptions are the doctor command's flags.
type doctorOptions struct {
	output     string
	timeout    time.Duration
	skip       []string
	strict     bool
	noSettings bool
	report     bool
	palette    termcolor.Palette // colors the text output
}

func newDoctorCmd() *cobra.Command {
	desc := `Check the phenix configuration without a running server

  Prints a header for bug reports (the phenix and minimega versions, OS and
  kernel, user, working directory, the config file read, and whether phenix
  runs in a container), then every setting with its effective value and where
  that value came from (flag, env, config file or default), then each check,
  then a summary that names each failed and warned check, with a hint for
  each failure.

  The checks read the config and users files and report unknown keys, list
  every config and users file on the search path (the working directory,
  $HOME/.config/phenix unless root, then /etc/phenix), name the file phenix
  settings set writes, check each set environment variable phenix reads
  (PHENIX_*, PATH, EDITOR, SUDO_USER and SUDO_COMMAND) as phenix parses it,
  warn about PHENIX_* variables that name no setting or override a different
  config file value, check the settings every other phenix command refuses or
  misreads (bridge-mode, deploy-mode, experiment.disk-check, log.level and
  log.system.*), check that the store and log files can be written, that the
  store file is a whole BoltDB file this user owns and that no root phenix ui
  uses another store, and that the base directories exist, check what phenix
  ui reads at startup (the JWT signing key and lifetime, initial users, proxy
  auth, the TLS key pair and certificate expiry, the listen endpoints, and
  the unix socket's directory and group), ask a running phenix ui on the unix
  socket for its version and its bridge and deploy modes, and check that
  minimega answers on its socket and that its files directory, base directory
  and mesh suit phenix. The disk-chains check reads the headers of the disk
  images in that files directory, for at most --timeout, and warns about each
  image whose backing chain is broken. The disk-formats check reads the same
  headers, and warns about each image in an old or unsupported format, such as
  qcow2 version 2, or one that qemu refuses. The tools and features checks look for
  qemu-img, vmdb2 and filebeat on PATH, list the user apps and SCORCH
  components there, measure free disk space, report whether phenix ui turns
  on tunneler downloads and WebShark (relative to the working directory), and
  check ui.features and ui.logs.minimega-path. --skip takes a check or a group of
  checks; an unknown name fails and lists the valid ones. Secret values, such
  as the JWT signing key and initial users, are redacted. doctor exits 1 when
  a check fails, or with --strict when one warns, and 0 otherwise.

  --report adds the hostname to the header and prints the whole output in a
  fenced block to paste into a bug report. The text output colors the
  statuses, the summary and the hints when --color allows it, but never with
  -o json or --report.

  doctor never opens the store through BoltDB, which locks it, and reads
  only the first two pages of the store file. It opens no log file, writes
  no file and binds no port, so it works while phenix ui is stopped. Before
  any command runs, phenix creates the config directory with an empty
  config.yaml when it finds no config file, and the default log directory.
  doctor connects to the unix socket as a client only, never creating or
  removing it, and sends minimega only global commands that change nothing
  (version, args and mesh status). Run it with the same flags, environment
  and user as the phenix command you are checking.`

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the phenix configuration without a running server",
		Long:  desc,
		Example: `
  # In the Docker deployment
  docker exec phenix phenix doctor

  # As JSON, waiting up to 10 seconds for minimega
  phenix doctor -o json --timeout 10s

  # Without minimega or the settings list, failing on any warning
  phenix doctor --skip minimega -x --strict

  # Output to paste into a bug report
  docker exec phenix phenix doctor --report`,
		Args: cobra.NoArgs,
		// The root command's hooks read settings from the server, open the
		// store and the system log, which doctor checks rather than uses.
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := doctorFlagOptions(cmd.Flags(), cmd.OutOrStdout())
			if opts.output != "text" && opts.output != FormatJSON {
				return fmt.Errorf("unsupported output format: %s", opts.output)
			}

			defs := doctorChecks()

			skip, err := parseDoctorSkip(opts.skip, defs)
			if err != nil {
				return err
			}

			in := newDoctorInput(cmd.Flags(), opts.timeout)
			report := runDoctor(cmd.Context(), in, defs, skip, opts)

			if err := writeDoctorReport(cmd.OutOrStdout(), report, opts); err != nil {
				return err
			}

			return doctorExitError(report.Status, opts.strict)
		},
	}

	addDoctorFlags(cmd.Flags())

	return cmd
}

// addDoctorFlags adds the doctor command's own flags to flags.
func addDoctorFlags(flags *pflag.FlagSet) {
	flags.Duration("timeout", defaultDoctorTimeout,
		"how long to wait for each answer of minimega or phenix ui, and for the disk-chains and disk-formats checks")
	flags.StringP("output", "o", "text", "output format ('text' or 'json')")
	flags.StringSlice("skip", nil,
		"checks or groups of checks to skip, comma-separated or repeated (such as minimega or ui.jwt-signing-key)")
	flags.Bool("strict", false, "exit 1 when a check warns, not only when one fails")
	flags.BoolP("no-print-settings", "x", false, "leave out the settings list")
	flags.Bool("report", false,
		"add the hostname and print the output in a fenced block to paste into a bug report")
}

// doctorFlagOptions reads the doctor flags, and the colors of output to out.
func doctorFlagOptions(flags *pflag.FlagSet, out io.Writer) doctorOptions {
	return doctorOptions{
		output:     mustGet(flags.GetString, "output"),
		timeout:    mustGet(flags.GetDuration, "timeout"),
		skip:       mustGet(flags.GetStringSlice, "skip"),
		strict:     mustGet(flags.GetBool, "strict"),
		noSettings: mustGet(flags.GetBool, "no-print-settings"),
		report:     mustGet(flags.GetBool, "report"),
		palette:    outputPalette(out),
	}
}

// doctorExitError is the error doctor exits with for status: one for a
// failure, and with strict for a warning too.
func doctorExitError(status doctorStatus, strict bool) error {
	switch {
	case status == doctorFail:
		return errDoctorFailed
	case status == doctorWarn && strict:
		return errDoctorWarned
	}

	return nil
}

// runDoctor runs the checks in defs that skip leaves out against in, and
// reports them.
func runDoctor(
	ctx context.Context,
	in *doctorInput,
	defs []doctorCheckDef,
	skip map[string]bool,
	opts doctorOptions,
) doctorReport {
	return newDoctorReport(in, runDoctorChecks(ctx, in, defs, skip), opts)
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
func (e doctorEnv) storeCheck(endpoint string) doctorCheck {
	path, err := store.BoltPath(endpoint)
	if err != nil {
		return doctorCheck{Name: "store.endpoint", Status: doctorFail, Detail: err.Error()}
	}

	if dir := filepath.Dir(path); e.dirMissing(dir) {
		return doctorCheck{
			Name:   "store.endpoint",
			Status: doctorFail,
			Detail: fmt.Sprintf("cannot create %s: %s does not exist", path, dir),
		}
	}

	return e.writableFileCheck("store.endpoint", path)
}

func (e doctorEnv) dirMissing(dir string) bool {
	_, err := e.stat(dir)

	return errors.Is(err, os.ErrNotExist)
}

// writableFileCheck checks that this user can write path, or create it in its
// nearest existing parent directory.
func (e doctorEnv) writableFileCheck(name, path string) doctorCheck {
	check := doctorCheck{Name: name, Status: doctorPass, Detail: path}

	info, err := e.stat(path)

	switch {
	case err == nil && !info.Mode().IsRegular():
		check.Status, check.Detail = doctorFail, path+" is not a regular file"
	case err == nil:
		if err := e.access(path, unix.W_OK); err != nil {
			check.Status, check.Detail = doctorFail, fmt.Sprintf("%s is not writable: %v", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		dir := e.existingParent(filepath.Dir(path))

		if info, err := e.stat(dir); err == nil && !info.IsDir() {
			check.Status, check.Detail = doctorFail, fmt.Sprintf("cannot create %s: %s is not a directory", path, dir)
		} else if err := e.access(dir, unix.W_OK); err != nil {
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
func (e doctorEnv) existingParent(dir string) string {
	for {
		if _, err := e.stat(dir); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}

		dir = parent
	}
}

// baseDirCheck checks that dir exists, is readable and searchable and, with needWrite,
// writable. phenix creates some base directories on first use, so a problem only warns.
func (e doctorEnv) baseDirCheck(name, dir string, needWrite bool) doctorCheck {
	check := doctorCheck{Name: name, Status: doctorPass, Detail: dir}

	info, err := e.stat(dir)

	switch {
	case errors.Is(err, os.ErrNotExist):
		check.Status, check.Detail = doctorWarn, dir+" does not exist"
	case err != nil:
		check.Status, check.Detail = doctorFail, err.Error()
	case !info.IsDir():
		check.Status, check.Detail = doctorFail, dir+" is not a directory"
	case e.access(dir, unix.R_OK|unix.X_OK) != nil:
		check.Status, check.Detail = doctorWarn, dir+" is not readable and searchable by this user"
	case needWrite && e.access(dir, unix.W_OK) != nil:
		check.Status, check.Detail = doctorWarn, dir+" is not writable by this user"
	case !filepath.IsAbs(dir):
		check.Status, check.Detail = doctorWarn, dir+" is relative, so it depends on the working directory"
	}

	return check
}

// systemLogCheck checks log.system.path, which names the log file or, when it is a
// directory, the directory phenix writes phenix.log in.
func (e doctorEnv) systemLogCheck(name, path string) doctorCheck {
	if info, err := e.stat(path); err == nil && info.IsDir() {
		return e.writableFileCheck(name, filepath.Join(path, "phenix.log"))
	}

	return e.writableFileCheck(name, path)
}

// gracePeriodCheck fails for a ui.shutdown-grace-period in v phenix ui refuses.
func gracePeriodCheck(v *viper.Viper) doctorCheck {
	grace, err := shutdownGracePeriod(v)
	if err != nil {
		return doctorCheck{Name: "ui.shutdown-grace-period", Status: doctorFail, Detail: err.Error()}
	}

	return doctorCheck{Name: "ui.shutdown-grace-period", Status: doctorPass, Detail: grace.String()}
}

// netflowMaxSizeCheck fails for a ui.netflow.max-size in v phenix ui refuses.
func netflowMaxSizeCheck(v *viper.Viper) doctorCheck {
	check := doctorCheck{Name: "ui.netflow.max-size", Status: doctorPass, Detail: ""}

	size, err := netflowMaxSize(v)

	switch {
	case err != nil:
		check.Status, check.Detail = doctorFail, err.Error()
	case size == 0:
		check.Detail = "0, so phenix ui records no netflow captures"
	default:
		check.Detail = strconv.FormatInt(size>>mibShift, 10) + " MiB"
	}

	return check
}

// minimegaCheck asks minimega, through probe, for its version, waiting at
// most timeout, and explains a failure to connect.
func minimegaCheck(
	ctx context.Context,
	base string,
	timeout time.Duration,
	probe func(time.Duration) (string, error),
) doctorCheck {
	socket := filepath.Join(base, minimegaCheckName)

	resps, err := boundedCall(ctx, timeout, func() ([]*minicli.Response, error) {
		version, err := probe(timeout)

		return []*minicli.Response{{Response: version}}, err
	})

	var check doctorCheck
	if len(resps) > 0 {
		check = passCheck(resps[0].Response)
	}

	switch {
	case errors.Is(err, errNoAnswer):
		check = failCheck(fmt.Sprintf("no answer on %s within %s", socket, timeout))
	case err != nil:
		check = failCheck(fmt.Sprintf("no answer on %s: %v", socket, err))
		if problem := minimegaDialProblem(err); problem != "" {
			check.Detail += " (" + problem + ")"
		}
	}

	check.Name = minimegaCheckName

	return check
}

// probeMinimega sends minimega's version command to the minimega whose base
// directory is base, on a connection of its own, bounded by timeout.
func probeMinimega(base string, timeout time.Duration) (string, error) {
	resps, err := runMinimegaAt(base, "version", timeout)
	if err != nil {
		return "", err
	}

	if len(resps) == 0 {
		return "", nil
	}

	return strings.TrimSpace(resps[0].Response), nil
}

// settingSource names where key's value comes from, in viper's order: a set
// flag, the PHENIX_ variable, the config files, then the default.
func settingSource(key string, flagChanged, inFile bool, lookupEnv func(string) (string, bool)) string {
	if flagChanged {
		return sourceFlag
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

// worstStatus is fail if any check failed, warn if any warned, else pass; a
// skipped check counts as neither.
func worstStatus(results []doctorResult) doctorStatus {
	status := doctorPass

	for _, r := range results {
		switch r.Status {
		case doctorFail:
			return doctorFail
		case doctorWarn:
			status = doctorWarn
		case doctorPass, doctorSkip:
		}
	}

	return status
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
