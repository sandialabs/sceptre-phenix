package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/sys/unix"

	"phenix/api/config"
	"phenix/util/common"
	"phenix/util/editor"
	"phenix/util/termcolor"
	"phenix/web"
)

const (
	envPrefix = "PHENIX_"

	// the closest a misspelled PHENIX_ variable is to the setting it suggests.
	maxEnvSuggestDistance = 3

	// lumberjack rotates at this many megabytes when log.system.max-size is 0.
	defaultLogRotateMiB = 100

	minJWTLifetime = time.Minute
	maxPort        = 65535
)

// doctorEnv is the environment doctor checks and the lookups and file system
// reads its checks make, so tests can pass their own.
type doctorEnv struct {
	lookup      func(string) (string, bool)
	environ     func() []string
	lookPath    func(string) (string, error)
	lookupPort  func(service string) (int, error) // the port of a TCP service name
	lookupUser  func(string) (*user.User, error)
	lookupGroup func(string) (*user.Group, error)
	lookupUID   func(string) (*user.User, error)
	stat        func(string) (os.FileInfo, error)
	lstat       func(string) (os.FileInfo, error)
	access      func(string, uint32) error
	readFile    func(string) ([]byte, error)
	readAt      func(path string, off int64, n int) ([]byte, error) // at most n bytes, from off
	readDir     func(string) ([]os.DirEntry, error)
	groups      func() ([]int, error) // the groups this process is in
	roles       []string              // the built-in roles, or nil when unknown
	pageSize    int                   // the host's memory page size, as bbolt reads it
	root        bool
}

func osDoctorEnv() doctorEnv {
	return doctorEnv{
		lookup:      os.LookupEnv,
		environ:     os.Environ,
		lookPath:    exec.LookPath,
		lookupPort:  lookupTCPPort,
		lookupUser:  user.Lookup,
		lookupGroup: user.LookupGroupId,
		lookupUID:   user.LookupId,
		stat:        os.Stat,
		lstat:       os.Lstat,
		access:      unix.Access,
		readFile:    os.ReadFile,
		readAt:      osReadAt,
		readDir:     os.ReadDir,
		groups: func() ([]int, error) {
			groups, err := os.Getgroups()

			return append(groups, os.Getegid()), err
		},
		roles:    builtinRoles(),
		pageSize: os.Getpagesize(),
		root:     os.Getuid() == 0,
	}
}

// lookupTCPPort is the port of the TCP service name service.
func lookupTCPPort(service string) (int, error) {
	return net.DefaultResolver.LookupPort(context.Background(), "tcp", service)
}

// builtinRoles is every name of a built-in role, or nil if they cannot be read.
func builtinRoles() []string {
	roles, err := config.BuiltinRoles()
	if err != nil {
		return nil
	}

	return roles
}

func passCheck(detail string) doctorCheck {
	return doctorCheck{Name: "", Status: doctorPass, Detail: detail}
}

func warnCheck(detail string) doctorCheck {
	return doctorCheck{Name: "", Status: doctorWarn, Detail: detail}
}

func failCheck(detail string) doctorCheck {
	return doctorCheck{Name: "", Status: doctorFail, Detail: detail}
}

// envChecks checks, in name order, each set environment variable phenix reads and
// each other PHENIX_ variable. sources holds where each setting's value comes from.
func envChecks(env doctorEnv, sources map[string]string) []doctorCheck {
	settingVars := make(map[string]string)
	for _, key := range knownSettings() {
		settingVars[settingEnvVar(key)] = key
	}

	entries := env.environ()
	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}

	slices.Sort(names)

	checks := make([]doctorCheck, 0, len(names))

	for _, name := range slices.Compact(names) {
		value, ok := env.lookup(name)
		if !ok {
			continue
		}

		var check doctorCheck

		if key, isSetting := settingVars[name]; isSetting {
			check = settingVarCheck(env, key, value, sources[key])
		} else if strings.HasPrefix(name, envPrefix) {
			check = warnCheck(unknownEnvDetail(name, settingVars))
		} else if check, ok = otherVarCheck(env, name, value); !ok {
			continue
		}

		check.Name = name
		checks = append(checks, check)
	}

	return checks
}

// settingVarCheck checks the variable of setting key. viper ignores an empty one,
// and one that a flag overrides is unused, so both warn.
func settingVarCheck(env doctorEnv, key, value, source string) doctorCheck {
	if value == "" {
		return warnCheck("empty, so phenix ignores it")
	}

	check, _ := settingValueCheck(env, key, value)

	if source != "" && source != sourceEnv {
		return warnCheck(fmt.Sprintf("%s, but unused, because the %s sets %s", check.Detail, source, key))
	}

	return check
}

// settingValueCheck checks value as the code that reads setting key parses it, and
// reports false for a key it does not know.
//
//nolint:cyclop,funlen,gocyclo // one case per setting
func settingValueCheck(env doctorEnv, key, value string) (doctorCheck, bool) {
	switch key {
	case "base-dir.phenix", "base-dir.injects", "mount-dir":
		return env.baseDirCheck("", value, true), true
	case "base-dir.minimega", "base-dir.topologies":
		return env.baseDirCheck("", value, false), true
	case "store.endpoint":
		return env.storeCheck(value), true
	case "log.system.path":
		return env.systemLogCheck("", value), true
	case "log.console":
		if value == consoleStderr || value == consoleStdout {
			return passCheck(value), true
		}

		return env.writableFileCheck("", value), true
	case "log.level":
		return logLevelCheck(value, true, "phenix ignores it and logs at info"), true
	case "ui.logs.level":
		return logLevelCheck(value, false, "phenix ui uses info"), true
	case "log.system.max-size":
		return logMaxSizeCheck(value), true
	case "log.system.max-backups", "log.system.max-age":
		return logRetentionCheck(value), true
	case "hostname-suffixes":
		return suffixesCheck(value), true
	case "bridge-mode":
		if value == "" {
			return passCheck("not set, so phenix uses manual, or auto when phenix ui runs in auto"), true
		}

		mode, err := common.ParseBridgeMode(value)
		if err != nil {
			return failCheck(err.Error() + ", so phenix commands refuse it (options: manual | auto)"), true
		}

		return passCheck(string(mode)), true
	case "deploy-mode":
		if value == "" {
			return passCheck("not set, so phenix uses the mode of phenix ui, or no-headnode"), true
		}

		mode, err := common.ParseDeployMode(value)
		if err != nil {
			return failCheck(err.Error() + ", so phenix commands refuse it (options: all | no-headnode | only-headnode)"), true
		}

		return passCheck(string(mode)), true
	case "experiment.disk-check":
		mode, err := common.ParseDiskCheck(value)
		if err != nil {
			return failCheck(err.Error() + ", so phenix commands refuse it (options: error | warn | off)"), true
		}

		return passCheck(string(mode)), true
	case colorFlag:
		mode, err := termcolor.ParseMode(value)
		if err != nil {
			return failCheck(err.Error() + ", so phenix commands refuse it (" + colorOptions + ")"), true
		}

		return passCheck(string(mode)), true
	case "use-gre-mesh", "ui.minimega-console":
		return boolVarCheck(value), true
	case "unix-socket":
		return env.socketCheck(value), true
	case "unix-socket-gid":
		return gidCheck(env, value), true
	case "ui.listen-endpoint":
		return listenCheck(env.lookupPort, value), true
	case "ui.base-path":
		return basePathCheck(value), true
	case "ui.jwt-signing-key":
		return jwtKeyCheck(value, env.roles), true
	case "ui.jwt-lifetime":
		return jwtLifetimeCheck(value), true
	case "ui.file-server-endpoint":
		return fileServerCheck(value), true
	case "ui.proxy-auth-header":
		if !httpguts.ValidHeaderFieldName(value) {
			return failCheck(fmt.Sprintf("%q is not an HTTP header name, so no request carries it", value)), true
		}

		return passCheck(value), true
	case "ui.users":
		return usersCheck(value, env.roles), true
	case "ui.tls-key", "ui.tls-cert":
		return env.readableFileCheck(value, doctorFail), true
	case "ui.logs.minimega-path":
		return env.readableFileCheck(value, doctorWarn), true
	case "ui.features":
		return featuresCheck(value), true
	case "ui.shutdown-grace-period":
		return gracePeriodCheck(viperWith(key, value)), true
	case "ui.netflow.max-size":
		return netflowMaxSizeCheck(viperWith(key, value)), true
	}

	return failCheck(""), false
}

// viperWith is a viper holding only value for key, for the parsers that read one.
func viperWith(key, value string) *viper.Viper {
	v := viper.New()
	v.Set(key, value)

	return v
}

// otherVarCheck checks the variables phenix reads that are not settings, and
// reports false for any other.
func otherVarCheck(env doctorEnv, name, value string) (doctorCheck, bool) {
	switch name {
	case "PATH":
		return pathVarCheck(value), true
	case "EDITOR":
		return editorCheck(env, value), true
	case "SUDO_USER":
		return sudoUserCheck(env, value), true
	case "SUDO_COMMAND":
		return sudoCommandCheck(env, value), true
	}

	return failCheck(""), false
}

// unknownEnvDetail explains a PHENIX_ variable that names no setting, never
// showing its value, and suggests the setting it may misspell.
func unknownEnvDetail(name string, settingVars map[string]string) string {
	detail := "matches no phenix setting, so phenix ignores it"

	switch name {
	case "PHENIX_DIR", "PHENIX_FILES_DIR", "PHENIX_LOG_FILE", "PHENIX_DRYRUN", "PHENIX_SCORCH_STARTTIME":
		detail = "phenix sets it for the apps it runs, and ignores it itself"
	case "PHENIX_WEB_AUTH", "PHENIX_BASE_PATH":
		detail = "a build argument of the phenix image, which phenix ignores when it runs"
	}

	best, bestDistance := "", maxEnvSuggestDistance+1

	for known := range settingVars {
		d := editDistance(name, known)
		if d < bestDistance || (d == bestDistance && known < best) {
			best, bestDistance = known, d
		}
	}

	if best != "" {
		detail += " (did you mean " + best + "?)"
	}

	return detail
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := range len(a) {
		cur := make([]int, len(b)+1)
		cur[0] = i + 1

		for j := range len(b) {
			cost := 1
			if a[i] == b[j] {
				cost = 0
			}

			cur[j+1] = min(prev[j+1]+1, cur[j]+1, prev[j]+cost)
		}

		prev = cur
	}

	return prev[len(b)]
}

// intVarCheck parses value as viper reads an int setting, with [strconv.ParseInt] in base
// 0, failing when viper reads 0 instead and warning when it reads another number.
func intVarCheck(value string) (int, doctorCheck) {
	n, err := strconv.ParseInt(value, 0, 0)
	if err != nil {
		return 0, failCheck(fmt.Sprintf("%q is not a whole number, so phenix reads it as 0", value))
	}

	if text := strconv.FormatInt(n, 10); text != strings.TrimPrefix(value, "+") {
		return int(n), warnCheck(fmt.Sprintf("phenix reads %q as %s", value, text))
	}

	return int(n), passCheck(strconv.FormatInt(n, 10))
}

// boolVarCheck parses value as viper reads a bool setting, which is false
// whenever [strconv.ParseBool] fails.
func boolVarCheck(value string) doctorCheck {
	b, err := strconv.ParseBool(value)
	if err != nil {
		return failCheck(fmt.Sprintf("%q is not true or false, so phenix reads it as false", value))
	}

	return passCheck(strconv.FormatBool(b))
}

// logLevelCheck parses value as plog does; only log.level accepts none. plog
// ignores a level it cannot parse, so that only warns.
func logLevelCheck(value string, allowNone bool, fallback string) doctorCheck {
	if allowNone && strings.EqualFold(value, "none") {
		return passCheck("none, so phenix logs nothing")
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return warnCheck(fmt.Sprintf("%q is not a log level (debug, info, warn or error), so %s", value, fallback))
	}

	return passCheck(strings.ToLower(level.String()))
}

// logMaxSizeCheck fails for a negative size, with which every write to the
// system log fails; one that reads as 0 only warns, since 0 is the default size.
func logMaxSizeCheck(value string) doctorCheck {
	n, check := intVarCheck(value)

	switch {
	case check.Status == doctorFail:
		return warnCheck(fmt.Sprintf("%s, and the system log rotates at %d MB", check.Detail, defaultLogRotateMiB))
	case n < 0:
		return failCheck(fmt.Sprintf("%d is negative, so every write to the system log fails", n))
	case n == 0:
		return passCheck(fmt.Sprintf("0, so the system log rotates at %d MB", defaultLogRotateMiB))
	}

	return check
}

// logRetentionCheck checks a log.system.max-backups or max-age, where 0 and
// below keep every old log file, so no value breaks logging.
func logRetentionCheck(value string) doctorCheck {
	n, check := intVarCheck(value)

	switch {
	case check.Status == doctorFail:
		return warnCheck(check.Detail + " (no limit)")
	case n < 0:
		return warnCheck(fmt.Sprintf("%d is negative, which phenix treats as 0 (no limit)", n))
	case n == 0:
		return passCheck("0 (no limit)")
	}

	return check
}

// suffixesCheck warns about a hostname suffix with spaces around it, which
// phenix keeps, so it rarely matches.
func suffixesCheck(value string) doctorCheck {
	for suffix := range strings.SplitSeq(value, ",") {
		if suffix != strings.TrimSpace(suffix) {
			return warnCheck(fmt.Sprintf("suffix %q has spaces around it, which phenix keeps", suffix))
		}
	}

	return passCheck(value)
}

// socketCheck checks a unix socket path that phenix ui creates, after
// deleting whatever is there, and other commands connect to.
func (e doctorEnv) socketCheck(path string) doctorCheck {
	if path == "" {
		return passCheck("empty, so phenix ui serves no unix socket")
	}

	if limit := len(unix.RawSockaddrUnix{}.Path); len(path) >= limit { //nolint:exhaustruct // only its size
		return failCheck(fmt.Sprintf("%s is %d bytes, longer than a unix socket path may be (%d)", path, len(path), limit-1))
	}

	dir := filepath.Dir(path)

	info, err := e.stat(dir)
	existing, lerr := e.lstat(path)
	isSocket := lerr == nil && existing.Mode()&os.ModeSocket != 0

	switch {
	case errors.Is(err, os.ErrNotExist):
		return failCheck(fmt.Sprintf("%s does not exist, so phenix ui cannot create the socket", dir))
	case err != nil:
		return failCheck(err.Error())
	case !info.IsDir():
		return failCheck(dir + " is not a directory")
	case e.access(dir, unix.W_OK) != nil && isSocket:
		// another user's phenix ui may serve it; the server check connects to it
		return warnCheck(fmt.Sprintf("%s is not writable by this user, so phenix ui run as this user cannot replace %s", dir, path))
	case e.access(dir, unix.W_OK) != nil:
		return failCheck(fmt.Sprintf("phenix ui cannot create %s: %s is not writable by this user", path, dir))
	case lerr == nil && !isSocket:
		return warnCheck(path + " is not a socket, and phenix ui deletes it to create one")
	}

	if !filepath.IsAbs(path) {
		return warnCheck(path + " is relative, so it depends on the working directory")
	}

	return passCheck(path)
}

// gidCheck checks the group phenix ui gives its unix socket, which needs
// root or a member of the group; -1 leaves the group as it is.
func gidCheck(env doctorEnv, value string) doctorCheck {
	gid, check := intVarCheck(value)

	switch {
	case check.Status == doctorFail:
		return check
	case gid == -1:
		return passCheck("-1, so phenix ui leaves the socket's group as it is")
	case gid < -1:
		return failCheck(fmt.Sprintf("%d is not a group id, so phenix ui fails to start", gid))
	}

	group, lookupErr := env.lookupGroup(strconv.Itoa(gid))

	if env.root {
		if lookupErr != nil {
			return warnCheck(fmt.Sprintf("no group has id %d, so no group can use the socket", gid))
		}

		return check
	}

	// chown needs membership of the group, not a name for it
	groups, err := env.groups()

	switch {
	case err == nil && slices.Contains(groups, gid) && lookupErr != nil:
		return passCheck(fmt.Sprintf("%d, a group of this user with no name", gid))
	case err == nil && slices.Contains(groups, gid):
		return check
	case lookupErr != nil:
		return failCheck(fmt.Sprintf("no group has id %d, so phenix ui exits when it sets the socket's group", gid))
	case err == nil:
		return failCheck(fmt.Sprintf(
			"this user is not root or in group %d (%s), so phenix ui exits when it sets the socket's group", gid, group.Name,
		))
	}

	return check
}

// listenCheck checks a host:port phenix ui listens on, where lookupPort
// finds the port of a service name.
func listenCheck(lookupPort func(string) (int, error), value string) doctorCheck {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return failCheck(fmt.Sprintf("%q is not host:port, so phenix ui cannot listen on it", value))
	}

	n, err := strconv.Atoi(port)

	switch {
	case port == "" || (err == nil && n == 0):
		return warnCheck(value + " has no port, so phenix ui listens on a random one")
	case err == nil && (n < 0 || n > maxPort):
		return failCheck(fmt.Sprintf("port %d is out of range 1-%d, so phenix ui cannot listen on it", n, maxPort))
	case err != nil:
		if _, err := lookupPort(port); err != nil {
			return failCheck(fmt.Sprintf("port %q is not a number or service name, so phenix ui cannot listen on it", port))
		}
	}

	return passCheck(value)
}

// basePathCheck checks the path phenix ui serves under, which it gives a
// leading and a trailing slash when either is missing.
func basePathCheck(value string) doctorCheck {
	switch {
	case strings.Contains(value, "://"):
		return warnCheck(fmt.Sprintf("%q is a URL, but phenix ui uses it as a path", value))
	case strings.ContainsAny(value, " \t?#"):
		return warnCheck(fmt.Sprintf("%q has characters a URL path cannot hold as they are", value))
	}

	return passCheck(web.NormalizeBasePath(value))
}

// jwtLifetimeCheck parses value as viper reads a duration, where a number
// without a unit is nanoseconds, and fails under a minute.
func jwtLifetimeCheck(value string) doctorCheck {
	if !strings.ContainsAny(value, "nsuµmh") {
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			return failCheck(fmt.Sprintf(
				"%q has no unit, so phenix reads it as nanoseconds and tokens expire immediately. Add a unit, such as 24h", value,
			))
		}
	}

	d, err := time.ParseDuration(value)
	if err != nil {
		return failCheck(fmt.Sprintf("%q is not a duration such as 24h, so tokens expire immediately", value))
	}

	switch {
	case d <= 0:
		return failCheck(fmt.Sprintf("%s is not positive, so tokens expire immediately", d))
	case d < minJWTLifetime:
		return failCheck(fmt.Sprintf("%s is under a minute, so tokens expire before anyone can use them", d))
	}

	return passCheck(d.String())
}

// fileServerCheck checks the endpoint of the experiment file server.
func fileServerCheck(value string) doctorCheck {
	endpoint, err := web.NormalizeFileServerEndpoint(value)
	if err != nil {
		return failCheck(fmt.Sprintf("phenix ui refuses %q: %v", value, err))
	}

	if endpoint == "" {
		return passCheck("0, so the file server is off")
	}

	return passCheck(endpoint)
}

// readableFileCheck checks a file phenix reads; missing is the status of one
// that does not exist or cannot be read.
func (e doctorEnv) readableFileCheck(path string, missing doctorStatus) doctorCheck {
	info, err := e.stat(path)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return doctorCheck{Name: "", Status: missing, Detail: path + " does not exist"}
	case err != nil:
		return failCheck(err.Error())
	case !info.Mode().IsRegular():
		return failCheck(path + " is not a regular file")
	case e.access(path, unix.R_OK) != nil:
		return doctorCheck{Name: "", Status: missing, Detail: path + " is not readable by this user"}
	}

	return passCheck(path)
}

// featuresCheck checks the phenix ui features; viper splits the variable on
// spaces, not commas. The ui.features check checks what each feature needs.
func featuresCheck(value string) doctorCheck {
	features := strings.Fields(value)

	var unknown []string

	for _, feature := range features {
		if strings.Contains(feature, ",") {
			return failCheck(fmt.Sprintf(
				"%q holds a comma. phenix splits %s on spaces, so separate features with spaces",
				feature, settingEnvVar("ui.features"),
			))
		}

		if feature != web.FeatureVMMount && !slices.Contains(autoFeatures, feature) {
			unknown = append(unknown, unknownFeatureDetail(feature))
		}
	}

	if len(unknown) > 0 {
		return warnCheck(strings.Join(unknown, ". "))
	}

	return passCheck(strings.Join(features, " "))
}

// pathVarCheck checks PATH, through which phenix finds apps and tools. Go refuses
// to run a command found through a relative entry.
func pathVarCheck(value string) doctorCheck {
	dirs := filepath.SplitList(value)
	if len(dirs) == 0 {
		return warnCheck("empty, so phenix finds no apps or tools")
	}

	var relative []string

	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			relative = append(relative, strconv.Quote(dir))
		}
	}

	if len(relative) > 0 {
		return warnCheck("relative entries " + strings.Join(relative, ", ") + ", which phenix will not run commands from")
	}

	return passCheck(fmt.Sprintf("%d directories", len(dirs)))
}

// editorCheck checks the editor phenix opens to edit configs, which it runs
// as one program without arguments; vim when EDITOR is empty.
func editorCheck(env doctorEnv, value string) doctorCheck {
	name := value
	if name == "" {
		name = editor.DefaultEditor
	}

	path, err := env.lookPath(name)
	if err != nil {
		detail := fmt.Sprintf("%q is not a program on PATH, so phenix cannot open an editor", name)
		if strings.ContainsAny(name, " \t") {
			detail += ". phenix runs EDITOR as one program, without arguments"
		}

		if value == "" {
			return warnCheck("empty, and " + detail)
		}

		return failCheck(detail)
	}

	if value == "" {
		return passCheck("empty, so phenix uses " + path)
	}

	return passCheck(path)
}

// sudoUserCheck checks the user whose home phenix uses when root runs it
// through sudo.
func sudoUserCheck(env doctorEnv, value string) doctorCheck {
	if !env.root {
		return passCheck("unused, because phenix is not running as root")
	}

	if command, _ := env.lookup("SUDO_COMMAND"); !sudoCommandNamesPhenix(command) {
		return passCheck("unused, because SUDO_COMMAND does not name phenix")
	}

	u, err := env.lookupUser(value)
	if err != nil {
		return warnCheck(fmt.Sprintf("no user %q, so phenix uses root's config, store and logs", value))
	}

	return passCheck(fmt.Sprintf("phenix uses the config, store and logs of %s (%s)", u.Username, u.HomeDir))
}

// sudoCommandCheck checks the command sudo ran, never showing it, since its
// arguments may hold a secret.
func sudoCommandCheck(env doctorEnv, value string) doctorCheck {
	switch {
	case !env.root:
		return passCheck("unused, because phenix is not running as root")
	case !sudoCommandNamesPhenix(value):
		return passCheck("does not name phenix, so phenix ignores SUDO_USER")
	}

	return passCheck("names phenix, so phenix uses SUDO_USER's config, store and logs")
}
