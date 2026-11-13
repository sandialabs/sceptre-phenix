package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	"phenix/util/envsubst"
	"phenix/util/sigterm"
)

// configFileFlag names the files and directories of config apply.
const configFileFlag = "filename"

// fileConfig is a config that config apply or config diff read from a file,
// or the error that stopped the read. Name is the kind/name of the config as
// the CLI spells it, "" when it is not known. Src is the text of the file
// with its variables filled in.
type fileConfig struct {
	path   string
	src    []byte
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

// loadFileConfigs reads the configs of the JSON and YAML files at paths, as
// [readConfigInputs] does, with the variables of vars filled in. Unreadable
// files come first, then the configs by kind (see [config.ApplyOrder]) in
// path order. A second file with the same kind and name is an error. A
// problem with a variable is the error of the call: no config is then read.
func loadFileConfigs(paths []string, vars *envsubst.Set) ([]fileConfig, error) {
	inputs, err := readConfigInputs(paths, vars)
	if err != nil {
		return nil, err
	}

	configs := make([]fileConfig, 0, len(inputs))

	for _, in := range inputs {
		if in.err != nil {
			configs = append(configs, fileConfig{path: in.path, src: nil, name: "", config: nil, err: in.err})

			continue
		}

		c, err := store.DecodeConfigFile(in.path, in.text)
		if err != nil {
			configs = append(configs, fileConfig{path: in.path, src: in.text, name: "", config: nil, err: err})

			continue
		}

		configs = append(configs, fileConfig{path: in.path, src: in.text, name: cliConfigName(c), config: c, err: nil})
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

	return configs, nil
}

// cliConfigName returns the kind/name of c as the CLI spells it, with the
// kind in lower case.
func cliConfigName(c *store.Config) string {
	return strings.ToLower(c.Kind) + "/" + c.Metadata.Name
}

// configFileError returns the text of err for the config whose file has the
// text src, with its variables filled in. A schema validation failure adds
// the lines from [types.ExplainValidationError], each on its own indented
// line. The lines point into src, which has the lines of the file.
func configFileError(src []byte, err error) string {
	if errors.Is(err, store.ErrConflict) {
		return "the config changed while phenix applied it, so run the command again: " + err.Error()
	}

	if !errors.Is(err, types.ErrValidationFailed) {
		return err.Error()
	}

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
  nothing.` + configVarsHelp

	example := `
  phenix config apply -f topology.yml -f scenario.yml
  phenix config apply -f ./configs --dry-run
  phenix config apply -f topology.yml -e NODES=3 --env-file site.env`

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

			ctx := sigterm.CancelContext(commandContext(cmd))
			opts := []config.ApplyOption{config.ApplyWithContext(ctx)}

			if dry.enabled() {
				opts = append(opts, config.ApplyDryRun())
			}

			if mustGet(cmd.Flags().GetBool, "skip-validation") {
				opts = append(opts, config.ApplySkipCreateValidation())
			}

			vars, err := configVarsFromFlags(cmd)
			if err != nil {
				return err
			}

			configs, err := loadFileConfigs(paths, vars)
			if err != nil {
				return err
			}

			return applyConfigs(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), configs, dry, opts...)
		},
	}

	cmd.Flags().StringArrayP(configFileFlag, "f", nil, "Configuration file or directory to apply (repeatable)")
	dry = addDryRunFlag(cmd)
	cmd.Flags().Bool("skip-validation", false, "Skip schema validation of new configurations (updates are always validated)")
	addVarFlags(cmd)

	_ = cmd.MarkFlagRequired(configFileFlag)
	_ = cmd.MarkFlagFilename(configFileFlag, "json", "yaml", "yml")

	return cmd
}

// applyConfigs applies each config in turn, writes a line for each to out, or
// to errOut when it failed, and returns an error when one or more failed. In
// a dry run, the line of a config that would change comes from dry. Give
// opts the config.ApplyDryRun option in a dry run. It applies no more
// configs once ctx is done.
func applyConfigs(
	ctx context.Context, out, errOut io.Writer, configs []fileConfig, dry dryRun, opts ...config.ApplyOption,
) error {
	applied, failed := 0, 0

	for _, f := range configs {
		if err := ctx.Err(); err != nil {
			return applyCanceled(applied, failed, len(configs), err)
		}

		if f.err != nil {
			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.src, f.err))

			continue
		}

		action, err := config.Apply(f.config, opts...)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, context.Canceled) {
				fmt.Fprintf(errOut, "%s: canceled\n", f.label())

				return applyCanceled(applied, failed, len(configs), ctx.Err())
			}

			failed++

			fmt.Fprintf(errOut, "%s: error: %s\n", f.label(), configFileError(f.src, err))

			continue
		}

		applied++

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

// applyCanceled is the error of a config apply that a signal canceled after it
// applied some of total configs and some failed.
func applyCanceled(applied, failed, total int, err error) error {
	msg := fmt.Sprintf("applied %d of %d configurations", applied, total)

	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}

	return fmt.Errorf("%s: %w", canceled(msg, err), err)
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
