package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/config"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

const (
	configArgParts = 2
	FormatJSON     = "json"
	FormatYAML     = "yaml"
)

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

			kinds := []string{"topology", "scenario", "experiment", "image", "user", "role"}

			if allowAll {
				kinds = append(kinds, "all")
			}

			if kind := tokens[0]; !util.StringSliceContains(kinds, kind) {
				return fmt.Errorf(
					"expects the configuration kind to be one of %v, received %s",
					kinds,
					kind,
				)
			}
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
		ValidArgs: []string{"all", "topology", "scenario", "experiment", "image", "user"},
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
		kinds := []string{"topology", "scenario", "experiment", "image", "user", "role"}
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

				fmt.Fprintln(os.Stdout, string(m))
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

				fmt.Fprintln(os.Stdout, string(m))
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
  files in the given directory will be parsed.`

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
				var configs []string

				err := filepath.Walk(f, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}

					// Don't recursively process subdirectories.
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
				if err != nil {
					err := util.HumanizeError(err, "%s", "Unable to create configuration from "+f)

					return err.Humanized()
				}

				for _, f := range configs {
					opts := []config.CreateOption{config.CreateFromPath(f)}

					if !skip {
						opts = append(opts, config.CreateWithValidation())
					}

					c, err := config.Create(opts...)
					if err != nil {
						err := util.HumanizeError(
							err,
							"%s",
							"Unable to create configuration from "+f,
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
		Use:   "delete <kind/name> ...",
		Short: "Delete a configuration(s)",
		Long:  desc,
		Args:  configKindArgsValidator(true, true),
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

func init() { //nolint:gochecknoinits // cobra command
	configCmd := newConfigCmd()

	configCmd.AddCommand(newConfigListCmd())
	configCmd.AddCommand(newConfigGetCmd())
	configCmd.AddCommand(newConfigCreateCmd())
	configCmd.AddCommand(newConfigEditCmd())
	configCmd.AddCommand(newConfigDeleteCmd())

	rootCmd.AddCommand(configCmd)
}
