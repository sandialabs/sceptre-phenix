package mm

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"phenix/util/mm/mmcli"
)

// ErrNoC2ExitCode is wrapped by the error of [GetC2ExitCode] when minimega
// gives no exit code for a command, such as a minimega without the
// `cc exitcode` command.
var ErrNoC2ExitCode = errors.New("minimega gave no exit code")

// c2ResponseDir is the folder, in the namespace's folder of the minimega
// files directory, where the cc server keeps the responses to cc commands.
const c2ResponseDir = "miniccc_responses"

// c2UUID matches a VM UUID as minimega reports it.
var c2UUID = regexp.MustCompile(`^[0-9a-fA-F-]+$`)

// C2RecvFile makes ExecC2Command post `cc recv` for the file, which the VM's
// miniccc client then sends to the cc server. The file must be a word that
// minimega takes as it is (see [CCExecCommand]).
func C2RecvFile(f string) C2Option {
	return func(o *c2Options) {
		o.recvFile = f
	}
}

// c2ExitCoder is the optional part of MM that reads exit codes. It is kept
// out of MM so the stand-ins tests use for MM need not implement it.
type c2ExitCoder interface {
	GetC2ExitCode(...C2Option) (int, error)
}

// GetC2ExitCode returns the exit code of the cc exec command with the ID that
// C2CommandID gives, in the VM that C2VM names. The error wraps
// [ErrNoC2ExitCode] when minimega gives none.
func GetC2ExitCode(opts ...C2Option) (int, error) {
	if m, ok := DefaultMM.(c2ExitCoder); ok {
		return m.GetC2ExitCode(opts...)
	}

	return 0, ErrNoC2ExitCode
}

// GetC2ExitCode returns the exit code that miniccc reported for a cc exec
// command, from `cc exitcode` on the VM's host.
func (Minimega) GetC2ExitCode(opts ...C2Option) (int, error) {
	o := NewC2Options(opts...)

	if o.vm == "" {
		return 0, errors.New("must provide VM when getting an exit code")
	}

	if _, err := strconv.Atoi(o.commandID); err != nil {
		return 0, fmt.Errorf("invalid C2 command ID %q", o.commandID)
	}

	target, err := c2CommandTarget(o)
	if err != nil {
		return 0, err
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = fmt.Sprintf("cc exitcode %s %s", o.commandID, target.uuid)

	resp, err := mmcli.HostResponse(mmcli.RunContext(o.ctx, cmd), target.host)
	if err != nil {
		return 0, fmt.Errorf("%w for command %s: %w", ErrNoC2ExitCode, o.commandID, err)
	}

	code, err := strconv.Atoi(strings.TrimSpace(resp))
	if err != nil {
		return 0, fmt.Errorf("%w for command %s: %q is not a number", ErrNoC2ExitCode, o.commandID, resp)
	}

	return code, nil
}

// C2ResponsePath returns the folder where the cc server of namespace ns keeps
// the response of the VM with uuid to command id, on the host of the VM. With
// an empty uuid, it returns the folder of all the responses to the command.
// The cc server writes a file that `cc recv` receives to the path of the file
// in the VM, relative to "/", below this folder.
func C2ResponsePath(ns, id, uuid string) (string, error) {
	if err := ValidateArg(ns); err != nil || ns == "." || ns == ".." || strings.Contains(ns, "/") {
		return "", fmt.Errorf("invalid namespace %q", ns)
	}

	if _, err := strconv.Atoi(id); err != nil {
		return "", fmt.Errorf("invalid C2 command ID %q", id)
	}

	if uuid != "" && !c2UUID.MatchString(uuid) {
		return "", fmt.Errorf("invalid VM UUID %q", uuid)
	}

	return GetMMFullPath(path.Join(ns, c2ResponseDir, id, uuid)), nil
}
