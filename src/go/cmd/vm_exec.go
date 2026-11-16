package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/vm"
)

// exitCodeMax is the largest exit code a process can give.
const exitCodeMax = 255

// errVMExecArgs is wrapped by the error of parseVMExecArgs.
var errVMExecArgs = errors.New("expected <experiment> <vm> [--] <command> [args...]")

// parseVMExecArgs splits the arguments of vm exec into the experiment, the VM
// and the command with its arguments. dash is the number of arguments before
// "--", as cobra's ArgsLenAtDash gives it, or -1 when there is no "--". With a
// "--", exactly the experiment and the VM come before it.
func parseVMExecArgs(args []string, dash int) (string, string, []string, error) {
	switch {
	case dash >= 0 && dash != 2:
		return "", "", nil, fmt.Errorf("%w: give only the experiment and the VM before --", errVMExecArgs)
	case len(args) < 3: //nolint:mnd // experiment, VM and command
		return "", "", nil, fmt.Errorf("%w: received %d arguments", errVMExecArgs, len(args))
	case args[0] == "" || args[1] == "":
		return "", "", nil, fmt.Errorf("%w: the experiment and the VM must not be empty", errVMExecArgs)
	}

	return args[0], args[1], args[2:], nil
}

func newVMExecCmd() *cobra.Command {
	desc := `Run a command in a running VM

  Run a command in a running VM through its miniccc agent, as the minimega
  command "cc exec" does, and print the stdout and stderr of the command.
  The command exits with the exit code of the command in the VM when minimega
  reports it.

  miniccc runs the command without a shell. To use pipes or redirects, run a
  shell, such as "sh -c '...'". Put "--" before a command that has flags.
  phenix refuses an argument that minimega would change on the host: an
  argument with a "$" variable, or a file:, http:, https: or tar: URL. The VM
  must run the miniccc agent.`

	example := `
  phenix vm exec myexp host-01 hostname
  phenix vm exec myexp host-01 --timeout 5m -- ls -la /tmp
  phenix vm exec myexp host-01 -- sh -c 'ps aux | grep nginx'`

	cmd := &cobra.Command{
		Use:               "exec <experiment name> <vm name> [--] <command> [args...]",
		Short:             "Run a command in a running VM",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: completeArgs(completeExperiments(expRunning, false), completeVMs),
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmName, argv, err := parseVMExecArgs(args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}

			timeout := mustGet(cmd.Flags().GetDuration, "timeout")
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be more than 0, received %s", timeout)
			}

			result, err := vm.Exec(commandContext(cmd), expName, vmName, argv, timeout)
			if err != nil {
				return fmt.Errorf("unable to run the command in VM %s: %w", vmName, err)
			}

			writeVMOutput(cmd.OutOrStdout(), result.Stdout)
			writeVMOutput(cmd.ErrOrStderr(), result.Stderr)

			if result.ExitCodeKnown && result.ExitCode != 0 {
				code := result.ExitCode
				if code < 0 || code > exitCodeMax {
					code = 1
				}

				return &exitCodeError{
					err:  fmt.Errorf("the command in VM %s exited with code %d", vmName, result.ExitCode),
					code: code,
				}
			}

			return nil
		},
	}

	cmd.Flags().Duration("timeout", vm.DefaultExecTimeout, "How long to wait for the command to complete")

	return cmd
}

// writeVMOutput writes s to w, with a newline at the end when s has none.
func writeVMOutput(w io.Writer, s string) {
	if s == "" {
		return
	}

	fmt.Fprint(w, s)

	if !strings.HasSuffix(s, "\n") {
		fmt.Fprintln(w)
	}
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMExecCmd())
}
