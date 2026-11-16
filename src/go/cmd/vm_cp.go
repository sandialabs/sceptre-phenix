package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/vm"
)

// vmCopyArgs is the number of arguments of vm cp.
const vmCopyArgs = 3

// errVMCopyArgs is wrapped by the error of parseVMCopyArgs.
var errVMCopyArgs = errors.New("expected exactly one of <src> and <dst> in the form <vm>:<path>")

// vmCopy is a copy that vm cp makes: from the local path into the VM at its
// path when toVM is true, and from the VM to the local path when it is false.
type vmCopy struct {
	vm, guest, local string
	toVM             bool
}

// splitVMPath splits arg, in the form <vm>:<path>, into the VM and the path,
// as scp does: arg names a VM only when it has a ":" with a VM name before it
// that has no "/". It returns false for a local path.
func splitVMPath(arg string) (string, string, bool) {
	name, guest, ok := strings.Cut(arg, ":")
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}

	return name, guest, true
}

// parseVMCopyArgs returns the copy that src and dst of vm cp name. Exactly one
// of them must be in the form <vm>:<path>, with a path that is not empty.
func parseVMCopyArgs(src, dst string) (vmCopy, error) {
	srcVM, srcPath, srcInVM := splitVMPath(src)
	dstVM, dstPath, dstInVM := splitVMPath(dst)

	var c vmCopy

	switch {
	case srcInVM && dstInVM:
		return vmCopy{}, fmt.Errorf("%w: both %q and %q name a VM", errVMCopyArgs, src, dst)
	case !srcInVM && !dstInVM:
		return vmCopy{}, fmt.Errorf("%w: neither %q nor %q names a VM", errVMCopyArgs, src, dst)
	case dstInVM:
		c = vmCopy{vm: dstVM, guest: dstPath, local: src, toVM: true}
	default:
		c = vmCopy{vm: srcVM, guest: srcPath, local: dst, toVM: false}
	}

	if c.guest == "" || c.local == "" {
		return vmCopy{}, fmt.Errorf("%w: the paths must not be empty", errVMCopyArgs)
	}

	return c, nil
}

// completeVMCopyArgs completes the arguments of vm cp: the experiment, then
// for <src> and <dst> a <vm name>: prefix of a VM of the experiment, or a
// local file. A path in a VM gets no completion, and when one argument
// names a VM, the other is a local file.
func completeVMCopyArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return completeExperiments(expRunning, false)(cmd, args, toComplete)
	case len(args) >= vmCopyArgs:
		return nil, cobra.ShellCompDirectiveNoFileComp
	case strings.Contains(toComplete, ":"):
		if _, _, inVM := splitVMPath(toComplete); inVM {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}

	if len(args) == vmCopyArgs-1 {
		if _, _, inVM := splitVMPath(args[1]); inVM {
			return nil, cobra.ShellCompDirectiveDefault
		}
	}

	vms, _ := completeVMs(cmd, args[:1], "")

	var prefixes []string

	for _, name := range vms {
		if prefix := name + ":"; strings.HasPrefix(prefix, toComplete) {
			prefixes = append(prefixes, prefix)
		}
	}

	if len(prefixes) == 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	return prefixes, cobra.ShellCompDirectiveNoSpace
}

func newVMCopyCmd() *cobra.Command {
	desc := `Copy a file into or out of a running VM

  Copy one file between the local host and a running VM through the VM's
  miniccc agent, as scp does. Name the VM side as <vm name>:<path>, with an
  absolute path in the VM. Exactly one of <src> and <dst> must name the VM.
  A path that ends with "/" is a folder, and the file keeps its name.

  To copy into the VM, phenix puts a staging copy in the experiment's folder
  of the minimega files directory. It sends the copy with "cc send" and then
  moves it to the path in the VM. To copy out of the VM, phenix gets the file
  with "cc recv". This works only for a VM on the headnode, and for a Unix
  path with no glob characters. phenix removes the staging copy and the
  received copy when the copy is complete. A file copied out of the VM gets
  the permissions 0600. The VM must run the miniccc agent.`

	example := `
  phenix vm cp myexp ./setup.sh host-01:/root/setup.sh
  phenix vm cp myexp ./setup.sh host-01:/root/
  phenix vm cp myexp host-01:/var/log/syslog ./syslog`

	cmd := &cobra.Command{
		Use:               "cp <experiment name> <src> <dst>",
		Aliases:           []string{"copy"},
		Short:             "Copy a file into or out of a running VM",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: completeVMCopyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != vmCopyArgs {
				return fmt.Errorf("expected <experiment name> <src> <dst>, received %d arguments", len(args))
			}

			c, err := parseVMCopyArgs(args[1], args[2])
			if err != nil {
				return err
			}

			timeout := mustGet(cmd.Flags().GetDuration, "timeout")
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be more than 0, received %s", timeout)
			}

			if c.toVM {
				dst, err := vm.CopyToVM(commandContext(cmd), args[0], c.vm, c.local, c.guest, timeout)
				if err != nil {
					return fmt.Errorf("unable to copy %s to VM %s: %w", c.local, c.vm, err)
				}

				fmt.Fprintf(cmd.OutOrStdout(), "copied %s to %s:%s\n", c.local, c.vm, dst)

				return nil
			}

			dst, err := vm.CopyFromVM(commandContext(cmd), args[0], c.vm, c.guest, c.local, timeout)
			if err != nil {
				return fmt.Errorf("unable to copy %s from VM %s: %w", c.guest, c.vm, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "copied %s:%s to %s\n", c.vm, c.guest, dst)

			return nil
		},
	}

	cmd.Flags().Duration("timeout", vm.DefaultExecTimeout, "How long to wait for the VM to complete the copy")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("vm").AddCommand(newVMCopyCmd())
}
