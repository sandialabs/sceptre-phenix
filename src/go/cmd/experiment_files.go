package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/util/file"
	"phenix/util/sigterm"
	"phenix/web/util"
)

const experimentFileArgs = 2

// experimentFileArgsCompletion completes <experiment name> <file>..., with
// each file once.
func experimentFileArgsCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completePositional(completeExperimentFiles, completeExperiments(expAny, false))(cmd, args, toComplete)
}

// requireExperiment returns an error unless the store has the experiment.
func requireExperiment(name string) error {
	if _, err := experiment.Get(name); err != nil {
		return fmt.Errorf("unable to get experiment %s: %w", name, err)
	}

	return nil
}

func newExperimentFilesCmd() *cobra.Command {
	desc := `Experiment file management

  Lists, gets, archives and deletes the files of an experiment, as the Files
  tab of the experiment page does: captures, VM snapshots and the files that
  apps and VMs write. A file is its path relative to the experiment's files
  directory, as phenix experiment files list shows it. A file that only a
  mesh node has is first copied to the head node.`

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Experiment file management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newExperimentFilesListCmd(),
		newExperimentFilesGetCmd(),
		newExperimentFilesChecksumCmd(),
		newExperimentFilesDeleteCmd(),
		newExperimentFilesArchiveCmd(),
	)

	return cmd
}

func newExperimentFilesListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list <experiment name>",
		Short: "Display a table of the files of an experiment",
		Long: `Display a table of the files of an experiment

  --filter takes the search syntax of the Files tab: words match the file
  name, and "and", "or", "not" and parentheses join them. Comparisons such as
  "size > 10mb" and "date >= 2026-01-01" and the categories packet, elf and
  vm also select files.`,
		Example: `
  # The packet captures larger than 1 MB
  phenix experiment files list exp1 --filter "packet and size > 1mb"`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, filter := args[0], mustGet(cmd.Flags().GetString, "filter")

			if strings.TrimSpace(filter) != "" && file.BuildTree(filter) == nil {
				return fmt.Errorf("the filter %q is not valid: check its operators and parentheses", filter)
			}

			if err := requireExperiment(name); err != nil {
				return err
			}

			files, _, err := experiment.FilesObserved(commandContext(cmd), name, filter)
			if err != nil {
				return fmt.Errorf("listing the files of experiment %s: %w", name, err)
			}

			files.SortBy("path", true)

			return out.printAPI(cmd, files, outputViews{
				formatTable: func(w io.Writer) error {
					if len(files) == 0 {
						fmt.Fprintln(w, "No files")

						return nil
					}

					table := tablewriter.NewWriter(w)
					table.SetAutoWrapText(false)
					table.SetHeader([]string{"Path", "Size", "Date", "Categories"})

					for _, f := range files {
						table.Append([]string{f.Path, strconv.FormatInt(f.Size, 10), f.Date, strings.Join(f.Categories, ", ")})
					}

					table.Render()

					return nil
				},
			})
		},
	}

	cmd.Flags().String("filter", "", "only the files that this search filter selects")
	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	return cmd
}

func newExperimentFilesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <experiment name> <file>",
		Short: "Write an experiment file to stdout or to a local file",
		Long: `Write an experiment file to stdout or to a local file

  Refuses a packet capture that minimega still writes: stop the capture
  first. With --output-file, phenix writes the file next to the given path
  first, and replaces the given path only when the copy is complete.
  Without --output-file, phenix does not write a binary file, such as a
  packet capture, to a terminal.`,
		Example: `
  # Copy a capture from a sub folder to the current folder
  phenix experiment files get exp1 captures/web_eth0.pcap -O web_eth0.pcap`,
		Args:              argsWithUsage(cobra.ExactArgs(experimentFileArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expAny, false), completeExperimentFiles),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]

			local, err := experiment.LocalFile(name, path)
			if err != nil {
				return fmt.Errorf("getting file %s of experiment %s: %w", path, name, err)
			}

			f, err := os.Open(local)
			if err != nil {
				return fmt.Errorf("opening file %s of experiment %s: %w", path, name, err)
			}

			defer func() { _ = f.Close() }()

			head := make([]byte, sniffLen)

			n, err := io.ReadFull(f, head)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("reading file %s of experiment %s: %w", path, name, err)
			}

			head = head[:n]

			write := func(w io.Writer) error {
				if _, err := io.Copy(w, io.MultiReader(bytes.NewReader(head), f)); err != nil {
					return fmt.Errorf("copying file %s of experiment %s: %w", path, name, err)
				}

				return nil
			}

			output := mustGet(cmd.Flags().GetString, outputFileFlag)
			if isTextData(head) {
				return writeOutput(cmd, output, write)
			}

			return writeBinaryOutput(cmd, output, "the binary file "+path, write)
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "write the file to this path instead of stdout")
	_ = cmd.MarkFlagFilename(outputFileFlag)

	return cmd
}

func newExperimentFilesChecksumCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "checksum <experiment name> <file>",
		Short: "Print the SHA-256 checksum of an experiment file",
		Long: `Print the SHA-256 checksum of an experiment file

  The text output has the format of sha256sum: the checksum, two spaces and
  the file's path relative to the experiment's files directory. The JSON and
  YAML output also give the size and the time of the last change.`,
		Args:              argsWithUsage(cobra.ExactArgs(experimentFileArgs)),
		ValidArgsFunction: completeArgs(completeExperiments(expAny, false), completeExperimentFiles),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, path := args[0], args[1]

			local, err := experiment.LocalFile(name, path)
			if err != nil {
				return fmt.Errorf("getting file %s of experiment %s: %w", path, name, err)
			}

			sum, err := util.FileChecksum(local, path)
			if err != nil {
				return fmt.Errorf("getting checksum of file %s of experiment %s: %w", path, name, err)
			}

			return out.printAPI(cmd, sum, outputViews{
				formatText: func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "%s  %s\n", sum.SHA256, sum.Path)

					return err //nolint:wrapcheck // the writer's own error
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})

	return cmd
}

func newExperimentFilesDeleteCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "delete <experiment name> <file>...",
		Short: "Delete experiment files from every node",
		Long: `Delete experiment files from every node

  Deletes each file from the head node and from every mesh node that has it.
  Refuses a file that is not in the list of the experiment's files, and a
  packet capture that minimega still writes. The command deletes the files it
  can, names each file it does not delete with the reason, and then fails.

  With --dry-run, phenix checks each file as for a delete, shows the files
  that it would delete, and deletes nothing.`,
		Args:              argsWithUsage(cobra.MinimumNArgs(experimentFileArgs)),
		ValidArgsFunction: experimentFileArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, paths := args[0], args[1:]

			var opts []experiment.DeleteFilesOption
			if dry.enabled() {
				opts = append(opts, experiment.DeleteFilesDryRun())
			}

			deleted, refused, err := experiment.DeleteFiles(name, paths, opts...)
			if err != nil {
				return fmt.Errorf("deleting files of experiment %s: %w", name, err)
			}

			for _, path := range deleted {
				if !dry.skip("delete", "file "+path+" of experiment "+name) {
					fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", path)
				}
			}

			var failed []string

			// in the order given, once each
			for _, path := range paths {
				if err, ok := refused[path]; ok {
					failed = append(failed, fmt.Sprintf("%s: %v", path, err))
					delete(refused, path)
				}
			}

			if len(failed) > 0 {
				return fmt.Errorf("phenix did not delete %d of the files of experiment %s:\n  %s",
					len(failed), name, strings.Join(failed, "\n  "))
			}

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func newExperimentFilesArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive <experiment name> <file>... --output-file <zip file>",
		Short: "Write experiment files to a local zip archive",
		Long: `Write experiment files to a local zip archive

  Archives the files as the Files tab's archive does, with the same limits:
  at most 500 files and 2 GiB in total. A file that phenix cannot add is
  left out, and the MISSING.txt entry of the archive names it with the
  reason; the command then fails after it writes the archive.`,
		Example: `
  phenix experiment files archive exp1 web_eth0.pcap db_eth0.pcap -O captures.zip`,
		Args:              argsWithUsage(cobra.MinimumNArgs(experimentFileArgs)),
		ValidArgsFunction: experimentFileArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, paths := args[0], args[1:]

			out := mustGet(cmd.Flags().GetString, outputFileFlag)
			if isStdoutPath(out) {
				return errors.New("give the path of the zip file with --output-file")
			}

			plan, err := experiment.PlanZip(name, paths, experiment.DefaultZipLimits())
			if err != nil {
				return fmt.Errorf("archiving files of experiment %s: %w", name, err)
			}

			var (
				ctx    = sigterm.CancelContext(commandContext(cmd))
				failed []string
			)

			err = writeBinaryOutput(cmd, out, "a zip archive", func(w io.Writer) error {
				archived, err := plan.Write(ctx, w, experiment.ZipHooks{
					OnStart: nil,
					OnFile: func(path string, err error) {
						if err != nil {
							failed = append(failed, fmt.Sprintf("%s: %v", path, err))
						}
					},
					OnBytes:    nil,
					SkipFailed: true,
				})

				if err == nil && len(archived) == 0 {
					err = errors.New("phenix could not add any of the files")
				}

				return err
			})
			if err != nil {
				return fmt.Errorf("archiving files of experiment %s: %w", name, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", out)

			if len(failed) > 0 {
				return fmt.Errorf("the archive %s is not complete (see its MISSING.txt):\n  %s", out, strings.Join(failed, "\n  "))
			}

			return nil
		},
	}

	cmd.Flags().StringP(outputFileFlag, "O", "", "write the zip archive to this path (required)")
	_ = cmd.MarkFlagFilename(outputFileFlag, "zip")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("experiment").AddCommand(newExperimentFilesCmd())
}
