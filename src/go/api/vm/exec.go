package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
)

// DefaultExecTimeout is how long [Exec], [CopyToVM] and [CopyFromVM] wait for
// the VM's response when the caller gives no timeout.
const DefaultExecTimeout = time.Minute

// c2CheckTimeout is how long to wait for the VM's miniccc agent to show as
// active before a command is refused.
const c2CheckTimeout = 5 * time.Second

var (
	// ErrC2NotActive is wrapped by the error of [Exec], [CopyToVM] and
	// [CopyFromVM] for a VM whose miniccc agent is not active.
	ErrC2NotActive = errors.New("the VM's C2 agent (miniccc) is not active")

	// ErrExecCommand is wrapped by the error of [ExecCommandText] for a
	// command that cannot reach the VM unchanged.
	ErrExecCommand = errors.New("invalid command for the VM")
)

// ExecResult is the response of a VM to a command that [Exec] ran in it.
// ExitCode is the command's exit code when ExitCodeKnown is true: an older
// minimega does not report it.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
	ExitCodeKnown  bool
}

// c2VM is a running VM with an active miniccc agent.
type c2VM struct {
	exp, name, uuid, host, osType string
}

// ExecCommandText returns the text of `cc exec` that makes miniccc run argv,
// the program and its arguments, without a shell. The error wraps
// [ErrExecCommand] for an empty argv, for a program that minimega reads as a
// stdin=, stdout= or stderr= redirect, and for a word that minimega would
// change (see [mm.CCExecCommand]).
func ExecCommandText(argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", fmt.Errorf("%w: no command given", ErrExecCommand)
	}

	if key, _, ok := strings.Cut(argv[0], "="); ok && (key == "stdin" || key == "stdout" || key == "stderr") {
		return "", fmt.Errorf("%w: minimega reads %q as a %s redirect", ErrExecCommand, argv[0], key)
	}

	text, err := mm.CCExecCommand(argv...)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrExecCommand, err)
	}

	return text, nil
}

// Exec runs argv in the running VM through its miniccc agent, as
// `cc exec` in the experiment's namespace, filtered to the VM by its UUID. It
// waits up to timeout (DefaultExecTimeout when zero) for the response and
// returns the command's stdout, stderr and exit code.
func Exec(ctx context.Context, expName, vmName string, argv []string, timeout time.Duration) (*ExecResult, error) {
	text, err := ExecCommandText(argv)
	if err != nil {
		return nil, err
	}

	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}

	target, err := activeC2VM(ctx, expName, vmName)
	if err != nil {
		return nil, err
	}

	id, err := mm.ExecC2Command(target.c2Options(ctx, mm.C2Command(text), mm.C2Wait(), mm.C2Timeout(timeout))...)
	if err != nil {
		return nil, fmt.Errorf("running the command in VM %s: %w", vmName, err)
	}

	plog.Info(plog.TypeAction, "vm command run", "exp", expName, "vm", vmName, "id", id)

	return target.result(ctx, id)
}

// result returns the response of the VM to command id.
func (t c2VM) result(ctx context.Context, id string) (*ExecResult, error) {
	stdout, err := mm.GetC2Response(
		mm.C2NS(t.exp), mm.C2VM(t.name), mm.C2CommandID(id), mm.C2ResponseTypeStdout(), mm.C2Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("getting the stdout of command %s: %w", id, err)
	}

	stderr, err := mm.GetC2Response(
		mm.C2NS(t.exp), mm.C2VM(t.name), mm.C2CommandID(id), mm.C2ResponseTypeStderr(), mm.C2Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("getting the stderr of command %s: %w", id, err)
	}

	result := &ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: 0, ExitCodeKnown: false}

	code, err := mm.GetC2ExitCode(mm.C2NS(t.exp), mm.C2VM(t.name), mm.C2CommandID(id), mm.C2Context(ctx))
	if err != nil {
		plog.Debug(plog.TypeSystem, "no exit code for C2 command", "exp", t.exp, "vm", t.name, "id", id, "err", err)

		return result, nil
	}

	result.ExitCode, result.ExitCodeKnown = code, true

	return result, nil
}

// c2Options returns the options of a C2 command for the VM, followed by opts.
func (t c2VM) c2Options(ctx context.Context, opts ...mm.C2Option) []mm.C2Option {
	return append([]mm.C2Option{mm.C2NS(t.exp), mm.C2VM(t.name), mm.C2IDClientsByUUID(), mm.C2Context(ctx)}, opts...)
}

// activeC2VM returns the VM of the running experiment, when the VM runs and
// its miniccc agent is active.
func activeC2VM(ctx context.Context, expName, vmName string) (c2VM, error) {
	if expName == "" {
		return c2VM{}, errors.New("no experiment name provided")
	}

	if vmName == "" {
		return c2VM{}, errors.New("no VM name provided")
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return c2VM{}, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	if !exp.Running() {
		return c2VM{}, fmt.Errorf("%w: %s", ErrExperimentNotRunning, expName)
	}

	node := exp.Spec.Topology().FindNodeByName(vmName)
	if node == nil {
		return c2VM{}, &mm.VMNotFoundError{Err: fmt.Errorf("vm %s not found in experiment %s", vmName, expName)}
	}

	info := mm.GetVMInfo(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err := ctx.Err(); err != nil {
		return c2VM{}, fmt.Errorf("getting details of VM %s: %w", vmName, err)
	}

	var details *mm.VM

	for i := range info {
		if info[i].Name == vmName {
			details = &info[i]
		}
	}

	if details == nil || !details.Running {
		return c2VM{}, fmt.Errorf("%w: %s", ErrVMNotRunning, vmName)
	}

	if err := mm.IsC2ClientActive(
		mm.C2NS(expName), mm.C2VM(vmName), mm.C2IDClientsByUUID(), mm.C2Timeout(c2CheckTimeout), mm.C2Context(ctx),
	); err != nil {
		return c2VM{}, fmt.Errorf("%w: %w", ErrC2NotActive, err)
	}

	var osType string
	if node.Hardware() != nil {
		osType = strings.ToLower(node.Hardware().OSType())
	}

	return c2VM{exp: expName, name: vmName, uuid: details.UUID, host: details.Host, osType: osType}, nil
}
