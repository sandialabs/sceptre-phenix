package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
	"phenix/api/settings"
	"phenix/types"
	"phenix/util"
	"phenix/util/plog"
	"phenix/util/printer"
)

const (
	editArgsMin = 2
	editArgsMax = 3
	setArgs     = 2
)

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "View, list, or edit phenix system settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newSettingsDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "View or edit legacy database settings",
		Long:  "Manage legacy settings stored in the internal database. For active runtime configuration (logging, etc.), use the root settings commands (get/set).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newSettingsDBListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:               "list [category]",
		Short:             "List settings in the database",
		Args:              argsWithUsage(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeArgs(completeValues(dbSettingCategories()...)),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := settings.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to print a table")

				return err.Humanized()
			}

			if len(args) > 0 {
				category := args[0]

				filtered := make([]types.Setting, 0, len(s))

				for _, setting := range s {
					// Filter by category prefix (e.g. "Category.Name")
					if strings.HasPrefix(
						strings.ToLower(setting.Metadata.Name),
						strings.ToLower(category)+".",
					) {
						filtered = append(filtered, setting)
					}
				}

				s = filtered
			}

			return out.print(cmd, s, outputViews{
				formatTable: func(w io.Writer) error {
					printer.PrintTableOfSettings(w, s)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML}, withJSONFlag())

	return cmd
}

// dbSettingCategories returns the categories of the database settings.
func dbSettingCategories() []string {
	var categories []string

	for _, s := range settings.DefaultSettings {
		if !slices.Contains(categories, s.Category) {
			categories = append(categories, s.Category)
		}
	}

	return categories
}

func newSettingsDBEditCmd() *cobra.Command {
	example := `
  phenix settings db edit <category> <name> <newValue>
  phenix settings db edit Password MinLength 20`

	cmd := &cobra.Command{
		Use:               "edit <category> <name> <value>",
		ValidArgsFunction: completeArgs(completeDBEditCategories, completeDBSettingNames),
		Short:             "Edit a setting in the database",
		Long: "Edit a setting in the database. This command does not change the log settings: use " +
			"'phenix settings set' for them (for example, phenix settings set log.system.max-size 100).",
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) < 1:
				return errors.New("must provide a setting category")
			case len(args) < editArgsMin:
				return errors.New("must provide a setting name")
			case len(args) < editArgsMax:
				return errors.New("must provide a setting value")
			case len(args) > editArgsMax:
				return errors.New(
					"must only provide a setting category, name and value. If the value is a string, please use quotes",
				)
			}

			category := args[0]
			name := args[1]
			value := args[2]

			if err := checkDBEditCategory(category); err != nil {
				return err
			}

			err := settings.UpdateWithVerification(category, name, value)
			if err != nil {
				err := util.HumanizeError(err, "Error updating setting")

				return err.Humanized()
			}

			return nil
		},
	}

	return cmd
}

// dbLoggingCategory is the database settings category that phenix settings db
// edit refuses: phenix reads the log settings from its runtime settings.
const dbLoggingCategory = "Logging"

// errDBCategory is returned for a category that phenix settings db edit does
// not change.
var errDBCategory = errors.New("unknown setting category")

// checkDBEditCategory refuses a category that phenix settings db edit does not
// change, and names the categories that it changes.
// completeDBEditCategories completes the categories of the database settings
// that phenix settings db edit changes.
func completeDBEditCategories(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var categories []string

	for _, c := range dbSettingCategories() {
		if checkDBEditCategory(c) == nil {
			categories = append(categories, c)
		}
	}

	return completionMatches(categories, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeDBSettingNames completes the names of the database settings of the
// category in the first argument.
func completeDBSettingNames(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var names []string

	for _, s := range settings.DefaultSettings {
		if len(args) > 0 && s.Category == args[0] {
			names = append(names, s.Name)
		}
	}

	return completionMatches(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func checkDBEditCategory(category string) error {
	var valid []string

	for _, s := range settings.DefaultSettings {
		if s.Category != dbLoggingCategory && !slices.Contains(valid, s.Category) {
			valid = append(valid, s.Category)
		}
	}

	if slices.Contains(valid, category) {
		return nil
	}

	if category == dbLoggingCategory {
		return fmt.Errorf(
			"%w %s: a change to the Logging database settings has no effect. Use 'phenix settings set' "+
				"(for example, phenix settings set log.system.max-size 100). The categories are %s",
			errDBCategory, category, strings.Join(valid, ", "),
		)
	}

	return fmt.Errorf("%w %s: the categories are %s", errDBCategory, category, strings.Join(valid, ", "))
}

func newSettingsSetCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long: "Sets a configuration value in the YAML config file phenix read, or, when it read none, in " +
			"$HOME/.config/phenix/config.yaml (/etc/phenix/config.yaml as root), creating it. " +
			"A flag or PHENIX_ environment variable for the key still overrides the file. " +
			"The value of a phenix setting must have the type of its flag, such as an integer for " +
			"log.system.max-size; a string setting keeps the text as it is. For other keys, true or false " +
			"becomes a boolean and a number becomes a number. Comments and the other keys in the file stay.",
		Example: `
  phenix settings set log.level debug
  phenix settings set log.system.max-size 200`,
		Args:              cobra.ExactArgs(setArgs),
		ValidArgsFunction: settingsKeyCompletion(v),
		RunE: func(cmd *cobra.Command, args []string) error {
			change, err := runtimeSettings(cmd.Root(), v).Set(args[0], args[1])
			if err != nil {
				return err //nolint:wrapcheck // the setting's error as it is
			}

			plog.Info(plog.TypeSystem, "configuration updated", "file", change.File)

			return nil
		},
	}

	return cmd
}

// runtimeSettings returns the runtime settings of v, whose flags are those of
// root, as phenix settings and phenix ui change them: in settingsFile.
func runtimeSettings(root *cobra.Command, v *viper.Viper) *runtimesettings.Settings {
	return runtimesettings.New(
		v,
		runtimesettings.WithFlags(func(key string) *pflag.Flag { return settingFlag(root, v, key) }),
		runtimesettings.WithFile(func() string {
			uid, home := getCurrentUserInfo()

			return settingsFile(v.ConfigFileUsed(), uid, home)
		}),
	)
}

// settingsFile is the config file phenix settings set and unset change: used,
// the file phenix read, or else config.yaml in the user's config directory.
func settingsFile(used, uid, home string) string {
	if used != "" {
		return used
	}

	if uid == "0" {
		return filepath.Join(configDir, "config.yaml")
	}

	return filepath.Join(home, ".config", appName, "config.yaml")
}

func newSettingsListCmd(v *viper.Viper) *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   commandList,
		Short: "List all runtime settings",
		Args:  argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := v.AllSettings()

			if filter := mustGet(cmd.Flags().GetString, "filter"); filter != "" {
				flat := make(map[string]any)
				runtimesettings.Flatten("", settings, flat)

				filtered := make(map[string]any)
				for k, v := range flat {
					if strings.HasPrefix(k, strings.ToLower(filter)) {
						filtered[k] = v
					}
				}
				settings = filtered
			}

			return out.print(cmd, settings, outputViews{
				formatTable: func(w io.Writer) error {
					printer.PrintTableOfRuntimeSettings(w, settings)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(
		cmd, formatTable, []string{formatTable, FormatJSON, FormatYAML}, withJSONFlag(), withFormatFlag(),
	)
	cmd.Flags().String("filter", "", "Filter settings by key prefix")
	_ = cmd.RegisterFlagCompletionFunc("filter", completeSettingsKeys(v))

	return cmd
}

// errSettingNotFound is the error of phenix settings get for a key that has no value.
var errSettingNotFound = errors.New("setting not found")

func newSettingsGetCmd(v *viper.Viper) *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get configuration value(s)",
		Long:  "Gets a configuration value from the runtime configuration. If no key is provided, returns all settings.",
		Example: `
  phenix settings get
  phenix settings get log.level
  phenix settings get log -o json`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: settingsKeyCompletion(v),
		RunE: func(cmd *cobra.Command, args []string) error {
			var val any

			if len(args) == 0 {
				val = v.AllSettings()
			} else {
				key := args[0]
				val = v.Get(key)
				if val == nil {
					return errSettingNotFound
				}
			}

			return out.print(cmd, val, settingsGetViews(val, args))
		},
	}

	out = addOutputFlag(
		cmd, FormatYAML, []string{formatTable, FormatJSON, FormatYAML}, withJSONFlag(), withFormatFlag(),
	)

	return cmd
}

// settingsGetViews returns the table view of the value of phenix settings get,
// and a YAML view that prints a value that is not a map or a list as it is.
func settingsGetViews(val any, args []string) outputViews {
	views := outputViews{
		formatTable: func(w io.Writer) error {
			m, ok := val.(map[string]any)
			if !ok {
				k := "value"
				if len(args) > 0 {
					k = args[0]
				}

				m = map[string]any{k: val}
			}

			printer.PrintTableOfRuntimeSettings(w, m)

			return nil
		},
	}

	// a list from a flag, such as ui.users, is a []string, not a []any
	switch reflect.ValueOf(val).Kind() { //nolint:exhaustive // other kinds print as they are
	case reflect.Map, reflect.Slice, reflect.Array:
	default:
		views[FormatYAML] = func(w io.Writer) error {
			_, err := fmt.Fprintln(w, val)

			return err //nolint:wrapcheck // the writer's own error
		}
	}

	return views
}

func newSettingsUnsetCmd(v *viper.Viper) *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "unset [key]",
		Short: "Unset configuration value(s)",
		Long: "Unsets a configuration value in the config file phenix read, so a PHENIX_ environment variable " +
			"or the default applies. Comments and the other keys in the file stay. A key that is not in the " +
			"file does not change the file. Use --all to reset all settings. With --dry-run, phenix shows " +
			"the key that it would remove, or the config file that it would delete, and changes nothing.",
		Example: `
  phenix settings unset log.level
  phenix settings unset log.level --dry-run
  phenix settings unset --all`,
		ValidArgsFunction: settingsKeyCompletion(v),
		RunE: func(cmd *cobra.Command, args []string) error {
			all := mustGet(cmd.Flags().GetBool, "all")

			if !all && len(args) == 0 {
				return errors.New("must provide a configuration key to unset, or use --all")
			}

			if !all && len(args) != 1 {
				return fmt.Errorf("accepts 1 arg(s), received %d", len(args))
			}

			if all && len(args) > 0 {
				return errors.New("cannot use --all with arguments")
			}

			var key string
			if !all {
				key = args[0]
			}

			if !all {
				var opts []runtimesettings.UnsetOption
				if dry.enabled() {
					opts = append(opts, runtimesettings.UnsetDryRun())
				}

				change, err := runtimeSettings(cmd.Root(), v).Unset(key, opts...)
				if err != nil {
					return err //nolint:wrapcheck // the setting's error as it is
				}

				if change.Changed && dry.skip("unset", "setting "+key+" in "+change.File) {
					return nil
				}

				if !change.Changed {
					plog.Info(
						plog.TypeSystem, "configuration value not set in the config file; the file did not change",
						"key", key, "file", change.File,
					)

					return nil
				}

				plog.Info(plog.TypeSystem, "configuration value unset", "key", key, "file", change.File)

				return nil
			}

			uid, home := getCurrentUserInfo()
			configFile := settingsFile(v.ConfigFileUsed(), uid, home)

			if _, err := os.Stat(configFile); os.IsNotExist(err) {
				return runtimesettings.ErrNoConfigFile
			}

			if dry.skip("delete", "config file "+configFile) {
				return nil
			}

			if err := os.Remove(configFile); err != nil {
				return fmt.Errorf("failed to remove config file %s: %w", configFile, err)
			}

			plog.Info(
				plog.TypeSystem,
				"configuration file deleted; settings reset to defaults",
				"file",
				configFile,
			)

			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Unset all settings (delete configuration file)")
	dry = addDryRunFlag(cmd)

	return cmd
}

// settingFlag returns the flag of a known setting, or nil for another key:
// a persistent flag of root, the flag <name> of the subcommand <command> for
// the key <command>.<name>, or a flag of a subcommand that v has at its own
// name, such as unix-socket-gid.
func settingFlag(root *cobra.Command, v *viper.Viper, key string) *pflag.Flag {
	key = strings.ToLower(key)

	if f := root.PersistentFlags().Lookup(key); f != nil {
		return f
	}

	if command, name, ok := strings.Cut(key, "."); ok {
		for _, sub := range root.Commands() {
			if sub.Name() == command {
				if f := sub.LocalFlags().Lookup(name); f != nil {
					return f
				}
			}
		}
	}

	if !slices.Contains(v.AllKeys(), key) {
		return nil
	}

	for _, sub := range root.Commands() {
		if f := sub.LocalFlags().Lookup(key); f != nil {
			return f
		}
	}

	return nil
}

// newSettingsCommands returns phenix settings and its subcommands, which read
// and change the settings of v.
func newSettingsCommands(v *viper.Viper) *cobra.Command {
	settingsCmd := newSettingsCmd()

	dbCmd := newSettingsDBCmd()
	dbCmd.AddCommand(newSettingsDBListCmd())
	dbCmd.AddCommand(newSettingsDBEditCmd())
	settingsCmd.AddCommand(dbCmd)

	settingsCmd.AddCommand(newSettingsSetCmd(v))
	settingsCmd.AddCommand(newSettingsListCmd(v))
	settingsCmd.AddCommand(newSettingsGetCmd(v))
	settingsCmd.AddCommand(newSettingsUnsetCmd(v))

	return settingsCmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newSettingsCommands(viper.GetViper()), true)
}
