package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"phenix/api/config"
)

// Exit codes of config diff, as diff(1) gives them.
const (
	exitDiffers   = 1
	exitDiffError = 2
)

func newConfigDiffCmd() *cobra.Command {
	desc := `Show how configuration files differ from the stored configurations

  This subcommand prints a unified diff from each stored configuration to the
  configuration of the same kind and name in the given JSON or YAML files. It
  prints "new" for a configuration that is not stored. It prints nothing for a
  configuration that is the same. An argument can be a file or a directory,
  as for config apply.

  The diff shows the apiVersion, kind, name, annotations and spec, as YAML
  with sorted keys. It ignores the fields that phenix sets: created, updated,
  revision, status and the experiment name. A configuration that config diff
  shows as the same is one that config apply does not change.

  The command exits with 0 when no configuration differs, 1 when one or more
  differ, and 2 when it cannot read or compare a configuration.`

	example := `
  phenix config diff topology.yml
  phenix config diff ./configs`

	cmd := &cobra.Command{
		Use:     "diff <file|dir>...",
		Short:   "Show how configuration files differ from the stored configurations",
		Long:    desc,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return &exitCodeError{
					err:  errors.New("must provide at least one configuration file or directory"),
					code: exitDiffError,
				}
			}

			return diffConfigs(cmd.OutOrStdout(), cmd.ErrOrStderr(), loadFileConfigs(args))
		},
	}

	return cmd
}

// diffConfigs writes the diff of each config to out, and each error to errOut.
// Its error sets the exit code: exitDiffers when a config differs, and
// exitDiffError when a config cannot be read or compared.
func diffConfigs(out, errOut io.Writer, configs []fileConfig) error {
	var differ, failed int

	for _, f := range configs {
		if f.err != nil {
			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.path, f.err))

			continue
		}

		cmp, err := config.Compare(f.config)
		if err != nil {
			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.path, err))

			continue
		}

		switch {
		case cmp.Stored == nil:
			differ++

			fmt.Fprintf(out, "%s new\n", f.label())
		case cmp.Changed():
			differ++

			fmt.Fprint(out, cmp.Diff("stored "+f.label(), f.path))
		}
	}

	switch {
	case failed > 0:
		return &exitCodeError{
			err:  fmt.Errorf("%d of %d configurations could not be compared", failed, len(configs)),
			code: exitDiffError,
		}
	case differ > 0:
		return &exitCodeError{
			err:  fmt.Errorf("%d of %d configurations differ from the store", differ, len(configs)),
			code: exitDiffers,
		}
	}

	return nil
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("config").AddCommand(newConfigDiffCmd())
}
