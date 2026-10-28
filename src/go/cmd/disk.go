package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/disk"
	"phenix/web/util"
)

const (
	diskPairArgs = 2

	// diskKindNames are the disk kinds disk list --type takes, as GET /disks
	// takes them in diskType.
	diskKindNames = "vm, container, iso, unknown"
)

// errUnknownDiskKind is the error of a disk kind that disk list --type does not know.
var errUnknownDiskKind = errors.New("unknown disk kind")

// parseDiskKinds returns the disk kinds that a comma-separated list of kind
// names selects. An empty list selects every kind. Names are not case
// sensitive, and spaces around a name are ignored.
func parseDiskKinds(list string) (disk.Kind, error) {
	all := disk.VMImage | disk.ContainerImage | disk.ISOImage | disk.UNKNOWN

	if strings.TrimSpace(list) == "" {
		return all, nil
	}

	var kinds disk.Kind

	for name := range strings.SplitSeq(list, ",") {
		name = strings.ToLower(strings.TrimSpace(name))

		switch name {
		case "vm", "container", "iso", "unknown":
			kinds |= disk.StringToKind(name)
		default:
			return 0, fmt.Errorf("%w %q: use one or more of %s, separated by commas", errUnknownDiskKind, name, diskKindNames)
		}
	}

	return kinds, nil
}

// diskInUseError is the error of a delete that err refused, with what to do.
func diskInUseError(err *disk.InUseError) error {
	return fmt.Errorf("%w; to delete it anyway, run the command again with --force", err)
}

func newDiskCmd() *cobra.Command {
	desc := `Disk image file management

  Lists, inspects and changes the disk images in the minimega files
  directory, as the Disks page of the web UI does. A disk is a path relative
  to the minimega files directory, or an absolute path inside it. The
  commands run in the CLI process, and do not need phenix ui to run.`

	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Disk image file management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newDiskListCmd(),
		newDiskDetailsCmd(),
		newDiskChecksumCmd(),
		newDiskSnapshotCmd(),
		newDiskCommitCmd(),
		newDiskRebaseCmd(),
		newDiskResizeCmd(),
		newDiskCloneCmd(),
		newDiskRenameCmd(),
		newDiskDeleteCmd(),
	)

	return cmd
}

func newDiskListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   commandList,
		Short: "Display a table of disk images",
		Long: `Display a table of disk images

  Lists the images in the minimega files directory, and the images that the
  experiments' topologies use. With --experiment, the topology images are
  only those of that experiment. -o json and -o yaml print each image with
  the fields of GET /disks.`,
		Example: `
  # Every VM and ISO image
  phenix disk list --type vm,iso

  # The images of one experiment, inspected again
  phenix disk list --experiment exp1 --refresh`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			kinds, err := parseDiskKinds(mustGet(cmd.Flags().GetString, "type"))
			if err != nil {
				return err
			}

			if mustGet(cmd.Flags().GetBool, "refresh") {
				disk.ClearCache()
			}

			expName := mustGet(cmd.Flags().GetString, "experiment")

			disks, err := disk.GetImages(expName)
			if err != nil {
				return fmt.Errorf("listing disk images: %w", err)
			}

			listed := make([]disk.Details, 0, len(disks))

			for _, d := range disks {
				if d.Kind&kinds != 0 {
					listed = append(listed, d)
				}
			}

			slices.SortFunc(listed, func(a, b disk.Details) int {
				return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.FullPath, b.FullPath))
			})

			return out.printAPI(cmd, listed, outputViews{
				formatTable: func(w io.Writer) error {
					if len(listed) == 0 {
						fmt.Fprintln(w, "No disk images")

						return nil
					}

					printDiskTable(w, listed)

					return nil
				},
			})
		},
	}

	cmd.Flags().StringP("experiment", "e", "", "only the topology images of this experiment")
	cmd.Flags().String("type", "", "only images of these kinds, separated by commas ("+diskKindNames+")")
	cmd.Flags().Bool("refresh", false, "inspect every image again, not from the cache")

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	_ = cmd.RegisterFlagCompletionFunc("experiment", completeExperiments(expAny, false))
	_ = cmd.RegisterFlagCompletionFunc("type", completeValues(strings.Split(diskKindNames, ", ")...))

	return cmd
}

// printDiskTable writes the disk images as a table.
func printDiskTable(w io.Writer, disks []disk.Details) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"Name", "Kind", "Path", "Format", "Size", "Virtual Size", "Backing Image", "Experiments", "Notes"})

	for _, d := range disks {
		path := d.RelativePath
		if path == "" {
			path = d.FullPath
		}

		backing := ""
		if len(d.BackingImages) > 0 {
			backing = d.BackingImages[0]
		}

		exps := make([]string, 0, len(d.Experiments))
		for _, use := range d.Experiments {
			exps = append(exps, use.Name)
		}

		table.Append([]string{
			d.Name, d.Kind.String(), path, d.Format, d.Size, d.VirtualSize, backing, strings.Join(exps, ", "), diskNotes(d),
		})
	}

	table.Render()
}

// diskNotes says what needs care about a disk image, in a few words.
func diskNotes(d disk.Details) string {
	var notes []string

	if d.ReadOnly {
		notes = append(notes, "read-only")
	}

	if d.BrokenChain {
		notes = append(notes, "broken chain: "+d.ChainProblem)
	}

	if d.FormatIssue != nil {
		notes = append(notes, string(d.FormatIssue.Level)+": "+d.FormatIssue.Problem)
	}

	return strings.Join(notes, "; ")
}

func newDiskDetailsCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "details <disk>",
		Short: "Show what phenix reads from a disk image",
		Long: `Show what phenix reads from a disk image

  Shows the format, sizes, header, partitions and file systems of the image,
  the images backed by it, and the VMs whose drives use it, as the details
  card of the Disks page does. The image must be in the disk list. -o json
  and -o yaml print the body of GET /disks/details.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := disk.Resolve(args[0])
			if err != nil {
				return fmt.Errorf("getting details of disk image %s: %w", args[0], err)
			}

			disks, err := disk.GetImages("")
			if err != nil {
				return fmt.Errorf("listing disk images: %w", err)
			}

			i := slices.IndexFunc(disks, func(d disk.Details) bool { return d.FullPath == path })
			if i < 0 {
				return fmt.Errorf("disk image %s is not in the disk list", path)
			}

			found := disk.Inspect(commandContext(cmd), disks[i], disks)

			uses, err := disk.VMUses(path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "phenix cannot tell which VMs use the disk image: %v\n", err)
			}

			found.VMs = uses

			return out.printAPI(cmd, found, outputViews{
				formatTable: func(w io.Writer) error {
					printDiskDetails(w, found)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

// printDiskDetails writes an inspection as a table of fields, then a table of
// partitions, overlays and VMs for each of them that it has.
func printDiskDetails(w io.Writer, found disk.Inspection) {
	format := found.Format
	if found.FormatVersion > 0 {
		format += " (version " + strconv.Itoa(found.FormatVersion) + ")"
	}

	fields := [][2]string{
		{"Path", found.FullPath},
		{"Format", format},
		{"File Size", diskBytes(found.FileBytes)},
		{"Allocated", diskBytes(found.AllocatedBytes)},
		{"Virtual Size", diskBytes(found.VirtualBytes)},
		{"Modified", found.Modified},
	}

	if found.FormatIssue != nil {
		fields = append(fields, [2]string{"Format Issue", string(found.FormatIssue.Level) + ": " + found.FormatIssue.Problem})
	}

	if found.Qcow != nil {
		fields = append(fields,
			[2]string{"Backing File", found.Qcow.BackingFile},
			[2]string{"Backing Format", found.Qcow.BackingFormat},
			[2]string{"Snapshots", strconv.FormatUint(uint64(found.Qcow.Snapshots), 10)},
		)
	}

	if found.FileUnavailable != "" {
		fields = append(fields, [2]string{"File Unavailable", found.FileUnavailable})
	}

	fields = append(fields, [2]string{"Partition Table", found.PartitionTable})

	if found.PartitionsUnavailable != "" {
		fields = append(fields, [2]string{"Partitions Unavailable", found.PartitionsUnavailable})
	}

	if found.Filesystem != nil {
		fields = append(fields, [2]string{"File System", diskFilesystem(found.Filesystem)})
	}

	for _, note := range found.PartitionNotes {
		fields = append(fields, [2]string{"Partition Note", note})
	}

	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"Field", "Value"})

	for _, f := range fields {
		if f[1] != "" {
			table.Append(f[:])
		}
	}

	table.Render()

	if len(found.Partitions) > 0 {
		table := tablewriter.NewWriter(w)
		table.SetHeader([]string{"Partition", "Type", "Start", "Size", "Bootable", "File System"})

		for _, p := range found.Partitions {
			table.Append([]string{
				strconv.Itoa(p.Number), p.Type, strconv.FormatInt(p.Start, 10), diskBytes(p.Size),
				strconv.FormatBool(p.Bootable), diskFilesystem(p.Filesystem),
			})
		}

		table.Render()
	}

	if len(found.Overlays) > 0 {
		table := tablewriter.NewWriter(w)
		table.SetHeader([]string{"Overlay", "Direct"})

		for _, o := range found.Overlays {
			table.Append([]string{o.FullPath, strconv.FormatBool(o.Direct)})
		}

		table.Render()
	}

	if len(found.VMs) > 0 {
		table := tablewriter.NewWriter(w)
		table.SetHeader([]string{"Experiment", "VM", "Drive", "Running"})

		for _, use := range found.VMs {
			table.Append([]string{use.Experiment, use.VM, strconv.Itoa(use.Drive), strconv.FormatBool(use.Running)})
		}

		table.Render()
	}
}

// diskBytes is n bytes as doctorBytes writes them, or "" for 0, which an
// inspection gives for a size it does not know.
func diskBytes(n int64) string {
	if n == 0 {
		return ""
	}

	return doctorBytes(n)
}

// diskFilesystem names a file system with its version and label, or is "".
func diskFilesystem(fs *disk.Filesystem) string {
	if fs == nil {
		return ""
	}

	parts := []string{fs.Type}

	if fs.Version != "" {
		parts = append(parts, fs.Version)
	}

	if fs.Label != "" {
		parts = append(parts, strconv.Quote(fs.Label))
	}

	return strings.Join(parts, " ")
}

func newDiskChecksumCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "checksum <disk>",
		Short: "Print the SHA-256 checksum of a disk image",
		Long: `Print the SHA-256 checksum of a disk image

  Reads the whole image, which can take a long time for a large image. The
  output has the format of sha256sum: the checksum, two spaces and the path.
  -o json and -o yaml print the body of GET /disks/checksum.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := disk.Resolve(args[0])
			if err != nil {
				return fmt.Errorf("getting checksum of disk image %s: %w", args[0], err)
			}

			sum, err := util.FileChecksum(path, path)
			if err != nil {
				return fmt.Errorf("getting checksum of disk image %s: %w", path, err)
			}

			return out.printAPI(cmd, sum, outputViews{
				formatTable: func(w io.Writer) error {
					fmt.Fprintf(w, "%s  %s\n", sum.SHA256, sum.Path)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

// diskCopy is a command that newDiskCopyCmd makes: act makes the new image
// dst from src. A command with check also takes --dry-run, which runs check
// in place of act and writes "would <action> <src> to <dst>".
type diskCopy struct {
	use, short, long, verb string
	sameFormat             bool
	act                    func(src, dst string) error
	check                  func(src, dst string) (string, string, error)
	action                 string
}

// newDiskCopyCmd returns a command that resolves a disk and the name of a new
// image, as sameFormat says (see disk.NewImagePath), then runs act on them.
func newDiskCopyCmd(c diskCopy) *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:               c.use + " <disk> <new>",
		Short:             c.short,
		Long:              c.long,
		Args:              argsWithUsage(cobra.ExactArgs(diskPairArgs)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := disk.Resolve(args[0])
			if err != nil {
				return fmt.Errorf("%s disk image %s: %w", c.verb, args[0], err)
			}

			dst, err := disk.NewImagePath(src, args[1], c.sameFormat)
			if err != nil {
				return fmt.Errorf("%s disk image %s: %w", c.verb, src, err)
			}

			if c.check != nil && dry.enabled() {
				if src, dst, err = c.check(src, dst); err != nil {
					return fmt.Errorf("%s disk image %s to %s: %w", c.verb, src, dst, err)
				}

				dry.skip(c.action, "disk image "+src+" to "+dst)

				return nil
			}

			if err := c.act(src, dst); err != nil {
				return fmt.Errorf("%s disk image %s to %s: %w", c.verb, src, dst, err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), dst)

			return nil
		},
	}

	if c.check != nil {
		dry = addDryRunFlag(cmd)
	}

	return cmd
}

func newDiskSnapshotCmd() *cobra.Command {
	return newDiskCopyCmd(diskCopy{
		use: "snapshot", short: "Create a qcow2 snapshot backed by a disk image",
		long: `Create a qcow2 snapshot backed by a disk image

  <new> is relative to the folder of <disk>, or absolute, and must be inside
  the minimega files directory. The snapshot gets the .qcow2 extension unless
  <new> ends in .qcow2 or .qc2. The command prints the path of the snapshot.`,
		verb: "snapshotting", sameFormat: false, act: disk.SnapshotDisk, check: nil, action: "",
	})
}

func newDiskCloneCmd() *cobra.Command {
	return newDiskCopyCmd(diskCopy{
		use: "clone", short: "Copy a disk image to a new file",
		long: `Copy a disk image to a new file

  <new> is as for phenix disk snapshot, but the copy keeps the format of
  <disk>, so it gets the extension of <disk>. A name with the extension of
  another image format is refused. The copy never replaces a file. The
  command prints the path of the copy.`,
		verb: "cloning", sameFormat: true, act: disk.CloneDisk, check: nil, action: "",
	})
}

func newDiskRenameCmd() *cobra.Command {
	return newDiskCopyCmd(diskCopy{
		use: "rename", short: "Rename a disk image",
		long: `Rename a disk image

  <new> is as for phenix disk clone. The rename never replaces a file. Images
  backed by <disk> and experiments that use it do not change, so check
  phenix disk details first. The command prints the new path.`,
		verb: "renaming", sameFormat: true, act: disk.RenameDisk, check: disk.CheckRenameDisk, action: "rename",
	})
}

func newDiskCommitCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "commit <disk>",
		Short: "Write a disk image into its backing image",
		Long: `Write a disk image into its backing image

  Both images must be inside the minimega files directory. All images backed
  by the backing image then see the changes.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dry.enabled() {
				path, backing, err := disk.CheckCommitDisk(args[0])
				if err != nil {
					return fmt.Errorf("committing disk image %s: %w", args[0], err)
				}

				dry.skip("commit", "disk image "+path+" into its backing image "+backing)

				return nil
			}

			if err := disk.CommitDisk(args[0]); err != nil {
				return fmt.Errorf("committing disk image %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Committed %s into its backing image\n", args[0])

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func newDiskRebaseCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "rebase <disk> [<backing>]",
		Short: "Change the backing image of a disk image",
		Long: `Change the backing image of a disk image

  Without --unsafe, qemu writes into <disk> the differences between the old
  and the new backing chains, so both chains must stay in the minimega files
  directory. With --unsafe, only the name of the backing image changes: use
  it only when the new backing image holds the same data. Give --independent
  in place of <backing> to copy all data into <disk> and remove its backing
  image.`,
		Example: `
  # Rebase onto another base image
  phenix disk rebase win10-snap.qcow2 win10-base.qcow2

  # Make the image independent of its backing image
  phenix disk rebase win10-snap.qcow2 --independent`,
		Args:              argsWithUsage(cobra.RangeArgs(1, diskPairArgs)),
		ValidArgsFunction: completeArgs(completeDisks, completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				independent = mustGet(cmd.Flags().GetBool, "independent")
				unsafe      = mustGet(cmd.Flags().GetBool, "unsafe")
				backing     string
			)

			switch {
			case len(args) == diskPairArgs && independent:
				return errors.New("give a backing image or --independent, not both")
			case len(args) == diskPairArgs:
				backing = args[1]
			case !independent:
				return errors.New("give a backing image, or --independent to remove the backing image")
			}

			if dry.enabled() {
				src, dst, err := disk.CheckRebaseDisk(args[0], backing, unsafe)
				if err != nil {
					return fmt.Errorf("rebasing disk image %s: %w", args[0], err)
				}

				if dst == "" {
					dry.skip("remove", "the backing image of disk image "+src)
				} else {
					dry.skip("rebase", "disk image "+src+" onto "+dst)
				}

				return nil
			}

			if err := disk.RebaseDisk(args[0], backing, unsafe); err != nil {
				return fmt.Errorf("rebasing disk image %s: %w", args[0], err)
			}

			if backing == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Disk image %s has no backing image now\n", args[0])
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Rebased %s onto %s\n", args[0], backing)
			}

			return nil
		},
	}

	cmd.Flags().Bool("unsafe", false, "change only the backing image name, and copy no data")
	cmd.Flags().Bool("independent", false, "remove the backing image, and copy its data into the image")

	dry = addDryRunFlag(cmd)

	return cmd
}

func newDiskResizeCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "resize <disk> <size>",
		Short: "Change the virtual size of a disk image",
		Long: `Change the virtual size of a disk image

  <size> is an integer followed by one of K, M, G, T, P, E: the new size, or
  with + or - before it, the change in size. A smaller size removes the data
  at the end of the disk. The image must keep all of its data in itself.`,
		Example: `
  # Add 10 GiB
  phenix disk resize win10.qcow2 +10G`,
		Args:              argsWithUsage(cobra.ExactArgs(diskPairArgs)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := disk.ValidateSize(args[1]); err != nil {
				return err //nolint:wrapcheck // says what is wrong with the argument
			}

			if dry.enabled() {
				src, err := disk.CheckResizeDisk(args[0])
				if err != nil {
					return fmt.Errorf("resizing disk image %s: %w", args[0], err)
				}

				dry.skip("resize", "disk image "+src+" by "+args[1])

				return nil
			}

			if err := disk.ResizeDisk(args[0], args[1]); err != nil {
				return fmt.Errorf("resizing disk image %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Resized %s by %s\n", args[0], args[1])

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func newDiskDeleteCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "delete <disk>",
		Short: "Delete a disk image",
		Long: `Delete a disk image

  Refuses an image that other images use as their backing image, or that
  experiments use, and names them. Give --force to delete it anyway: those
  images and experiments then cannot start.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeDisks),
		RunE: func(cmd *cobra.Command, args []string) error {
			force := mustGet(cmd.Flags().GetBool, "force")

			var (
				path string
				err  error
			)

			if dry.enabled() {
				path, err = disk.CheckDeleteDisk(args[0], force)
			} else {
				err = disk.DeleteDisk(args[0], force)
			}

			var inUse *disk.InUseError
			if errors.As(err, &inUse) {
				return diskInUseError(inUse)
			}

			if err != nil {
				return fmt.Errorf("deleting disk image %s: %w", args[0], err)
			}

			if dry.skip("delete", "disk image "+path) {
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", filepath.Clean(args[0]))

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "delete the image even when other images or experiments use it")

	dry = addDryRunFlag(cmd)

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newDiskCmd(), true)
}
