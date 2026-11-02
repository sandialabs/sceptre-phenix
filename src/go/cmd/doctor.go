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

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"

	"phenix/api/runtimesettings"
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

	// doctorUnknownKernel is the kernel release when uname gives none.
	doctorUnknownKernel = "unknown"

	sourceFlag    = runtimesettings.SourceFlag
	sourceEnv     = runtimesettings.SourceEnv
	sourceFile    = runtimesettings.SourceFile
	sourceDefault = runtimesettings.SourceDefault

	redacted = runtimesettings.Redacted

	minimegaCheckName = "minimega"
)

var (
	errDoctorFailed = errors.New("phenix doctor found a problem")
	errDoctorWarned = errors.New("phenix doctor found a warning, which --strict makes an error")
)

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
}

// doctorReport is everything phenix doctor found.
type doctorReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	Status        doctorStatus      `json:"status"`
	Time          string            `json:"time"`
	Version       doctorVersion     `json:"version"`
	Environment   doctorEnvironment `json:"environment"`
	Checks        []doctorResult    `json:"checks"`
	HiddenChecks  int               `json:"hiddenChecks,omitempty"` // passing and skipped checks -p leaves out
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
	problems   bool              // only the checks that warn or fail
	palette    termcolor.Palette // colors the text output
}

// doctorDesc is the long help of phenix doctor.
const doctorDesc = `Check the phenix configuration without a running server

  Prints the phenix and minimega versions, every setting with its value and
  source, one row for each check (pass, warn, fail or skip), and a summary
  with a hint for each failure (and each warning with --strict). The checks look at the config files, the
  environment, the store, phenix ui, minimega, the host tools, the disk images
  and the apps. doctor changes nothing. Run it as the user, and with the flags
  and environment, of the phenix command you are checking. It exits 1 when a
  check fails, or with --strict when one warns. For each check and its fix,
  see https://phenix.sceptre.dev/latest/doctor/.`

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the phenix configuration without a running server",
		Long:  doctorDesc,
		Example: `
  # As JSON, waiting up to 10 seconds for minimega
  phenix doctor -o json --timeout 10s

  # Without minimega or the settings list, failing on any warning
  phenix doctor --skip minimega -x --strict

  # Only the problems
  phenix doctor -p -x

  # Output to paste into a bug report, with host data replaced
  phenix doctor --report`,
		Args: cobra.NoArgs,
		// The root command's hooks read settings from the server, open the
		// store and the system log, which doctor checks rather than uses.
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := doctorFlagOptions(cmd.Flags(), cmd.OutOrStdout())

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

	addDoctorFlags(cmd)

	return cmd
}

// addDoctorFlags adds the doctor command's own flags to cmd.
func addDoctorFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.Duration("timeout", defaultDoctorTimeout,
		"how long to wait for each answer of minimega, phenix ui or ovs-vsctl, for the store lock, "+
			"and for each check that reads many files, such as disk-chains, leftovers and image-locks")
	addOutputFlag(cmd, formatText, []string{formatText, FormatJSON})
	flags.StringSlice("skip", nil,
		"checks or groups of checks to skip, comma-separated or repeated (such as minimega or ui.jwt-signing-key)")
	flags.Bool("strict", false, "exit 1 when a check warns, not only when one fails")
	flags.BoolP("no-print-settings", "x", false, "leave out the settings list")
	flags.Bool("report", false,
		"print the output in a fenced block to paste into a bug report, "+
			"with host names, addresses, user names and secrets replaced")
	flags.BoolP("problems-only", "p", false,
		"show only the checks that warn or fail (the summary still counts every check)")

	_ = cmd.RegisterFlagCompletionFunc("skip", completeDoctorChecks)
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
		problems:   mustGet(flags.GetBool, "problems-only"),
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
// reports them, with what the checks found for the report's redactor.
func runDoctor(
	ctx context.Context,
	in *doctorInput,
	defs []doctorCheckDef,
	skip map[string]bool,
	opts doctorOptions,
) doctorReport {
	results, found := runDoctorChecksFound(ctx, in, defs, skip)
	run := *in
	run.found = found

	return newDoctorReport(&run, results, opts)
}

// knownSettings are the settings phenix reads: the global flags, the
// phenix ui settings and the deprecated unix-socket-gid.
func knownSettings() []string {
	var keys []string

	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { keys = append(keys, f.Name) })

	for _, name := range uiSettings {
		keys = append(keys, "ui."+name)
	}

	keys = append(keys, legacyKeyUnixSocketGID)
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
