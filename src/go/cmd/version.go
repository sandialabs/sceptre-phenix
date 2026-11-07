package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"phenix/version"
	"phenix/web"
)

func newVersionCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long: `Print version information

  The JSON and YAML output have the keys of GET /version.`,
		Args: argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			info := web.Version{Commit: version.Commit, Tag: version.Tag, BuildDate: version.Date, Repo: version.Repo}

			return out.printAPI(cmd, info, outputViews{
				formatText: func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "%s (commit %s) %s\n", version.Label(), version.Commit, version.Date)

					return err //nolint:wrapcheck // the writer's own error
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatText, []string{formatText, FormatJSON, FormatYAML})

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newVersionCmd(), true)
}
