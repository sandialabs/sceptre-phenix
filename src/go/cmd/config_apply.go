package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
)

// configFileFlag names the files and directories of config apply.
const configFileFlag = "filename"

// fileConfig is a config that config apply or config diff read from a file,
// or the error that stopped the read. Name is the kind/name of the config as
// the CLI spells it, "" when it is not known.
type fileConfig struct {
	path   string
	name   string
	config *store.Config
	err    error
}

// label is what an output line names the config by.
func (f fileConfig) label() string {
	if f.name != "" {
		return f.name
	}

	return f.path
}

// loadFileConfigs reads the configs of the JSON and YAML files at paths (files
// or directories) as config create does: it skips a Builder file in a directory
// and refuses one in paths. Unreadable files come first, then the configs by
// kind (see [config.ApplyOrder]) in path order. A second file with the same
// kind and name is an error.
func loadFileConfigs(paths []string) []fileConfig {
	var configs []fileConfig

	for _, arg := range paths {
		files, err := configFilesAt(arg)
		if err != nil {
			configs = append(configs, fileConfig{path: arg, name: "", config: nil, err: err})

			continue
		}

		if len(files) == 0 {
			configs = append(configs, fileConfig{
				path: arg, name: "", config: nil, err: errors.New("no JSON or YAML files found"),
			})
		}

		for _, path := range files {
			if kind := builderFileKind(path); kind != "" {
				if path == arg {
					configs = append(configs, fileConfig{path: path, name: "", config: nil, err: builderFileRefusal(kind, path)})
				} else {
					skipBuilderFile(kind, path)
				}

				continue
			}

			c, err := store.NewConfigFromFile(path)
			if err != nil {
				configs = append(configs, fileConfig{path: path, name: "", config: nil, err: err})

				continue
			}

			configs = append(configs, fileConfig{path: path, name: cliConfigName(c), config: c, err: nil})
		}
	}

	slices.SortStableFunc(configs, func(a, b fileConfig) int {
		switch {
		case a.config == nil && b.config == nil:
			return 0
		case a.config == nil:
			return -1
		case b.config == nil:
			return 1
		}

		return config.ApplyOrder(a.config.Kind, b.config.Kind)
	})

	definedBy := map[string]string{}

	for i, f := range configs {
		if f.config == nil {
			continue
		}

		if first, ok := definedBy[f.name]; ok {
			configs[i].err = fmt.Errorf("%s also defines %s; only the first file is used", first, f.name)
			configs[i].config = nil

			continue
		}

		definedBy[f.name] = f.path
	}

	return configs
}

// cliConfigName returns the kind/name of c as the CLI spells it, with the
// kind in lower case.
func cliConfigName(c *store.Config) string {
	return strings.ToLower(c.Kind) + "/" + c.Metadata.Name
}

// configFileError returns the text of err for the config of the file at path.
// A schema validation failure adds the lines from
// [types.ExplainValidationError], each on its own indented line.
func configFileError(path string, err error) string {
	if errors.Is(err, store.ErrConflict) {
		return "the config changed while phenix applied it, so run the command again: " + err.Error()
	}

	if !errors.Is(err, types.ErrValidationFailed) {
		return err.Error()
	}

	// The explained lines point into the file; if it is gone, the explainer
	// falls back to the validator's text.
	src, _ := os.ReadFile(path)
	lines := types.ExplainValidationError(src, err)

	return err.Error() + "\n  " + strings.Join(lines, "\n  ")
}

func newConfigApplyCmd() *cobra.Command {
	desc := `Create or update configurations from files

  This subcommand creates each configuration in the given JSON or YAML files
  that is not stored, updates each stored configuration that is different,
  and does not change a configuration that is the same. A -f value can be a
  file or a directory. The command reads all JSON and YAML files in a
  directory and its subdirectories, as config create does.

  The command applies Topology configurations first, then Scenario, then
  Experiment, then Image and Role configurations, because a scenario and an
  experiment refer to a topology. It does not apply User configurations,
  because their files do not hold the password hash and the tokens.

  A comparison looks at the apiVersion, kind, name, annotations and spec. It
  ignores the fields that phenix sets: created, updated, revision, status and
  the experiment name. "phenix config diff" shows the differences. phenix
  adds values to an experiment configuration when it stores it. Thus, apply
  an experiment file that "phenix config get" wrote, or the file is always
  different.

  An update goes through the same checks as config edit. phenix refuses to
  update the configuration of a running experiment, and refuses an update
  when the configuration changes while the command runs. The command prints
  one line for each configuration. It tries all configurations, and then
  exits with an error if one or more failed.

  With --dry-run, the command checks each configuration as for an apply,
  shows the configurations that it would create or update, and changes
  nothing.`

	example := `
  phenix config apply -f topology.yml -f scenario.yml
  phenix config apply -f ./configs --dry-run`

	var dry dryRun

	cmd := &cobra.Command{
		Use:     "apply -f <file|dir> [-f <file|dir>]...",
		Short:   "Create or update configurations from files",
		Long:    desc,
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths := mustGet(cmd.Flags().GetStringArray, configFileFlag)
			if len(paths) == 0 {
				return errors.New("must provide at least one configuration file or directory with -f")
			}

			var opts []config.ApplyOption

			if dry.enabled() {
				opts = append(opts, config.ApplyDryRun())
			}

			if mustGet(cmd.Flags().GetBool, "skip-validation") {
				opts = append(opts, config.ApplySkipCreateValidation())
			}

			return applyConfigs(cmd.OutOrStdout(), cmd.ErrOrStderr(), loadFileConfigs(paths), dry, opts...)
		},
	}

	cmd.Flags().StringArrayP(configFileFlag, "f", nil, "Configuration file or directory to apply (repeatable)")
	dry = addDryRunFlag(cmd)
	cmd.Flags().Bool("skip-validation", false, "Skip schema validation of new configurations (updates are always validated)")

	_ = cmd.MarkFlagRequired(configFileFlag)
	_ = cmd.MarkFlagFilename(configFileFlag, "json", "yaml", "yml")

	return cmd
}

// applyConfigs applies each config in turn, writes a line for each to out, or
// to errOut when it failed, and returns an error when one or more failed. In
// a dry run, the line of a config that would change comes from dry. Give
// opts the config.ApplyDryRun option in a dry run.
func applyConfigs(out, errOut io.Writer, configs []fileConfig, dry dryRun, opts ...config.ApplyOption) error {
	failed := 0

	for _, f := range configs {
		if f.err != nil {
			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.path, f.err))

			continue
		}

		action, err := config.Apply(f.config, opts...)
		if err != nil {
			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.path, err))

			continue
		}

		if action != config.ApplyUnchanged && dry.skip(applyVerb(action), "configuration "+f.label()) {
			continue
		}

		fmt.Fprintf(out, "%s %s\n", f.label(), action)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d configurations failed", failed, len(configs))
	}

	return nil
}

// applyVerb is the verb of action in the line of a dry run, such as "create".
func applyVerb(action config.ApplyAction) string {
	if action == config.ApplyCreated {
		return "create"
	}

	return "update"
}

func init() { //nolint:gochecknoinits // cobra command
	subcommandOf("config").AddCommand(newConfigApplyCmd())
}
