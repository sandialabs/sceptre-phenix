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
  auth, the TLS key pair and certificate, the listen endpoints, and
  the unix socket's directory and group), ask a running phenix ui on the unix
  socket for its version and its bridge and deploy modes, and check that
  minimega answers on its socket and that its files directory, base directory
  and mesh suit phenix. The disk-chains check reads the headers of the disk
  images in that files directory, for at most --timeout, and warns about each
  image whose backing chain is broken. The disk-formats check reads the same
  headers, and warns about each image in an old or unsupported format, such as
  qcow2 version 2, or one that qemu refuses. The tools and features checks look for
  qemu-img, vmdb2 and filebeat on PATH, measure free disk space, report
  whether phenix ui turns on tunneler downloads and WebShark (relative to the
  working directory), and check ui.features and ui.logs.minimega-path. --skip takes a check or a group of
  checks; an unknown name fails and lists the valid ones. Secret values, such
  as the JWT signing key and initial users, are redacted. doctor exits 1 when
  a check fails, or with --strict when one warns, and 0 otherwise.

  --report prints the whole output, text or JSON, in a fenced block to paste
  into a bug report. It replaces host names, IP addresses, user names and email
  addresses with tokens, such as <host-1>. It replaces passwords, secrets and
  the credentials in URLs with <redacted>. The same value always gets the same
  token, and a path keeps its shape, such as /home/<user-1>/images. It keeps
  localhost, 127.0.0.1, ::1, 0.0.0.0 and ::, the users root, phenix, minimega,
  nobody and admin@foo.com, and a hostname suffix, such as -phenix in
  <host-1>-phenix.

  --problems-only (-p) shows only the checks that warn or fail. The summary
  still counts every check and gives the number of hidden checks, and the exit
  status does not change. The text output colors the statuses, the summary
  and the hints when --color allows it, but never with -o json or --report.

  The kernel-modules, nbd, openvswitch and image-build checks look at this
  host, the head node, and only on Linux. kernel-modules warns when
  /lib/modules has no directory for the running kernel, or when a reboot is
  pending. nbd checks what minimega disk inject needs for injects: qemu-nbd,
  the nbd module with max_part, and a free /dev/nbdN. It also names the
  devices that a failed inject can leave. openvswitch runs ovs-vsctl show,
  and reports the Open vSwitch version and the experiment bridge. image-build
  checks the programs phenix image build needs, root and a free loop device.
  It also checks the debootstrap script and keyring of the release of each
  stored image config.

  The server group also looks at the state of phenix ui. stuck-status names
  the app runs, SCORCH runs and delayed starts that the store marks as in
  progress for a running experiment, when no phenix process can own them. It
  also names the interrupted starts. ui-limits (Linux only) warns when phenix
  ui has more than 80 percent of its soft limit of open files open. In the
  tls group, ui.tls-cert warns when the TLS certificate expires within 30
  days, or when its names do not cover the host that users reach. It fails
  when the certificate expired or is not valid yet. In the env group,
  proxy-env warns when a proxy variable is set and NO_PROXY does not cover
  the hosts of phenix ui. It names the variables, never their values.

  The images group also looks for problems that stopped or deleted
  experiments leave. writable-images warns when a VM with snapshot: false
  writes to an image that backs other images, or that more than one VM uses.
  leftovers names the snapshot files and directories of deleted experiments,
  VM file system mounts with no running VM, minimega taps and Open vSwitch
  ports that no VM owns, and mirrors that no capture owns. disk-space
  measures the filesystems of the images directory, minimega's -base and its
  log, and names the dstImg directories that a failed inject can leave.
  image-locks (Linux only) names the qemu processes that hold an image of a
  stopped experiment open, so qemu cannot lock it at the start.

  The minimega group also compares minimega with this host and the store.
  minimega-limits (Linux only) reads the open files limit of the minimega
  process from /proc, and warns when it is too low for its VMs on this node.
  It also warns when this host is short of memory or swap, or when the VMs
  ask for more memory than the host has. state-drift names the experiments
  that the store marks as running with no VMs in minimega, and the
  namespaces with VMs that no stored experiment owns. vlans warns when two
  experiments pin the same VLAN ID, or when a stopped experiment cannot get
  its VLANs now. bridge-trunk warns when a mesh of more than one node has an
  experiment bridge with no trunk or tunnel port. schedule-hosts names the VM
  schedules on nodes that are not in the mesh. cc-clients names the running
  VMs that phenix needs miniccc in but that have no client, and the clients
  that minimega logged with another miniccc version after their VMs launched.

  The apps group lists the user apps and SCORCH components on PATH.
  scenario-apps names each app and SCORCH component type of a stored scenario
  or experiment that is not built in and not on PATH. app-runtimes names each
  of those programs that this user cannot run, such as a program whose #!
  line names an interpreter that does not exist.

  Other checks compare the stored configs with this host. scenario-hosts
  names the hosts of scenario apps that are not nodes of the topology.
  inject-sources names the inject sources that do not exist. image-configs
  names the overlay directories of stored image configs that do not exist.
  rbac-users, only with sign-in on, names the users with an unknown role, and
  the names of deleted experiments and users in roles. store-fs warns when the
  store is on a network or FUSE filesystem, or when a large store has many
  free pages. secret-files warns when the group or others can read a file
  that holds a secret, such as the TLS key, and never reads the contents.

  The store-file and store-fs checks read the pages of the store file that
  they need without the lock. The other checks that read the store open it
  through BoltDB, once for each kind of config in a run. doctor opens it
  read-only, and waits at most --timeout for the lock. After one read waits
  for the lock in vain, the other reads do not wait. doctor opens no log
  file, writes no file and binds no port, so it works while phenix ui is
  stopped. Before any command runs, phenix creates the config directory with
  an empty config.yaml when it finds no config file, and the default log
  directory.
  doctor connects to the unix socket as a client only, never creating or
  removing it. It sends minimega only commands that change nothing (version,
  args, mesh status, mesh list all and namespace, then vm info, tap, capture,
  vlans, vlans range and cc clients in the namespaces that minimega lists, and
  vlans blacklist). It also reads /proc and the end of minimega's log file.
  The only programs it runs are qemu-img --version, ovs-vsctl show and
  ovs-vsctl list, which change nothing. Run it with the same flags,
  environment and user as the phenix command you are checking.`

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the phenix configuration without a running server",
		Long:  doctorDesc,
		Example: `
  # In the Docker deployment
  docker exec phenix phenix doctor

  # As JSON, waiting up to 10 seconds for minimega
  phenix doctor -o json --timeout 10s

  # Without minimega or the settings list, failing on any warning
  phenix doctor --skip minimega -x --strict

  # Only the problems
  phenix doctor -p -x

  # Output to paste into a bug report, with host data replaced
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
		"how long to wait for each answer of minimega, phenix ui or ovs-vsctl, for the store lock, "+
			"and for each check that reads many files, such as disk-chains, leftovers and image-locks")
	flags.StringP("output", "o", "text", "output format ('text' or 'json')")
	flags.StringSlice("skip", nil,
		"checks or groups of checks to skip, comma-separated or repeated (such as minimega or ui.jwt-signing-key)")
	flags.Bool("strict", false, "exit 1 when a check warns, not only when one fails")
	flags.BoolP("no-print-settings", "x", false, "leave out the settings list")
	flags.Bool("report", false,
		"print the output in a fenced block to paste into a bug report, "+
			"with host names, addresses, user names and secrets replaced")
	flags.BoolP("problems-only", "p", false,
		"show only the checks that warn or fail (the summary still counts every check)")
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
