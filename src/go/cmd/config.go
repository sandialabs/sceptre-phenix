package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
	bdoc "phenix/types/builder"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
	"phenix/util/sigterm"
)

const (
	configArgParts       = 2
	FormatJSON           = "json"
	FormatYAML           = "yaml"
	configKindExperiment = "experiment"
	commandImage         = "image"
	commandList          = "list"
	boolStringTrue       = "true"
)

func configKinds() []string {
	return []string{"topology", "scenario", configKindExperiment, commandImage, "user", "role"}
}

func configListKinds() []string {
	return []string{allExperiments, "topology", "scenario", configKindExperiment, commandImage, "user"}
}

func configKindArgsValidator(multi bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if multi {
			if len(args) == 0 {
				return errors.New("must provide at least one argument")
			}
		} else {
			if narg := len(args); narg != 1 {
				return fmt.Errorf("expected a single argument, received %d", narg)
			}
		}

		for _, arg := range args {
			tokens := strings.Split(arg, "/")

			if len(tokens) != configArgParts {
				return errors.New("expected an argument in the form of <config kind>/<config name>")
			}

			kinds := configKinds()

			kind := strings.ToLower(tokens[0])

			if !util.StringSliceContains(kinds, kind) {
				return fmt.Errorf(
					"expects the configuration kind to be one of %v, received %s",
					kinds,
					tokens[0],
				)
			}
		}

		return nil
	}
}

func configKindValidator() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if narg := len(args); narg > 1 {
			return fmt.Errorf("expected zero or one argument, received %d", narg)
		}

		if len(args) == 0 {
			return nil
		}

		kind := strings.ToLower(args[0])
		kinds := configKinds()

		if !util.StringSliceContains(kinds, kind) {
			return fmt.Errorf(
				"expects the configuration kind to be one of %v, received %s",
				kinds,
				args[0],
			)
		}

		return nil
	}
}

func newConfigCmd() *cobra.Command {
	desc := `Configuration file management

  This subcommand is used to manage the different kinds of phenix configuration
  files: topology, scenario, experiment, or image.`

	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"cfg"},
		Short:   "Configuration file management",
		Long:    desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newConfigListCmd() *cobra.Command {
	example := `
  phenix config list all
  phenix config list topology
  phenix config list scenario
  phenix config list experiment
  phenix config list image
  phenix config list user
  phenix config list topology -o json`

	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list <kind>",
		Short: "Show table of stored configuration files",
		Long: `Show table of stored configuration files

  The JSON and YAML output give the apiVersion, kind and metadata of each
  configuration, as GET /api/v1/configs does, without the spec.`,
		Example:   example,
		ValidArgs: configListKinds(),
		RunE: func(cmd *cobra.Command, args []string) error {
			var kinds string

			if len(args) > 0 {
				kinds = args[0]
			}

			configs, err := config.List(kinds)
			if err != nil {
				err := util.HumanizeError(err, "Unable to list known configurations")

				return err.Humanized()
			}

			listed := make([]store.Config, 0, len(configs))

			for _, c := range configs {
				c.Spec, c.Status = nil, nil
				listed = append(listed, c)
			}

			return out.print(cmd, listed, outputViews{
				formatTable: func(w io.Writer) error {
					fmt.Fprintln(w)

					if len(configs) == 0 {
						fmt.Fprintln(w, "There are no configurations available")
					} else {
						printer.PrintTableOfConfigs(w, configs)
					}

					fmt.Fprintln(w)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML})

	return cmd
}

func newConfigGetCmd() *cobra.Command {
	desc := `Get a configuration

  This subcommand is used to get a specific configuration file by kind/name.
  Valid options for kinds of configuration files are the same as described
  for the parent config command.`

	example := `
  phenix config get topology/foo
  phenix config get scenario/bar
  phenix config get experiment/foobar
  phenix config get topology/foo -o json`

	var out *outputFlag

	cmd := &cobra.Command{
		Use:               "get <kind/name>",
		Short:             "Get a configuration",
		Long:              desc,
		Example:           example,
		Args:              configKindArgsValidator(false),
		ValidArgsFunction: configGetArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Get(args[0])
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to get the "+args[0]+" configuration")

				return err.Humanized()
			}

			if c.Kind == "Experiment" {
				// Clear experiment name... not applicable to end users.
				delete(c.Spec, "experimentName")
			}

			return out.print(cmd, c, nil)
		},
	}

	out = addOutputFlag(cmd, FormatYAML, []string{FormatYAML, FormatJSON})
	// JSON output is always indented now; -p stays so that existing scripts work.
	cmd.Flags().BoolP("pretty", "p", false, "Pretty print the JSON output (JSON output is always indented)")

	return cmd
}

func newConfigCreateCmd() *cobra.Command {
	desc := `Create a configuration(s)

  This subcommand is used to create one or more configurations from JSON or
  YAML file(s). Each argument is the path of a file with the extension
  .json, .yaml or .yml. A directory is an error: give its files, or use
  "phenix config apply -f" for a directory.

  A Builder document (the Builder's JSON or YAML export), a Builder
  template file, a Builder package and a Builder layer set file are not
  configurations. The command refuses such a file. Use the Builder in the web
  UI, or the Builder REST API below /api/v1/builder/, for these files. Upload
  a document and publish it to create its topology. Use Import templates to
  add the templates of a template file. Use Upload to open a package. Use
  Upload layer set on the Layers tab to add a layer set file.` + configVarsHelp + `

  The command reads every file and checks every variable before it stores a
  configuration.`

	example := `
  phenix config create topology.yml scenario.yml
  phenix config create ./configs/*.yml
  phenix config create topology.yml -e NODES=3 --env-file site.env`

	cmd := &cobra.Command{
		Use:     "create </path/to/filename> ...",
		Short:   "Create a configuration(s)",
		Long:    desc,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("must provide at least one configuration file")
			}

			skip := mustGet(cmd.Flags().GetBool, "skip-validation")
			ctx := sigterm.CancelContext(commandContext(cmd))

			for _, arg := range args {
				if info, err := os.Stat(arg); err == nil && info.IsDir() {
					return fmt.Errorf("%s is a directory: %w", arg, errConfigDirectory)
				}
			}

			vars, err := configVarsFromFlags(cmd)
			if err != nil {
				return err
			}

			inputs, err := readConfigInputs(args, vars)
			if err != nil {
				return err
			}

			// The command stores no config when it cannot read an argument,
			// or when an argument is not a config file.
			for _, in := range inputs {
				switch {
				case in.err == nil:
				case in.builder:
					return in.err
				case errors.Is(in.err, errNoConfigFiles):
					return fmt.Errorf("%s: %w", in.path, errNotConfigFile)
				default:
					return configCreateError(in.path, nil, in.err)
				}
			}

			for _, in := range inputs {
				c, err := createConfigInput(ctx, in, skip)
				if err != nil {
					return configCreateError(in.path, in.text, err)
				}

				plog.Info(
					plog.TypeSystem,
					"configuration created",
					"kind",
					c.Kind,
					"name",
					c.Metadata.Name,
				)
			}

			return nil
		},
	}

	cmd.Flags().Bool("skip-validation", false, "Skip configuration spec validation against schema")
	addVarFlags(cmd)

	return cmd
}

// createConfigInput stores the config of in, a file that was read with no
// error. It validates the config against its schema unless skip is set.
func createConfigInput(ctx context.Context, in configInput, skip bool) (*store.Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c, err := store.DecodeConfigFile(in.path, in.text)
	if err != nil {
		return nil, fmt.Errorf("creating new config from file: %w", err)
	}

	opts := []config.CreateOption{config.CreateFromConfig(c), config.CreateWithContext(ctx)}

	if !skip {
		opts = append(opts, config.CreateWithValidation())
	}

	return config.Create(opts...)
}

// configFilesAt returns the JSON and YAML files at path: the file itself, or
// the files of the directory and of the directories below it.
func configFilesAt(path string) ([]string, error) {
	var configs []string

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		extensions := []string{"*.json", "*.yaml", "*.yml"}

		for _, ext := range extensions {
			match, err := filepath.Match(ext, filepath.Base(path))
			if err != nil {
				return err
			}

			if match {
				configs = append(configs, path)

				break
			}
		}

		return nil
	})

	return configs, err //nolint:wrapcheck // the caller words the error
}

// Kinds of Builder-owned files, none of which holds a configuration (see
// [builderFileKind]).
const (
	builderFileDocument     = "Builder document"
	builderFileTemplateFile = "Builder template file"
	builderFilePackage      = "Builder package"
	builderFileLayerSetFile = "Builder layer set file"
)

// builderFileKind returns what the file at path is, by its content, when it is
// a Builder-owned file: a Builder document, a template file, a package or a
// layer set file. The Builder exports each of these as plain .json or .yaml. It returns "" for any
// other file. It also returns "" for a file that cannot be read, and leaves it
// to the normal read to report why.
func builderFileKind(path string) string {
	text, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	switch bdoc.FileKindOf(text) {
	case bdoc.FileKindDocument:
		return builderFileDocument
	case bdoc.FileKindTemplateFile:
		return builderFileTemplateFile
	case bdoc.FileKindPackage:
		return builderFilePackage
	case bdoc.FileKindLayerSetFile:
		return builderFileLayerSetFile
	case bdoc.FileKindOther:
	}

	return ""
}

// skipBuilderFile logs that a config command skipped the Builder-owned file at
// path, of kind, found in a directory it was given. The line of a Builder
// document says where to publish it. The other kinds are logged at debug
// level.
func skipBuilderFile(kind, path string) {
	if kind == builderFileDocument {
		plog.Info(plog.TypeSystem, "skipped Builder document. Upload it in the Builder to publish it", "path", path)

		return
	}

	plog.Debug(plog.TypeSystem, "skipped Builder file, which is not a configuration", "kind", kind, "path", path)
}

// builderFileRefusal is the error of config create for the Builder-owned file
// at path, of kind, named on the command line. The error says what to do with
// the file instead.
func builderFileRefusal(kind, path string) error {
	switch kind {
	case builderFileTemplateFile:
		return fmt.Errorf(
			"%s is a Builder template file, not a configuration. To add its Node Templates, use Import templates "+
				"in the Builder or the Builder REST API (POST /api/v1/builder/templates/items)", path,
		)
	case builderFilePackage:
		return fmt.Errorf(
			"%s is a Builder package, not a configuration. Upload it in the Builder to open its diagram", path,
		)
	case builderFileLayerSetFile:
		return fmt.Errorf(
			"%s is a Builder layer set file, not a configuration. "+
				"Use Upload layer set on the Layers tab of the Builder to add its layer set", path,
		)
	}

	return fmt.Errorf(
		"%s is a Builder document, not a configuration. Upload it in the Builder "+
			"or send it to the Builder REST API (/api/v1/builder/drafts). Then publish it to create its topology", path,
	)
}

// configCreateError is the error "config create" returns when the config in
// path cannot be created. src is the text of the file with its variables
// filled in. A schema validation failure adds the lines from
// [types.ExplainValidationError], which point into src, each on its own
// indented line.
func configCreateError(path string, src []byte, err error) error {
	h := util.HumanizeError(err, "%s", canceled("Unable to create configuration from "+path, err))

	if !errors.Is(err, types.ErrValidationFailed) {
		return h.Humanized()
	}

	lines := types.ExplainValidationError(src, err)

	return fmt.Errorf("%w\n  %s", h.Humanized(), strings.Join(lines, "\n  "))
}

func newConfigEditCmd() *cobra.Command {
	desc := `Edit a configuration

  This subcommand is used to edit a configuration using your default editor.
	`

	cmd := &cobra.Command{
		Use:               "edit <kind/name>",
		Short:             "Edit a configuration",
		Long:              desc,
		Args:              configKindArgsValidator(false),
		ValidArgsFunction: configGetArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			force := mustGet(cmd.Flags().GetBool, "force")

			_, err := config.Edit(args[0], force)
			if err != nil {
				if config.IsConfigNotModified(err) {
					plog.Warn(plog.TypeSystem, "configuration not updated", "config", args[0])

					return nil
				}

				if errors.Is(err, store.ErrConflict) {
					err := util.HumanizeError(
						err,
						"%s",
						args[0]+" changed while you edited it, so run `phenix config edit` again",
					)

					return err.Humanized()
				}

				err := util.HumanizeError(
					err,
					"%s",
					"Unable to edit the "+args[0]+" configuration provided",
				)

				return err.Humanized()
			}

			plog.Info(plog.TypeSystem, "configuration updated", "config", args[0])

			return nil
		},
	}

	cmd.Flags().
		Bool("force", false, "override checks (only applies to configs for running experiments)")

	return cmd
}

func newConfigDeleteCmd() *cobra.Command {
	desc := `Delete a configuration(s)

  This subcommand is used to delete one or more configurations. With
  --dry-run, it shows the configurations that it would delete, and deletes
  nothing.

  A name can contain the wildcard *, which matches zero or more characters.
  For example, topology/cm-* selects each topology whose name starts with
  "cm-". Put quotation marks around a pattern, so that your shell does not
  expand it. The kind cannot contain *. The pattern <kind>/* does the same
  as "delete all <kind>", and */* does the same as "delete all". Another
  pattern that selects no configuration is an error. Before phenix deletes
  the configurations that a pattern selects, it shows their names. A pattern
  does not delete the configuration of a running experiment.

  When phenix cannot delete a configuration, it continues with the other
  ones. Then it exits with an error that names each failure.
	`

	example := `
  phenix config delete topology/foo
  phenix config delete topology/foo scenario/bar
  phenix config delete 'topology/cm-*'
  phenix config delete 'topology/cm-*' --dry-run`

	var dry dryRun

	cmd := &cobra.Command{
		Use:               "delete <kind/name> ...",
		Aliases:           []string{deleteAlias},
		Short:             "Delete a configuration(s)",
		Long:              desc,
		Example:           example,
		Args:              configDeleteArgsValidator(),
		ValidArgsFunction: completePositional(completeConfigNames),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan := newDeletePlan()

			for _, arg := range args {
				kind, name, _ := strings.Cut(arg, "/")

				switch {
				case arg == deleteWildcard+"/"+deleteWildcard:
					plan.addPattern(arg, allExperiments, deleteWildcard, true)
				case isDeletePattern(name):
					plan.addPattern(arg, strings.ToLower(kind), name, name == deleteWildcard)
				default:
					plan.addExact(arg, kind, name)
				}
			}

			failed := plan.failed
			item := func(key string) string { return "configuration " + key }
			deleted := func(key string) { plog.Info(plog.TypeSystem, "configuration deleted", "config", key) }

			for _, step := range plan.steps {
				if step.key == "" {
					writeDeleteMatches(cmd.OutOrStdout(), "configuration", step.arg, step.matches)
					failed = append(failed, deleteStepMatches(dry, step, item, deleted)...)

					continue
				}

				if err := deleteConfig(dry, step.arg); err != nil {
					failed = append(failed, err)
				}
			}

			return deleteFailures(failed, "configurations")
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

// deleteConfig deletes the config c, given as kind/name, or shows the delete
// in a dry run.
func deleteConfig(dry dryRun, c string) *util.HumanizedError {
	if dry.enabled() {
		if _, err := config.Get(c); err != nil {
			return util.HumanizeError(err, "%s", "Unable to delete the "+c+" configuration")
		}
	}

	if dry.skip("delete", "configuration "+c) {
		return nil
	}

	if err := config.Delete(c); err != nil {
		return util.HumanizeError(err, "%s", "Unable to delete the "+c+" configuration")
	}

	plog.Info(plog.TypeSystem, "configuration deleted", "config", c)

	return nil
}

// configDeleteArgsValidator checks the arguments of config delete as
// configKindArgsValidator does, and also takes */*. A kind that contains the
// wildcard * is an error.
func configDeleteArgsValidator() cobra.PositionalArgs {
	every := deleteWildcard + "/" + deleteWildcard
	check := configKindArgsValidator(true)

	return func(cmd *cobra.Command, args []string) error {
		rest := make([]string, 0, len(args))

		for _, arg := range args {
			if arg == every {
				continue
			}

			if kind, _, _ := strings.Cut(arg, "/"); isDeletePattern(kind) {
				return fmt.Errorf("the configuration kind cannot contain %s (use %s for all configurations), received %s",
					deleteWildcard, every, arg)
			}

			rest = append(rest, arg)
		}

		if len(args) > 0 && len(rest) == 0 {
			return nil
		}

		return check(cmd, rest)
	}
}

func newConfigDeleteAllCmd() *cobra.Command {
	desc := `Delete all configurations

  This subcommand is used to delete all configurations, or all configurations
  of a specific kind. It does not delete the configuration of a running
  experiment. With --dry-run, it shows the configurations that it would
  delete, and deletes nothing.`

	example := `
  phenix config delete all
  phenix config delete all topology
  phenix config delete all scenario
  phenix config delete all topology --dry-run`

	var dry dryRun

	cmd := &cobra.Command{
		Use:       "all [kind]",
		Short:     "Delete all configurations (optionally by kind)",
		Long:      desc,
		Example:   example,
		Args:      configKindValidator(),
		ValidArgs: configKinds(),
		RunE: func(cmd *cobra.Command, args []string) error {
			which := allExperiments
			if len(args) == 1 {
				which = strings.ToLower(args[0])
			}

			var opts []config.DeleteKindOption
			if dry.enabled() {
				opts = append(opts, config.DeleteKindWith(func(name string) error {
					kind, rest, _ := strings.Cut(name, "/")
					dry.skip("delete", "configuration "+strings.ToLower(kind)+"/"+rest)

					return nil
				}))
			}

			deleted, failed, err := config.DeleteKind(which, opts...)
			if err != nil {
				err := util.HumanizeError(err, "Unable to list known configurations")

				return err.Humanized()
			}

			if !dry.enabled() {
				for _, name := range deleted {
					plog.Info(plog.TypeSystem, "configuration deleted", "config", name)
				}
			}

			var errs error

			for _, f := range failed {
				errs = multierror.Append(errs, fmt.Errorf("deleting config %s: %w", f.Config, f.Err))
			}

			if errs != nil {
				err := util.HumanizeError(errs, "Unable to delete one or more configurations")
				return err.Humanized()
			}

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	configCmd := newConfigCmd()
	deleteCmd := newConfigDeleteCmd()
	deleteCmd.AddCommand(newConfigDeleteAllCmd())

	configCmd.AddCommand(newConfigListCmd())
	configCmd.AddCommand(newConfigGetCmd())
	configCmd.AddCommand(newConfigCreateCmd())
	configCmd.AddCommand(newConfigEditCmd())
	configCmd.AddCommand(deleteCmd)

	addCommandToRoot(configCmd, true)
}
