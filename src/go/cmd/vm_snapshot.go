package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/disk"
	"phenix/api/vm"
	"phenix/util/plog"
)

const (
	snapshotListArgs = 2
	snapshotNameArgs = 3
	commitArgs       = 2
)

// vmDiskError is the error of the VM disk command that could not do op to the VM vmName for err. It keeps err, and
// adds what to do for a VM state that the command cannot change.
func vmDiskError(op, vmName string, err error) error {
	switch {
	case errors.Is(err, vm.ErrSnapshotDisabled):
		return fmt.Errorf("unable to %s the %s VM: %w (stop the experiment, then run phenix vm set <experiment name> %s --snapshot)",
			op, vmName, err, vmName)
	case errors.Is(err, vm.ErrMultipleDisks):
		return fmt.Errorf("unable to %s the %s VM: %w (see sandialabs/sceptre-phenix#145)", op, vmName, err)
	default:
		return fmt.Errorf("unable to %s the %s VM: %w", op, vmName, err)
	}
}

func newVMSnapshotCmd() *cobra.Command {
	desc := `List, create, restore, and delete the snapshots of a VM

  A snapshot holds the disk and the memory state of a VM in a running
  experiment. Restore a snapshot to return the VM to that state. For an ELF
  memory dump, use phenix vm memory-snapshot.

  To create or restore a snapshot, the VM must have its snapshot flag set and
  exactly one disk. phenix refuses a VM with more than one disk, because a
  snapshot holds only the first disk (sandialabs/sceptre-phenix#145).

  phenix keeps the snapshot files on the cluster node that runs the VM, in
  <base-dir.phenix>/images/<experiment name>/files. The list shows the
  snapshots on all nodes. A restore of a VM on a node other than the head
  node can fail if minimega on that node runs with the -headnode flag:
  minimega then gets the snapshot files from the head node, which does not
  have them (sandialabs/sceptre-phenix#153).`

	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "List, create, restore, and delete VM disk and memory snapshots",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVMSnapshotListCmd(), newVMSnapshotCreateCmd(), newVMSnapshotRestoreCmd(), newVMSnapshotDeleteCmd())

	return cmd
}

func newVMSnapshotListCmd() *cobra.Command {
	var out *outputFlag

	desc := `List the snapshots of a VM

  Prints the name of each snapshot of the VM, one name on each line, in sort
  order. Give one of these names to phenix vm snapshot restore. The
  experiment does not have to run. -o json and -o yaml print the names as a
  list.`

	cmd := &cobra.Command{
		Use:               "list <experiment name> <vm name>",
		Short:             "List the snapshots of a VM",
		Long:              desc,
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != snapshotListArgs {
				return errors.New("must provide an experiment name and VM name")
			}

			expName, vmName := args[0], args[1]

			if _, err := vm.CheckTarget(expName, vmName, false); err != nil {
				return vmDiskError("list the snapshots of", vmName, err)
			}

			snapshots, err := vm.Snapshots(context.Background(), expName, vmName)
			if err != nil {
				return vmDiskError("list the snapshots of", vmName, err)
			}

			slices.Sort(snapshots)

			return out.printAPI(cmd, snapshots, outputViews{
				formatTable: func(w io.Writer) error {
					for _, name := range snapshots {
						fmt.Fprintln(w, name)
					}

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newVMSnapshotCreateCmd() *cobra.Command {
	desc := `Create a snapshot of a running VM

  Saves the disk and the memory state of the VM as a snapshot. The snapshot
  name must be a bare file name, with no folder. phenix removes an extension
  from the name, and names the snapshot <vm name>__<snapshot name>. The
  command prints that name.

  minimega pauses the VM while it saves the snapshot, and then resumes the VM.
  The command waits up to one hour for the save. The experiment and the VM
  must run.`

	return &cobra.Command{
		Use:               "create <experiment name> <vm name> <snapshot name>",
		Short:             "Create a snapshot of a running VM",
		Long:              desc,
		Example:           "  phenix vm snapshot create my-exp web before-update",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != snapshotNameArgs {
				return errors.New("must provide an experiment name, VM name, and snapshot name")
			}

			expName, vmName, name := args[0], args[1], args[2]

			base := strings.TrimSuffix(name, filepath.Ext(name))

			// the name without its extension names the snapshot, so it must not be empty
			for _, n := range []string{name, base} {
				if err := disk.ValidateFileName(n); err != nil {
					return fmt.Errorf("invalid snapshot name: %w", err)
				}
			}

			if _, err := vm.CheckTarget(expName, vmName, true); err != nil {
				return vmDiskError("create a snapshot of", vmName, err)
			}

			if err := vm.Snapshot(context.Background(), expName, vmName, name, nil); err != nil {
				return vmDiskError("create a snapshot of", vmName, err)
			}

			snapshot := vmName + "__" + base

			fmt.Fprintf(cmd.OutOrStdout(), "Created snapshot %s of VM %s in experiment %s\n", snapshot, vmName, expName)
			plog.Info(plog.TypeSystem, "vm snapshot created", "vm", vmName, "exp", expName, "snapshot", snapshot)

			return nil
		},
	}
}

func newVMSnapshotRestoreCmd() *cobra.Command {
	desc := `Restore a VM to one of its snapshots

  Relaunches the VM from the disk and the memory state of the snapshot. The
  VM keeps its UUID. The VM loses all changes made after the snapshot. Give
  the snapshot name as phenix vm snapshot list prints it. The experiment must
  run.`

	return &cobra.Command{
		Use:               "restore <experiment name> <vm name> <snapshot name>",
		Short:             "Restore a VM to one of its snapshots",
		Long:              desc,
		Example:           "  phenix vm snapshot restore my-exp web web__before-update",
		ValidArgsFunction: vmSnapshotCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != snapshotNameArgs {
				return errors.New("must provide an experiment name, VM name, and snapshot name")
			}

			expName, vmName, snapshot := args[0], args[1], args[2]

			if _, err := vm.CheckTarget(expName, vmName, true); err != nil {
				return vmDiskError("restore", vmName, err)
			}

			if err := vm.Restore(context.Background(), expName, vmName, snapshot); err != nil {
				return vmDiskError("restore", vmName, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Restored VM %s in experiment %s to snapshot %s\n", vmName, expName, snapshot)
			plog.Info(plog.TypeSystem, "vm restored", "vm", vmName, "exp", expName, "snapshot", snapshot)

			return nil
		},
	}
}

func newVMSnapshotDeleteCmd() *cobra.Command {
	var dry dryRun

	desc := `Delete one of the snapshots of a VM

  Deletes the memory state and the disk of the snapshot from the experiment's
  files directory on every cluster node. Give the snapshot name as phenix vm
  snapshot list prints it. The experiment does not have to run. phenix
  refuses a snapshot whose disk a running VM uses, as it does after a
  restore.`

	cmd := &cobra.Command{
		Use:               "delete <experiment name> <vm name> <snapshot name>",
		Short:             "Delete one of the snapshots of a VM",
		Long:              desc,
		Example:           "  phenix vm snapshot delete my-exp web web__before-update",
		Args:              argsWithUsage(cobra.ExactArgs(snapshotNameArgs)),
		ValidArgsFunction: vmSnapshotCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			expName, vmName, snapshot := args[0], args[1], args[2]

			if _, err := vm.CheckTarget(expName, vmName, false); err != nil {
				return vmDiskError("delete a snapshot of", vmName, err)
			}

			if dry.enabled() {
				name, err := vm.CheckDeleteSnapshot(commandContext(cmd), expName, vmName, snapshot)
				if err != nil {
					return vmDiskError("delete a snapshot of", vmName, err)
				}

				dry.skip("delete", "snapshot "+name+" of VM "+vmName+" in experiment "+expName)

				return nil
			}

			if _, err := vm.DeleteSnapshot(commandContext(cmd), expName, vmName, snapshot); err != nil {
				return vmDiskError("delete a snapshot of", vmName, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted snapshot %s of VM %s in experiment %s\n", snapshot, vmName, expName)
			plog.Info(plog.TypeSystem, "vm snapshot deleted", "vm", vmName, "exp", expName, "snapshot", snapshot)

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func newVMCommitCmd() *cobra.Command {
	desc := `Create a backing image from the disk of a running VM

  Writes the first disk of the VM as it is now, with the changes in its
  snapshot, to a new image that has no backing image. phenix shuts down the
  VM while it writes the image, and then starts the VM again. It also starts
  the VM again if the operation fails.

  The image name must be a bare file name, with no folder. Without an image
  name, phenix names the image after the base image of the VM and the time,
  for example ubuntu_20261106120000.qc2. phenix writes the image in
  <base-dir.phenix>/images on the head node, copies it to the other cluster
  nodes, and prints its name. The VM can run on any cluster node.

  The experiment and the VM must run, and the VM must have its snapshot flag
  set. For a VM with more than one disk, the image holds only the first disk.
  The other disks do not change.`

	return &cobra.Command{
		Use:               "commit <experiment name> <vm name> [image name]",
		Short:             "Create a backing image from the disk of a running VM",
		Long:              desc,
		Example:           "  phenix vm commit my-exp web web-configured.qc2",
		ValidArgsFunction: vmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < commitArgs || len(args) > commitArgs+1 {
				return errors.New("must provide an experiment name and VM name, optionally followed by an image name")
			}

			expName, vmName := args[0], args[1]

			var name string

			if len(args) > commitArgs {
				name = args[2]

				if err := disk.ValidateFileName(name); err != nil {
					return fmt.Errorf("invalid image name: %w", err)
				}
			}

			exp, err := vm.CheckTarget(expName, vmName, true)
			if err != nil {
				return vmDiskError("commit the disk of", vmName, err)
			}

			if name == "" {
				generated, err := vm.GetNewDiskName(expName, vmName)
				if err != nil {
					return vmDiskError("commit the disk of", vmName, err)
				}

				// the generated name keeps the folder of the base image; the new image goes in the images directory
				name = filepath.Base(generated)
			}

			if drives := exp.Spec.Topology().FindNodeByName(vmName).Hardware().Drives(); len(drives) > 1 {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: VM %s has %d disks; the new image holds only the first disk\n",
					vmName, len(drives))
			}

			image, err := vm.CommitToDisk(context.Background(), expName, vmName, name, nil)
			if err != nil {
				return vmDiskError("commit the disk of", vmName, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created image %s from the first disk of VM %s in experiment %s\n",
				image, vmName, expName)
			plog.Info(plog.TypeSystem, "vm committed", "vm", vmName, "exp", expName, "image", image)

			return nil
		},
	}
}
