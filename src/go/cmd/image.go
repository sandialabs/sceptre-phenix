package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/api/image"
	"phenix/types"
	v1 "phenix/types/version/v1"
	"phenix/util"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/printer"
)

const createFromArgs = 2

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandImage,
		Short: "Virtual disk image management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

// imageListRow is one image configuration of phenix image list -o json or -o
// yaml, with the values of the table and its optional columns.
type imageListRow struct {
	Name       string   `json:"name"       yaml:"name"`
	Size       string   `json:"size"       yaml:"size"`
	Variant    string   `json:"variant"    yaml:"variant"`
	Release    string   `json:"release"    yaml:"release"`
	Format     string   `json:"format"     yaml:"format"`
	Compressed bool     `json:"compressed" yaml:"compressed"`
	Mirror     string   `json:"mirror"     yaml:"mirror"`
	Overlays   []string `json:"overlays"   yaml:"overlays"`
	Packages   []string `json:"packages"   yaml:"packages"`
	Scripts    []string `json:"scripts"    yaml:"scripts"`
}

// imageListRows returns the rows of imgs. Scripts holds the names of the
// scripts, sorted, as the table shows them.
func imageListRows(imgs []types.Image) []imageListRow {
	rows := make([]imageListRow, 0, len(imgs))

	for _, img := range imgs {
		rows = append(rows, imageListRow{
			Name:       img.Metadata.Name,
			Size:       img.Spec.Size,
			Variant:    img.Spec.Variant,
			Release:    img.Spec.Release,
			Format:     string(img.Spec.Format),
			Compressed: img.Spec.Compress,
			Mirror:     img.Spec.Mirror,
			Overlays:   nonNil(img.Spec.Overlays),
			Packages:   nonNil(img.Spec.Packages),
			Scripts:    nonNil(slices.Sorted(maps.Keys(img.Spec.Scripts))),
		})
	}

	return rows
}

func newImageListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   commandList,
		Short: "Table of image configurations",
		Long: `Table of image configurations

  -f, -c and -m add columns to the table. The JSON and YAML output always
  have those values.`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			imgs, err := image.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to print a list of configurations")

				return err.Humanized()
			}

			var optional []string

			if mustGet(cmd.Flags().GetBool, "format") {
				optional = append(optional, "Format")
			}

			if mustGet(cmd.Flags().GetBool, "compressed") {
				optional = append(optional, "Compressed")
			}

			if mustGet(cmd.Flags().GetBool, "mirror") {
				optional = append(optional, "Mirror")
			}

			return out.print(cmd, imageListRows(imgs), outputViews{
				formatTable: func(w io.Writer) error {
					if len(imgs) == 0 {
						fmt.Fprintln(w, "\nThere are no image configurations available")
					} else {
						printer.PrintTableOfImageConfigs(w, optional, imgs...)
					}

					return nil
				},
			})
		},
	}

	cmd.Flags().BoolP("format", "f", false, "Include disk image format")
	cmd.Flags().BoolP("compressed", "c", false, "Include disk compression")
	cmd.Flags().BoolP("mirror", "m", false, "Include debootstrap mirror")
	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	return cmd
}

//nolint:funlen // command definition
func newImageCreateCmd() *cobra.Command {
	desc := `Create a disk image configuration

  Used to create a virtual disk image configuration from which to build
  an image.

  When specifying the --size option, the following units can be used:

  M - Megabytes
  G - Gigabytes`

	example := `
  phenix image create <image name>
  phenix image create --size 2G --variant mingui --release noble --compress --overlays foobar --packages foo --scripts bar <image name>`

	cmd := &cobra.Command{
		Use:     "create <image name>",
		Short:   "Create a disk image configuration",
		Long:    desc,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			var img v1.Image

			if len(args) == 0 {
				return errors.New("must provide an image name")
			} else if len(args) > 1 {
				// This might happen if, for example, multiple overlays are provided to
				// the overlays flag space-delimited instead of comma-delimited.
				return errors.New(
					"must provide an image name as the only argument (check that you are using commas where required for flags)",
				)
			}

			img.Name = args[0]
			img.Size = mustGet(cmd.Flags().GetString, "size")
			img.Variant = mustGet(cmd.Flags().GetString, "variant")
			img.Release = mustGet(cmd.Flags().GetString, "release")
			img.Mirror = mustGet(cmd.Flags().GetString, "mirror")
			img.Format = v1.Format(mustGet(cmd.Flags().GetString, "format"))
			img.Compress = mustGet(cmd.Flags().GetBool, "compress")
			img.Ramdisk = mustGet(cmd.Flags().GetBool, "ramdisk")
			img.NoVirtuals = mustGet(cmd.Flags().GetBool, "no-virtuals")
			img.SkipDefaultPackages = mustGet(cmd.Flags().GetBool, "skip-default-pkgs")

			if overlays := mustGet(cmd.Flags().GetString, "overlays"); overlays != "" {
				img.Overlays = commaList(overlays)
			}

			if packages := mustGet(cmd.Flags().GetString, "packages"); packages != "" {
				img.Packages = commaList(packages)
			}

			if components := mustGet(cmd.Flags().GetString, "components"); components != "" {
				img.Components = commaList(components)
			}

			if scripts := mustGet(cmd.Flags().GetString, "scripts"); scripts != "" {
				img.ScriptPaths = commaList(scripts)
			}

			if kernel := mustGet(cmd.Flags().GetString, "kernel-args"); kernel != "" {
				img.Kernel = commaList(kernel)
			}

			if err := image.ValidateSize(img.Size); err != nil {
				return fmt.Errorf("invalid --size %q: %w", img.Size, err)
			}

			err := image.Create(&img)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to create the "+img.Name+" image")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "image configuration created", "image", img.Name)

			return nil
		},
	}

	cmd.Flags().StringP("size", "s", "10G", "Image size to use")
	cmd.Flags().StringP("variant", "v", "minbase", "Image variant to use")
	cmd.Flags().StringP("release", "r", "noble", "OS release codename")
	cmd.Flags().
		StringP("mirror", "m", "http://us.archive.ubuntu.com/ubuntu", "Debootstrap mirror (must match release)")
	cmd.Flags().
		StringP("components", "l", "", "List of components from the mirror to download packages from (separated by comma)")
	cmd.Flags().StringP("format", "f", "qcow2", "Format of disk image")
	cmd.Flags().
		BoolP("compress", "c", false, "Compress image after creation (does not apply to raw image)")
	cmd.Flags().
		BoolP("ramdisk", "R", false, "Create a kernel/initrd pair in addition to a disk image")
	cmd.Flags().
		StringP("overlays", "O", "", "List of overlay names (include full path; separated by comma)")
	cmd.Flags().
		Bool("skip-default-pkgs", false, "Skip default packages typically included in all builds")
	cmd.Flags().
		StringP("packages", "P", "", "List of packages to include in addition to those provided by variant (separated by comma)")
	cmd.Flags().
		StringP("scripts", "T", "", "List of scripts to include in addition to the defaults (include full path; separated by comma)")
	cmd.Flags().
		Bool("no-virtuals", false, "Don't add virtual filesystem mounts to chroot before executing scripts when running vmdb2 (default is 'false')")
	cmd.Flags().
		StringP(
			"kernel-args",
			"k",
			"",
			"List of parameters which grub will pass to the Linux kernel (e.g. 'net.ifnames=0','consoleblank=0'); separated by comma)."+
				" Warning: each comma starts a new parameter, so a value with a comma, such as console=ttyS0,115200n8, is split, which can make the build fail.",
		)

	_ = cmd.RegisterFlagCompletionFunc("format", completeValues(
		string(v1.FormatQcow2), string(v1.FormatRaw), string(v1.FormatVmdk), string(v1.FormatVdi), string(v1.FormatVhdx),
	))

	return cmd
}

// commaList is the items of a comma-separated flag value, with no space at their ends and no empty items.
func commaList(value string) []string {
	return normalizeVMFilterValues([]string{value})
}

func newImageCreateFromCmd() *cobra.Command {
	desc := `Create image configuration from existing one

  Used to create a new virtual disk image configuration from an existing one;
  if options are used they will be added to the existing configuration.`

	cmd := &cobra.Command{
		Use:               "create-from <existing configuration> <new configuration>",
		Short:             "Create image configuration from existing one",
		Long:              desc,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < createFromArgs {
				return errors.New("the name of a existing and/or new configuration is required")
			}

			var (
				name     = args[0]
				saveas   = args[1]
				overlays []string
				packages []string
				scripts  []string
			)

			if opt := mustGet(cmd.Flags().GetString, "overlays"); opt != "" {
				overlays = commaList(opt)
			}

			if opt := mustGet(cmd.Flags().GetString, "packages"); opt != "" {
				packages = commaList(opt)
			}

			if opt := mustGet(cmd.Flags().GetString, "scripts"); opt != "" {
				scripts = commaList(opt)
			}

			err := image.CreateFromConfig(name, saveas, overlays, packages, scripts)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to create the configuration file "+saveas,
				)

				return err.Humanized()
			}

			plog.Info(
				plog.TypeSystem,
				"image configuration created from existing",
				"new",
				saveas,
				"existing",
				name,
			)

			return nil
		},
	}

	cmd.Flags().
		StringP("overlays", "O", "", "List of overlay names (include full path; separated by comma)")
	cmd.Flags().
		StringP("packages", "P", "", "List of packages to include in addition to those provided by variant (separated by comma)")
	cmd.Flags().
		StringP("scripts", "T", "", "List of scripts to include in addition to the defaults (include full path; separated by comma)")

	return cmd
}

func newImageEditCmd() *cobra.Command {
	desc := `Edit an image

  This subcommand is used to edit an image using your default editor.
	`

	cmd := &cobra.Command{
		Use:               "edit <image name>",
		Short:             "Edit an image",
		Long:              desc,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			img := "image/" + args[0]

			_, err := config.Edit(img, false)
			if err != nil {
				if config.IsConfigNotModified(err) {
					plog.Warn(plog.TypeSystem, "image configuration not updated", "image", args[0])

					return nil
				}

				err := util.HumanizeError(err, "Unable to edit the %s image", args[0])

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "image configuration updated", "image", args[0])

			return nil
		},
	}

	return cmd
}

func newImageBuildCmd() *cobra.Command {
	desc := `Build a virtual disk image

  Used to build a new virtual disk using an existing configuration; vmdb2 must
  be in path.`

	example := `
  phenix image build <configuration name>
  phenix image build --very-verbose --output </path/to/dir/>`

	cmd := &cobra.Command{
		Use:               "build <configuration name>",
		Short:             "Build a virtual disk image",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("the name of a configuration to build the disk image is required")
			}

			var (
				name      = args[0]
				cache     = mustGet(cmd.Flags().GetBool, "cache")
				dryrun    = mustGet(cmd.Flags().GetBool, "dry-run")
				output    string
				verbosity int
			)

			if mustGet(cmd.Flags().GetString, "output") == "" {
				cwd, err := os.Getwd()
				if err != nil {
					err := util.HumanizeError(err, "Unable to get the current working directory")

					return err.Humanized()
				}

				output = cwd
			} else {
				output = mustGet(cmd.Flags().GetString, "output")
			}

			if mustGet(cmd.Flags().GetBool, "verbose") {
				verbosity |= image.VVerbose
			}

			if mustGet(cmd.Flags().GetBool, "very-verbose") {
				verbosity |= image.VVVerbose
			}

			ctx := notes.Context(context.Background(), false)

			err := image.Build(ctx, name, verbosity, cache, dryrun, output)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to build the "+name+" image")

				return err.Humanized()
			}

			notes.PrettyPrint(ctx, false)

			plog.Info(plog.TypeSystem, "image built successfully", "image", name)

			return nil
		},
	}

	// panic: "vv" shorthand is more than one ASCII character
	cmd.Flags().BoolP("verbose", "v", false, "Enable verbose output")
	cmd.Flags().
		BoolP("very-verbose", "x", false, "Enable very verbose output, additionally writes output log file to <image name>.log")
	cmd.Flags().BoolP("cache", "c", false, "Cache rootfs as tar archive")
	cmd.Flags().BoolP("dry-run", "", false, "Do everything but actually call out to vmdb2")
	cmd.Flags().
		StringP("output", "o", "", "Specify the output directory for the disk image to be saved to")
	_ = cmd.MarkFlagDirname("output")

	return cmd
}

func newImageDeleteCmd() *cobra.Command {
	desc := `Delete one or more image configurations

  When phenix cannot delete an image configuration, it continues with the
  other ones. Then it exits with an error that names each failure.

  With --dry-run, phenix shows the image configurations that it would
  delete, and deletes nothing.`

	var dry dryRun

	cmd := &cobra.Command{
		Use:     "delete <configuration name>...",
		Aliases: []string{deleteAlias},
		Short:   "Delete one or more image configurations",
		Long:    desc,
		Example: "  phenix image delete ubuntu\n  phenix image delete ubuntu kali-mini",
		Args:    cobra.MinimumNArgs(1),
		// every argument is another image name
		ValidArgsFunction: completePositional(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if slices.Contains(args, "") {
				return errors.New("the name of the configuration to delete is required")
			}

			var failed []*util.HumanizedError

			for _, name := range uniqueNames(args) {
				if dry.enabled() {
					if _, err := config.Get("image/" + name); err != nil {
						failed = append(failed, util.HumanizeError(err, "%s", "Unable to delete the "+name+" image"))

						continue
					}
				}

				if dry.skip("delete", "image "+name) {
					continue
				}

				if err := config.Delete("image/" + name); err != nil {
					failed = append(failed, util.HumanizeError(err, "%s", "Unable to delete the "+name+" image"))

					continue
				}

				plog.Info(plog.TypeSystem, "image configuration deleted", "image", name)
			}

			return deleteFailures(failed, "images")
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

// uniqueNames returns names in order without repeats, so a name given twice
// is acted on once.
func uniqueNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))

	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}

	return out
}

func newImageAppendCmd() *cobra.Command {
	desc := `Append to an image configuration

  Used to add scripts, packages, and/or overlays to an existing virtual disk
  image configuration.`

	cmd := &cobra.Command{
		Use:               "append <configuration name>",
		Short:             "Append to an image configuration",
		Long:              desc,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("the name of a configuration to append to is required")
			}

			var (
				name     = args[0]
				overlays []string
				packages []string
				scripts  []string
			)

			if opt := mustGet(cmd.Flags().GetString, "overlays"); opt != "" {
				overlays = commaList(opt)
			}

			if opt := mustGet(cmd.Flags().GetString, "packages"); opt != "" {
				packages = commaList(opt)
			}

			if opt := mustGet(cmd.Flags().GetString, "scripts"); opt != "" {
				scripts = commaList(opt)
			}

			err := image.Append(name, overlays, packages, scripts)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to append to the "+name+" image")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "image configuration appended", "image", name)

			return nil
		},
	}

	cmd.Flags().
		StringP("overlays", "O", "", "List of overlay names (include full path; separated by comma)")
	cmd.Flags().
		StringP("packages", "P", "", "List of packages to include in addition to those provided by variant (separated by comma)")
	cmd.Flags().
		StringP("scripts", "T", "", "List of scripts to include in addition to the defaults (include full path; separated by comma)")

	return cmd
}

func newImageRemoveCmd() *cobra.Command {
	desc := `Remove from an image configuration

  Used to remove scripts, packages, and/or overlays to an existing virtual disk
  image configuration`

	cmd := &cobra.Command{
		Use:               "remove <configuration name>",
		Short:             "Remove from an image configuration",
		Long:              desc,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("the name of a configuration to remove from is required")
			}

			var (
				name     = args[0]
				overlays = commaList(mustGet(cmd.Flags().GetString, "overlays"))
				packages = commaList(mustGet(cmd.Flags().GetString, "packages"))
				scripts  = commaList(mustGet(cmd.Flags().GetString, "scripts"))
			)

			err := image.Remove(name, overlays, packages, scripts)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to remove from the "+name+" image")

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "image configuration items removed", "image", name)

			return nil
		},
	}

	cmd.Flags().
		StringP("overlays", "O", "", "List of overlay names (include full path; separated by comma)")
	cmd.Flags().
		StringP("packages", "P", "", "List of packages to include in addition to those provided by variant (separated by comma)")
	cmd.Flags().
		StringP("scripts", "T", "", "List of scripts to include in addition to the defaults (include full path; separated by comma)")

	return cmd
}

func newImageUpdateCmd() *cobra.Command {
	desc := `Reread the scripts of an image configuration

  Reads again each script of an existing virtual disk image configuration from
  its file on disk, and stores the new contents in the configuration. A script
  that is a URL, or whose file is not on disk, keeps its stored contents. The
  scripts keep their order.

  Note: this command does not add or remove scripts, and does not change
  packages or overlays. Use 'phenix image append' or 'phenix image remove' for
  those.`

	cmd := &cobra.Command{
		Use:               "update <configuration name>",
		Short:             "Reread the scripts of an image configuration from disk",
		Long:              desc,
		ValidArgsFunction: completeArgs(completeConfigNamesOf("Image")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("the name of a configuration to update is required")
			}

			name := args[0]

			err := image.Update(name)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to update scripts from the "+name+" image",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "image configuration scripts updated", "image", name)

			return nil
		},
	}

	return cmd
}

func newImageInjectMiniExeCmd() *cobra.Command {
	desc := `Inject a minimega executable into a disk image

  Used to add a minimega executable (miniccc, protonuke, or minirouter) and
  relevant boot scripts into an existing disk image. The disk operating system
  is guessed based on the provided agent's extension. When specifying the path
  to the disk to modify, the partition to inject into can optionally be included
  at the end of the path using a colon (for example, /phenix/images/foo.qc2:2).
  If not specified, partition 1 will be assumed.

  In a Windows disk image, the minimega executable will be injected into
  C:\minimega\[miniccc,protonuke]. A scheduler command will be placed into the
  Windows Startup directory for miniccc if '--init-system=startup' is provided,
  but not for protonuke, since protonuke's command line arguments are dynamic.
  Users or apps wishing to leverage protonuke on Windows hosts need to inject
  their own scheduler command in the Windows Startup directory or use miniccc to
  start protonuke.

  In a Linux disk image, the minimega executable will be injected into
  /usr/local/bin and the service file and symlinks will be injected into the
  appropriate locations, depending on which init system is being used. For
  systemd, they will be injected into /etc/systemd/system, and for sysinitv they
  will be injected into /etc/init.d and /etc/rc5.d. The protonuke service will
  only start if a file is present at /etc/default/protonuke, and that file
  should contain a single line setting the PROTONUKE_ARGS variable to a set of
  protonuke command line arguments. The minirouter service expects the miniccc
  service to be injected, and injecting minirouter will automatically cause
  miniccc to be injected as well.`

	cmd := &cobra.Command{
		Use:   "inject-miniexe <path to exe> <path to disk>",
		Short: "Inject a minimega executable into a disk image",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < createFromArgs {
				return errors.New("the path to the minimega executable and the disk is required")
			}

			var (
				exe  = args[0]
				disk = args[1]
				init = mustGet(cmd.Flags().GetString, "init-system")
			)

			err := image.InjectMiniExe(exe, disk, init)
			if err != nil {
				err := util.HumanizeError(
					err,
					"%s",
					"Unable to inject "+exe+" into the "+disk+" image",
				)

				return err.Humanized()
			}

			return nil
		},
	}

	cmd.Flags().
		String("init-system", "systemd", "Linux init system to generate boot scripts for (Linux: systemd, sysinitv; Windows: startup)")

	_ = cmd.RegisterFlagCompletionFunc("init-system", completeValues("systemd", "sysinitv", "startup"))

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	imageCmd := newImageCmd()

	imageCmd.AddCommand(newImageListCmd())
	imageCmd.AddCommand(newImageCreateCmd())
	imageCmd.AddCommand(newImageCreateFromCmd())
	imageCmd.AddCommand(newImageEditCmd())
	imageCmd.AddCommand(newImageBuildCmd())
	imageCmd.AddCommand(newImageDeleteCmd())
	imageCmd.AddCommand(newImageAppendCmd())
	imageCmd.AddCommand(newImageRemoveCmd())
	imageCmd.AddCommand(newImageUpdateCmd())
	imageCmd.AddCommand(newImageInjectMiniExeCmd())

	addCommandToRoot(imageCmd, true)
}
