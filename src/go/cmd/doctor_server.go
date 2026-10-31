package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"golang.org/x/sys/unix"

	"phenix/util/common"
	"phenix/util/mm/mmcli"
	"phenix/version"
	"phenix/web/health"
	"phenix/web/middleware"
)

const (
	serverCheckName     = "server"
	serverBridgeRow     = "server bridge-mode"
	serverDeployRow     = "server deploy-mode"
	minimegaFilesRow    = "minimega files"
	minimegaBaseRow     = "minimega base"
	minimegaMeshRow     = "minimega mesh"
	maxServerOptionsLen = 1 << 20

	// Phrases in the details of rows, which their hints look for.
	stalePhrase       = "stale socket file"
	noAnswerPhrase    = "did not answer within"
	thisPhenixPhrase  = "this phenix is"
	notWritablePhrase = "is not writable by this user"
	relativePhrase    = "is relative"
)

// errNoAnswer marks a call that gave no answer before its time bound.
var errNoAnswer = errors.New("no answer")

// doctorFound is what earlier checks of one run found, for the checks that
// build on them.
type doctorFound struct {
	minimegaUp   bool
	minimegaArgs map[string]string // minimega's flags, once minimega-env read them
	images       *doctorImageScan  // the scan of the images folder, once disk-chains or disk-formats read it

	experiments *doctorExperiments // the stored experiments, once a check read them
	inventory   *doctorInventory   // minimega's namespaces, VMs, taps and captures, once a check read them
	ovsShow     *doctorOVSRun      // the output of ovs-vsctl show, once a check ran it
	leftovers   *doctorLeftovers   // what the leftovers check found that is safe to delete, once it ran
	mesh        *doctorMesh        // the nodes of minimega's mesh, once a check read them
}

// minimegaRunner sends minimega, whose base directory is base, one
// command, bounded by timeout, and returns every response or the first error.
type minimegaRunner func(base, command string, timeout time.Duration) ([]*minicli.Response, error)

// boundedCall runs call, and gives up with errNoAnswer once timeout passes or
// ctx ends. A call it gives up on runs on, and its result is dropped.
func boundedCall(
	ctx context.Context,
	timeout time.Duration,
	call func() ([]*minicli.Response, error),
) ([]*minicli.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type answer struct {
		value []*minicli.Response
		err   error
	}

	answers := make(chan answer, 1)

	go func() {
		value, err := call()
		answers <- answer{value: value, err: err}
	}()

	select {
	case a := <-answers:
		return a.value, a.err
	case <-ctx.Done():
		return nil, errNoAnswer
	}
}

// runMinimegaAt sends minimega, whose base directory is base, one command on
// a connection of its own, bounded by timeout. A namespaced command creates
// its namespace, so send one only for a namespace that minimega listed.
func runMinimegaAt(base, command string, timeout time.Duration) ([]*minicli.Response, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = command
	cmd.Timeout = timeout

	var (
		resps []*minicli.Response
		err   error
	)

	// every response is read, so the connection is released
	for resp := range mmcli.RunDedicatedAt(base, cmd) {
		for _, r := range resp.Resp {
			if r.Error != "" {
				if err == nil {
					err = errors.New(r.Error) //nolint:err113 // minimega's error text
				}

				continue
			}

			resps = append(resps, r)
		}
	}

	return resps, err
}

// doctorServerClient is the client of the root command, bounded by timeout.
// It connects to socket and never creates or removes it.
func doctorServerClient(socket string, timeout time.Duration) *http.Client {
	client := serverOptionsClient(socket)
	client.Timeout = timeout

	return client
}

// serverModes is the part of GET /api/v1/options that doctor compares.
type serverModes struct {
	BridgeMode string `json:"bridge-mode"`
	DeployMode string `json:"deploy-mode"`
}

// serverChecks asks the phenix ui on socket for its options through client, as
// every other phenix command does, and compares its version and modes with
// this command's bridge and deploy settings. lstat looks at a socket that fails.
func serverChecks(
	ctx context.Context,
	socket string,
	timeout time.Duration,
	client func(string, time.Duration) *http.Client,
	lstat func(string) (os.FileInfo, error),
	bridge, deploy string,
) []doctorCheck {
	if socket == "" {
		return []doctorCheck{passCheck("unix-socket is empty, so phenix commands ask no phenix ui for its modes")}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/api/v1/options", nil)
	if err != nil {
		return []doctorCheck{failCheck(err.Error())}
	}

	c := client(socket, timeout)
	defer c.CloseIdleConnections()

	resp, err := c.Do(req) //nolint:gosec // G704 the phenix server the user configured
	if err != nil {
		return []doctorCheck{serverDialCheck(socket, timeout, lstat, err)}
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxServerOptionsLen))

	switch {
	case err != nil:
		return []doctorCheck{warnCheck(fmt.Sprintf("reading the answer of phenix ui on %s: %v", socket, err))}
	case resp.StatusCode != http.StatusOK:
		return []doctorCheck{warnCheck(fmt.Sprintf(
			"phenix ui on %s answered GET /api/v1/options with %s", socket, resp.Status,
		))}
	}

	var modes serverModes
	if err := json.Unmarshal(body, &modes); err != nil {
		return []doctorCheck{warnCheck(fmt.Sprintf("phenix ui on %s answered with options that do not parse: %v", socket, err))}
	}

	rows := []doctorCheck{serverVersionCheck(socket, resp.Header.Get(middleware.HeaderVersion), version.API())}

	if row, ok := serverBridgeModeCheck(bridge, modes.BridgeMode); ok {
		rows = append(rows, row)
	}

	if row, ok := serverDeployModeCheck(deploy, modes.DeployMode); ok {
		rows = append(rows, row)
	}

	return rows
}

// serverDialCheck explains err, the failure of a request to the phenix ui on
// socket, which lstat describes. Only one this user may not send fails.
func serverDialCheck(socket string, timeout time.Duration, lstat func(string) (os.FileInfo, error), err error) doctorCheck {
	var (
		netErr net.Error
		urlErr *url.Error
	)

	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}

	// Linux refuses a connection to a file that is not a socket as to a stale one
	if info, lerr := lstat(socket); lerr == nil && info.Mode()&os.ModeSocket == 0 {
		return warnCheck(fmt.Sprintf("%s is not a socket, so no phenix ui listens on it", socket))
	}

	switch {
	case errors.Is(err, fs.ErrPermission):
		return failCheck(fmt.Sprintf("this user cannot connect to %s: %v", socket, err))
	case errors.Is(err, fs.ErrNotExist):
		return warnCheck(fmt.Sprintf("no phenix ui on %s: phenix ui is not running, or serves another unix socket", socket))
	case errors.Is(err, syscall.ECONNREFUSED):
		return warnCheck(fmt.Sprintf("%s is a %s: no phenix ui listens on it", socket, stalePhrase))
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return warnCheck(fmt.Sprintf("phenix ui on %s %s %s", socket, noAnswerPhrase, timeout))
	}

	return warnCheck(fmt.Sprintf("asking phenix ui on %s: %v", socket, err))
}

// serverVersionCheck compares the version the phenix ui on socket gives with
// this build's.
func serverVersionCheck(socket, server, local string) doctorCheck {
	switch server {
	case local:
		return passCheck(fmt.Sprintf("phenix ui %s answers on %s", server, socket))
	case "":
		return warnCheck(fmt.Sprintf("phenix ui on %s does not give its version, and %s %s", socket, thisPhenixPhrase, local))
	}

	return warnCheck(fmt.Sprintf("phenix ui on %s is %s, and %s %s", socket, server, thisPhenixPhrase, local))
}

// serverBridgeModeCheck compares bridge-mode, empty when not set, with the
// server's. A phenix ui in auto makes every command use auto; reports false
// for a local mode every command refuses.
func serverBridgeModeCheck(local, server string) (doctorCheck, bool) {
	mode := common.BridgeModeManual

	if local != "" {
		parsed, err := common.ParseBridgeMode(local)
		if err != nil {
			return doctorCheck{}, false
		}

		mode = parsed
	}

	auto := string(common.BridgeModeAuto)
	check := passCheck(fmt.Sprintf("phenix commands use %s, as phenix ui does", mode))

	switch {
	case server == auto && local == "":
		check = passCheck("phenix commands use auto, from phenix ui")
	case server == auto && mode != common.BridgeModeAuto:
		check = warnCheck(fmt.Sprintf("phenix commands use auto, from phenix ui, in place of the %s set here", mode))
	case server != auto && mode == common.BridgeModeAuto:
		check = warnCheck("phenix commands use auto, set here, and phenix ui uses " + server)
	}

	check.Name = serverBridgeRow

	return check, true
}

// serverDeployModeCheck compares deploy-mode with the server's. Unset, every
// command uses the server's; reports false for a local mode every command
// refuses.
func serverDeployModeCheck(local, server string) (doctorCheck, bool) {
	var check doctorCheck

	if local == "" {
		check = passCheck(fmt.Sprintf("phenix commands use %s, from phenix ui", server))
	} else {
		mode, err := common.ParseDeployMode(local)

		switch {
		case err != nil:
			return doctorCheck{}, false
		case string(mode) == server:
			check = passCheck(fmt.Sprintf("phenix commands use %s, as phenix ui does", mode))
		default:
			check = warnCheck(fmt.Sprintf("phenix commands use %s, set here, in place of phenix ui's %s", mode, server))
		}
	}

	check.Name = serverDeployRow

	return check, true
}

// serverHint is what to do about a server row that warns or fails.
func serverHint(c doctorCheck) string {
	switch {
	case c.Name == serverBridgeRow:
		return "set bridge-mode to the mode of phenix ui"
	case c.Name == serverDeployRow:
		return "unset deploy-mode to use the mode of phenix ui, or set it to that mode"
	case c.Status == doctorFail:
		return socketPermissionHint
	case strings.Contains(c.Detail, stalePhrase):
		return "start phenix ui, which replaces the socket file, or delete the file"
	case strings.Contains(c.Detail, noAnswerPhrase):
		return "check that phenix ui answers requests, or increase --timeout"
	case strings.Contains(c.Detail, thisPhenixPhrase):
		return "run the same phenix version as phenix ui"
	}

	return "start phenix ui, or set unix-socket to the socket it serves"
}

// minimegaDialProblem explains err, minimega's failure to answer, by the
// error text mmcli passes on.
func minimegaDialProblem(err error) string {
	text := err.Error()

	switch {
	case strings.Contains(text, syscall.ENOENT.Error()):
		return "no socket there: minimega is not running, or base-dir.minimega names another directory"
	case strings.Contains(text, syscall.ECONNREFUSED.Error()):
		return "a " + stalePhrase + ": minimega stopped without removing it"
	case strings.Contains(text, syscall.EACCES.Error()):
		return "this user may not connect to it"
	}

	return ""
}

// minimegaHint is what to do when minimega gives no version.
func minimegaHint(c doctorCheck) string {
	switch {
	case strings.Contains(c.Detail, stalePhrase):
		return "restart minimega, which replaces the socket file"
	case strings.Contains(c.Detail, "may not connect"):
		return "run phenix doctor as the user that runs phenix ui and minimega, usually root"
	case strings.Contains(c.Detail, " within "):
		return "check that minimega answers requests (minimega -e version), or increase --timeout"
	}

	return "start minimega, or set base-dir.minimega to the directory that holds its socket"
}

// minimegaEnvChecks reads minimega's flags with its global args command and
// checks the files directory, base directory and mesh they name.
func minimegaEnvChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	if !in.found.minimegaUp {
		return []doctorCheck{{Name: "", Status: doctorSkip, Detail: "not checked, because minimega gave no version"}}
	}

	base := in.text("base-dir.minimega")

	run := func(command string) ([]*minicli.Response, error) {
		return boundedCall(ctx, in.timeout, func() ([]*minicli.Response, error) {
			return in.minimega(base, command, in.timeout)
		})
	}

	resps, err := run("args")

	switch {
	case errors.Is(err, errNoAnswer):
		return []doctorCheck{warnCheck(fmt.Sprintf("minimega did not give its flags (args) within %s", in.timeout))}
	case err != nil:
		return []doctorCheck{warnCheck("minimega did not give its flags (args): " + err.Error())}
	}

	args, ok := minimegaArgs(resps)
	if !ok {
		return []doctorCheck{warnCheck("minimega's answer to args holds no flags")}
	}

	in.found.minimegaArgs = args

	files := in.env.minimegaFilesCheck(args["filepath"])
	files.Name = minimegaFilesRow

	return []doctorCheck{files, minimegaBaseCheck(base, args["base"]), minimegaMeshCheck(args, run)}
}

// minimegaArgs is the flags minimega's args command gives, by name.
func minimegaArgs(resps []*minicli.Response) (map[string]string, bool) {
	for _, resp := range resps {
		if len(resp.Tabular) == 0 || len(resp.Header) == 0 {
			continue
		}

		args := make(map[string]string, len(resp.Header))

		for i, name := range resp.Header {
			if i < len(resp.Tabular[0]) {
				args[name] = resp.Tabular[0][i]
			}
		}

		return args, true
	}

	return nil, false
}

// minimegaFilesCheck checks minimega's files directory, which phenix resolves
// relative image paths against and keeps disk images in.
func (e doctorEnv) minimegaFilesCheck(dir string) doctorCheck {
	switch {
	case dir == "":
		return warnCheck("minimega gives no -filepath, so phenix uses <base-dir.phenix>/images in its place")
	case !filepath.IsAbs(dir):
		return warnCheck(fmt.Sprintf("minimega's -filepath %s %s, so phenix uses <base-dir.phenix>/images in its place",
			dir, relativePhrase))
	}

	info, err := e.stat(dir)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return failCheck(fmt.Sprintf(
			"minimega's -filepath %s does not exist here, so phenix cannot find the images minimega launches", dir,
		))
	case err != nil:
		return failCheck(err.Error())
	case !info.IsDir():
		return failCheck(fmt.Sprintf("minimega's -filepath %s is not a directory", dir))
	case e.access(dir, unix.R_OK|unix.X_OK) != nil:
		return failCheck(fmt.Sprintf(
			"minimega's -filepath %s is not readable and searchable by this user, so phenix cannot read its images", dir,
		))
	case e.access(dir, unix.W_OK) != nil:
		return warnCheck(fmt.Sprintf(
			"minimega's -filepath %s %s, so phenix cannot upload, clone, rename or delete disk images in it",
			dir, notWritablePhrase,
		))
	}

	return passCheck(dir + " (minimega's -filepath)")
}

// minimegaBaseCheck compares minimega's -base with base-dir.minimega, under
// which phenix also measures the disk usage the Hosts page shows.
func minimegaBaseCheck(setting, base string) doctorCheck {
	check := passCheck(base)

	switch {
	case base == "":
		check = warnCheck("minimega gives no -base")
	case filepath.Clean(base) != filepath.Clean(setting):
		check = warnCheck(fmt.Sprintf(
			"minimega runs with -base %s and base-dir.minimega is %s. The Hosts page measures disk usage under %s",
			base, setting, setting,
		))
	}

	check.Name = minimegaBaseRow

	return check
}

// minimegaMeshCheck reports minimega's degree and abssnapshot flags and, when
// minimega meshes with other nodes, the mesh's size from its global mesh status.
func minimegaMeshCheck(args map[string]string, run func(string) ([]*minicli.Response, error)) doctorCheck {
	detail := fmt.Sprintf("degree %s, abssnapshot %s", args["degree"], args["abssnapshot"])
	check := passCheck(detail)

	if degree, err := strconv.Atoi(args["degree"]); err == nil && degree > 0 {
		resps, err := run("mesh status")

		if size, ok := health.MeshSize(resps); err == nil && ok {
			check.Detail = fmt.Sprintf("%s, mesh of %d nodes", detail, size)
		} else if err != nil {
			check = warnCheck(detail + ", but mesh status failed: " + err.Error())
		} else {
			check = warnCheck(detail + ", but mesh status gives no size")
		}
	}

	check.Name = minimegaMeshRow

	return check
}

// minimegaEnvHint is what to do about a minimega-env row that warns or fails.
func minimegaEnvHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == minimegaBaseRow:
		return "set base-dir.minimega to minimega's -base"
	case c.Name == minimegaMeshRow:
		return "check minimega's mesh with minimega -e mesh status"
	case c.Name == minimegaFilesRow && strings.Contains(c.Detail, notWritablePhrase):
		return "give this user write access to minimega's files directory"
	case c.Name == minimegaFilesRow && (strings.Contains(c.Detail, relativePhrase) || strings.Contains(c.Detail, "no -filepath")):
		return "start minimega with an absolute -filepath"
	case c.Name == minimegaFilesRow:
		return "mount minimega's files directory at the same path for phenix (Docker Compose shares /phenix), " +
			"readable by this user"
	}

	return "run minimega 3.0 or later, whose args command gives its flags"
}
