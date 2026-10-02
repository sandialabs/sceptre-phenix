package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/config"
	bdoc "phenix/types/builder"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
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

func configKindArgsValidator(multi, allowAll bool) cobra.PositionalArgs {
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

			kinds := append([]string(nil), configKinds()...)

			if allowAll {
				kinds = append(kinds, allExperiments)
			}

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
  phenix config list user`

	cmd := &cobra.Command{
		Use:       "list <kind>",
		Short:     "Show table of stored configuration files",
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

			fmt.Fprintln(os.Stdout)

			if len(configs) == 0 {
				fmt.Fprintln(os.Stdout, "There are no configurations available")
			} else {
				printer.PrintTableOfConfigs(os.Stdout, configs)
			}

			fmt.Fprintln(os.Stdout)

			return nil
		},
	}

	return cmd
}

func configGetArgsCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comps []string
	parts := strings.Split(toComplete, "/")

	if len(parts) == 1 {
		kinds := configKinds()
		for _, k := range kinds {
			if strings.HasPrefix(k, toComplete) {
				comps = append(comps, k+"/")
			}
		}
		return comps, cobra.ShellCompDirectiveNoSpace
	} else if len(parts) == configArgParts {
		kind := parts[0]
		var listKind string

		for _, k := range config.AllKinds {
			if strings.EqualFold(k, kind) {
				listKind = k
				break
			}
		}

		if listKind == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		configs, err := config.List(listKind)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, c := range configs {
			if strings.HasPrefix(c.Metadata.Name, parts[1]) {
				comps = append(comps, fmt.Sprintf("%s/%s", kind, c.Metadata.Name))
			}
		}
	}

	return comps, cobra.ShellCompDirectiveNoFileComp
}

func newConfigGetCmd() *cobra.Command {
	desc := `Get a configuration

  This subcommand is used to get a specific configuration file by kind/name.
  Valid options for kinds of configuration files are the same as described
  for the parent config command.`

	example := `
  phenix config get topology/foo
  phenix config get scenario/bar
  phenix config get experiment/foobar`

	cmd := &cobra.Command{
		Use:               "get <kind/name>",
		Short:             "Get a configuration",
		Long:              desc,
		Example:           example,
		Args:              configKindArgsValidator(false, false),
		ValidArgsFunction: configGetArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			upgraded := MustGetBool(cmd.Flags(), "show-upgraded")

			c, err := config.Get(args[0], upgraded)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to get the "+args[0]+" configuration")

				return err.Humanized()
			}

			if c.Kind == "Experiment" {
				// Clear experiment name... not applicable to end users.
				delete(c.Spec, "experimentName")
			}

			output := MustGetString(cmd.Flags(), "output")

			switch output {
			case FormatYAML:
				m, err := yaml.Marshal(c)
				if err != nil {
					err := util.HumanizeError(err, "Unable to convert configuration to YAML")

					return err.Humanized()
				}

				fmt.Fprintln(cmd.OutOrStdout(), string(m))
			case FormatJSON:
				var (
					m   []byte
					err error
				)

				if MustGetBool(cmd.Flags(), "pretty") {
					m, err = json.MarshalIndent(c, "", "  ")
				} else {
					m, err = json.Marshal(c)
				}

				if err != nil {
					err := util.HumanizeError(err, "Unable to convert configuration to JSON")

					return err.Humanized()
				}

				fmt.Fprintln(cmd.OutOrStdout(), string(m))
			default:
				return fmt.Errorf("unrecognized output format '%s'", output)
			}

			return nil
		},
	}

	cmd.Flags().StringP("output", "o", FormatYAML, "Configuration output format ('yaml' or 'json')")
	cmd.Flags().BoolP("pretty", "p", false, "Pretty print the JSON output")
	cmd.Flags().
		BoolP("show-upgraded", "u", false, "Show upgraded version of config (if not already latest version)")

	return cmd
}

func newConfigCreateCmd() *cobra.Command {
	desc := `Create a configuration(s)

  This subcommand is used to create one or more configurations from JSON or
  YAML file(s). A directory path can also be given, and all JSON and YAML
  files in the given directory will be parsed.

  A Builder document (the Builder's JSON or YAML export) is not a
  configuration. One found in a directory is skipped, and one named on the
  command line is refused: "phenix builder publish" creates its topology.`

	cmd := &cobra.Command{
		Use:   "create </path/to/filename> ...",
		Short: "Create a configuration(s)",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("must provide at least one configuration file")
			}

			skip := MustGetBool(cmd.Flags(), "skip-validation")

			for _, f := range args {
				configs, err := configFilesAt(f)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to create configuration from "+f)

					return err.Humanized()
				}

				for _, path := range configs {
					if isBuilderDocumentFile(path) {
						// The file named on the command line, and not one
						// found in a directory it names.
						if path == f {
							return fmt.Errorf(
								"%s is a Builder document, not a configuration: "+
									"use \"phenix builder publish %s\" to create its topology", path, path,
							)
						}

						plog.Info(plog.TypeSystem, "skipped Builder document; use phenix builder publish", "path", path)

						continue
					}

					opts := []config.CreateOption{config.CreateFromPath(path)}

					if !skip {
						opts = append(opts, config.CreateWithValidation())
					}

					c, err := config.Create(opts...)
					if err != nil {
						err := util.HumanizeError(
							err,
							"%s",
							"Unable to create configuration from "+path,
						)

						return err.Humanized()
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
			}

			return nil
		},
	}

	cmd.Flags().Bool("skip-validation", false, "Skip configuration spec validation against schema")

	return cmd
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

// isBuilderDocumentFile reports whether the file at path holds a Builder
// document, by its content: the Builder exports one as plain .json or .yaml.
// A file that cannot be read is not one, and is left to report why.
func isBuilderDocumentFile(path string) bool {
	text, err := os.ReadFile(path) //nolint:gosec // a config file the caller named
	if err != nil {
		return false
	}

	return bdoc.IsDocumentText(text)
}

func newConfigEditCmd() *cobra.Command {
	desc := `Edit a configuration

  This subcommand is used to edit a configuration using your default editor.
	`

	cmd := &cobra.Command{
		Use:   "edit <kind/name>",
		Short: "Edit a configuration",
		Long:  desc,
		Args:  configKindArgsValidator(false, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			force := MustGetBool(cmd.Flags(), "force")

			_, err := config.Edit(args[0], force)
			if err != nil {
				if config.IsConfigNotModified(err) {
					plog.Warn(plog.TypeSystem, "configuration not updated", "config", args[0])

					return nil
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

  This subcommand is used to delete one or more configurations.
	`

	cmd := &cobra.Command{
		Use:     "delete <kind/name> ...",
		Aliases: []string{deleteAlias},
		Short:   "Delete a configuration(s)",
		Long:    desc,
		Args:    configKindArgsValidator(true, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, c := range args {
				err := config.Delete(c)
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to delete the "+c+" configuration")

					return err.Humanized()
				}

				plog.Info(plog.TypeSystem, "configuration deleted", "config", c)
			}

			return nil
		},
	}

	return cmd
}

func newConfigDeleteAllCmd() *cobra.Command {
	desc := `Delete all configurations

  This subcommand is used to delete all configurations, or all configurations
  of a specific kind.`

	example := `
  phenix config delete all
  phenix config delete all topology
  phenix config delete all scenario`

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

			configs, err := config.List(which)
			if err != nil {
				err := util.HumanizeError(err, "Unable to list known configurations")

				return err.Humanized()
			}

			var errs error

			for _, c := range configs {
				name := strings.ToLower(c.Kind) + "/" + c.Metadata.Name

				err = config.Delete(name)
				if err != nil {
					errs = multierror.Append(errs, fmt.Errorf("deleting config %s: %w", name, err))
					continue
				}

				plog.Info(plog.TypeSystem, "configuration deleted", "config", name)
			}

			if errs != nil {
				err := util.HumanizeError(errs, "Unable to delete one or more configurations")
				return err.Humanized()
			}

			return nil
		},
	}

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
