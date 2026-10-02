package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	bapi "phenix/api/builder"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/plog"
)

func newBuilderCmd() *cobra.Command {
	desc := `Builder document management

  This subcommand works with Builder documents: the diagram files the
  Builder saves as Builder JSON or Builder YAML.`

	cmd := &cobra.Command{
		Use:   "builder",
		Short: "Builder document management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newBuilderPublishCmd() *cobra.Command {
	desc := `Create or update a topology from a Builder document

  Reads a Builder document (JSON or YAML), checks it as the Builder's
  Publish does, and stores the Topology configuration it describes. The
  document is stored with the topology, so the topology opens as a diagram
  in the Builder.

  The topology is named after the document unless --name is given. An
  existing topology is only replaced with --update, and only when nothing
  else has changed it since it was published. Publishing the same document
  again changes nothing. Scenarios and experiments are not created.

  The topology names its document in a builder-doc annotation. While the
  Builder is turned off in the web UI, the Configs page does not open such a
  topology for editing as text; "phenix config edit" still does.`

	example := `  phenix builder publish pump-station.json
  phenix builder publish pump-station.yaml --name pump-station
  phenix builder publish pump-station.json --update
  phenix builder publish pump-station.json --dry-run
  phenix builder publish /phenix/topologies/pump-station/pump-station.yaml --record-path`

	cmd := &cobra.Command{
		Use:     "publish </path/to/document>",
		Short:   "Create or update a topology from a Builder document",
		Long:    desc,
		Example: example,
		Args:    argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return publishBuilderDocument(cmd, args[0])
		},
	}

	cmd.Flags().Bool("dry-run", false, "Check the document and report what would be written, without writing anything")
	cmd.Flags().StringP("name", "n", "", "Topology name (default: the document's name, as the Publish dialog proposes it)")
	cmd.Flags().Bool("update", false, "Replace an existing topology of that name")
	cmd.Flags().String("user", "", "User to record as the publisher (default: the current OS user)")
	cmd.Flags().Bool("record-path", false,
		"Also record the absolute path of the document file in the topology's builder-doc annotation")

	return cmd
}

// publishBuilderDocument publishes the Builder document in the file at path
// as a topology, as `phenix builder publish` asks. What is wrong with the
// document, the request or the stored topology is the returned error's own
// text, since it is for the user to fix; only a failure of the store is
// humanized.
func publishBuilderDocument(cmd *cobra.Command, path string) error {
	var (
		dryRun = MustGetBool(cmd.Flags(), "dry-run")
		record = MustGetBool(cmd.Flags(), "record-path")
		actor  = MustGetString(cmd.Flags(), "user")
	)

	file, err := bapi.LoadDocumentFile(path)
	if err != nil {
		return builderFileError(path, err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving the path of %s: %w", path, err)
	}

	if actor == "" {
		if actor, err = builderPublisher(); err != nil {
			return err
		}
	}

	service, err := bapi.New()
	if err != nil {
		return util.HumanizeError(err, "%s", "Unable to publish Builder document "+path).Humanized()
	}

	publication, err := service.PublishTopology(cmd.Context(), bapi.PublishTopologyRequest{
		Document:   file.Data,
		Name:       MustGetString(cmd.Flags(), "name"),
		Actor:      actor,
		Update:     MustGetBool(cmd.Flags(), "update"),
		DryRun:     dryRun,
		Path:       absolute,
		RecordPath: record,
	})
	if err != nil {
		return builderPublishError(path, err)
	}

	warnings := publication.Warnings

	if base, mounts := common.PhenixBase, common.MountDir(); record &&
		!bapi.DocumentPathServed(base, []string{mounts}, absolute) {
		warnings = append(warnings, fmt.Sprintf(
			"The phenix server does not read Builder files from %s: it reads them below %s, except below %s. "+
				"The topology opens from the stored document.",
			absolute, base, mounts,
		))
	}

	if dryRun {
		return writeBuilderDryRun(cmd.OutOrStdout(), absolute, publication, warnings)
	}

	for _, warning := range warnings {
		plog.Warn(plog.TypeSystem, warning, "topology", publication.Name)
	}

	message := "topology already up to date"

	switch publication.Outcome {
	case bapi.TopologyCreated:
		message = "topology created"
	case bapi.TopologyUpdated:
		message = "topology updated"
	case bapi.TopologyUnchanged:
	}

	plog.Info(
		plog.TypeSystem,
		message,
		"name", publication.Name,
		"document", publication.Document.ID,
		"digest", publication.Digest,
	)

	return nil
}

// builderPublisher is the OS account a publication is recorded under when
// --user names none: the user who ran sudo when phenix was run through it
// (see getCurrentUserInfo), else the current user.
func builderPublisher() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("unable to determine the current user, name one with --user: %w", err)
	}

	if sudo := os.Getenv("SUDO_USER"); current.Uid == "0" && sudo != "" && sudoRanPhenix() {
		return sudo, nil
	}

	return current.Username, nil
}

// builderFileError says why the file at path cannot be used as a Builder
// document: what the builder package found wrong with it, without the prefix
// its errors carry for its other callers, or else why it could not be read.
func builderFileError(path string, err error) error {
	var (
		invalid *bapi.ValidationError
		large   *bapi.TooLargeError
	)

	switch {
	case errors.As(err, &large):
		const mebibyte = 1 << 20

		return fmt.Errorf("%s is larger than %d MiB, the most a Builder document may be", path, bapi.MaxDocumentBytes/mebibyte)
	case errors.As(err, &invalid) && invalid.Cause != nil:
		return fmt.Errorf("%s is not a valid Builder document: %w", path, invalid.Cause)
	case errors.As(err, &invalid):
		return fmt.Errorf("%s %s", path, invalid.Reason)
	}

	// The operating system's error names the file.
	if cause := errors.Unwrap(err); cause != nil {
		err = cause
	}

	return fmt.Errorf("unable to read Builder document: %w", err)
}

// builderPublishError is the error of a publication that did not go
// through. A refusal is shown as it is, with the flag that lifts it where
// there is one.
func builderPublishError(path string, err error) error {
	var (
		refused *bapi.PublishRefusedError
		invalid *bapi.ValidationError
	)

	switch {
	case errors.As(err, &refused) && refused.Refusal == bapi.PublishRefusedExists:
		return errors.New(refused.Error() + "; use --update to replace it")
	case errors.As(err, &refused):
		return errors.New(refused.Error())
	case errors.As(err, &invalid) && invalid.Field == "actor":
		return fmt.Errorf("the user to record as the publisher %s", invalid.Reason)
	}

	return util.HumanizeError(err, "%s", "Unable to publish Builder document "+path).Humanized()
}

// writeBuilderDryRun writes the report of a dry run: the document and the
// file it was read from, the digest, the ID and, when one is recorded, the
// path the topology's document reference would hold, what publishing would
// do to which topology, and every warning.
func writeBuilderDryRun(out io.Writer, path string, publication *bapi.TopologyPublication, warnings []string) error {
	result := "would be left as it is: it already holds this document"

	switch publication.Outcome {
	case bapi.TopologyCreated:
		result = "would be created"
	case bapi.TopologyUpdated:
		result = "would be updated"
	case bapi.TopologyUnchanged:
	}

	nodes, _ := publication.Config.Spec["nodes"].([]any)

	var report strings.Builder

	fmt.Fprintf(&report, "Document:     %s\n", publication.Title)
	fmt.Fprintf(&report, "File:         %s\n", path)
	fmt.Fprintf(&report, "Digest:       %s\n", publication.Digest)
	fmt.Fprintf(&report, "Document ID:  %s\n", publication.Reference.ID)

	if publication.Reference.Path != "" {
		fmt.Fprintf(&report, "Path:         %s\n", publication.Reference.Path)
	}

	fmt.Fprintf(&report, "Topology:     %s (%s)\n", publication.Name, result)
	fmt.Fprintf(&report, "Nodes:        %d\n", len(nodes))

	if len(warnings) > 0 {
		report.WriteString("Warnings:\n")

		for _, warning := range warnings {
			fmt.Fprintf(&report, "  - %s\n", warning)
		}
	}

	report.WriteString("Nothing was written.\n")

	if _, err := io.WriteString(out, report.String()); err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}

	return nil
}

func init() { //nolint:gochecknoinits // cobra command
	builderCmd := newBuilderCmd()
	builderCmd.AddCommand(newBuilderPublishCmd())

	addCommandToRoot(builderCmd, true)
}
