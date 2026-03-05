package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

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
	keyParts    = 2
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
	cmd := &cobra.Command{
		Use:   "list [category]",
		Short: "List settings in the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := settings.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to print a table")

				return err.Humanized()
			}

			if len(args) > 0 {
				category := args[0]

				var filtered []types.Setting

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

			if MustGetBool(cmd.Flags(), "json") {
				b, err := json.MarshalIndent(s, "", "  ")
				if err != nil {
					return err
				}

				fmt.Fprintln(os.Stdout, string(b))

				return nil
			}

			printer.PrintTableOfSettings(os.Stdout, s)

			return nil
		},
	}

	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

func newSettingsDBEditCmd() *cobra.Command {
	example := `
  phenix settings db edit <category> <name> <newValue>
  phenix settings db edit Password MinLength 20`

	cmd := &cobra.Command{
		Use:     "edit",
		Short:   "Edit a setting in the database",
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

			if category == "Logging" {
				plog.Warn(
					plog.TypeSystem,
					"Logging settings via 'phenix settings db edit' are deprecated and will be ignored. Please use 'phenix settings set' (e.g. phenix settings set log.system.max-size 100).",
				)
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

func settingsKeyCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	settings := getEffectiveSettings()
	flat := make(map[string]any)
	flattenSettingsMap("", settings, flat)

	var keys []string
	for k := range flat {
		if strings.HasPrefix(k, toComplete) {
			keys = append(keys, k)
		}
	}
	return keys, cobra.ShellCompDirectiveNoFileComp
}

func newSettingsSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long:  "Sets a configuration value in the config file. If the file does not exist, it will be created.",
		Example: `
  phenix settings set log.level debug
  phenix settings set log.system.max-size 200`,
		Args:              cobra.ExactArgs(setArgs),
		ValidArgsFunction: settingsKeyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			val := inferType(args[1])

			configFile := viper.ConfigFileUsed()
			if configFile == "" {
				uid, home := getCurrentUserInfo()
				targetDir := "/etc/phenix"

				if uid != "0" {
					targetDir = filepath.Join(home, ".config", "phenix")
				}

				err := os.MkdirAll(targetDir, 0o750)
				if err != nil {
					return fmt.Errorf("creating config directory: %w", err)
				}

				configFile = filepath.Join(targetDir, "config.yaml")
			}

			// Read existing config file
			data, err := os.ReadFile(configFile)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("reading config file: %w", err)
			}

			configMap := make(map[string]any)
			if len(data) > 0 {
				err := yaml.Unmarshal(data, &configMap)
				if err != nil {
					return fmt.Errorf("parsing config file: %w", err)
				}
			}

			// Helper to set nested keys
			var setKey func(m map[string]any, k string, v any)

			setKey = func(m map[string]any, k string, v any) {
				parts := strings.SplitN(k, ".", keyParts)
				target := parts[0]

				// Find existing key with case-insensitive match
				for mk := range m {
					if strings.EqualFold(mk, target) {
						target = mk
						break
					}
				}

				if len(parts) == 1 {
					m[target] = v

					return
				}

				if _, ok := m[target]; !ok {
					m[target] = make(map[string]any)
				}

				if next, ok := m[target].(map[string]any); ok {
					setKey(next, parts[1], v)
				} else {
					// Overwrite if it's not a map (e.g. changing a leaf to a branch, which shouldn't happen in valid config but good for robustness)
					next := make(map[string]any)
					m[target] = next
					setKey(next, parts[1], v)
				}
			}

			setKey(configMap, key, val)

			newData, err := yaml.Marshal(configMap)
			if err != nil {
				return fmt.Errorf("marshaling config: %w", err)
			}

			// Write back to file
			if err := os.WriteFile(configFile, newData, 0o600); err != nil {
				return fmt.Errorf("writing config file: %w", err)
			}

			plog.Info(plog.TypeSystem, "configuration updated", "file", configFile)

			return nil
		},
	}

	return cmd
}

func newSettingsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all runtime settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := getEffectiveSettings()

			if filter := MustGetString(cmd.Flags(), "filter"); filter != "" {
				flat := make(map[string]any)
				flattenSettingsMap("", settings, flat)

				filtered := make(map[string]any)
				for k, v := range flat {
					if strings.HasPrefix(k, strings.ToLower(filter)) {
						filtered[k] = v
					}
				}
				settings = filtered
			}

			format := MustGetString(cmd.Flags(), "format")

			if MustGetBool(cmd.Flags(), "json") {
				format = FormatJSON
			}

			switch format {
			case FormatJSON:
				b, err := json.MarshalIndent(settings, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, string(b))
			case FormatYAML:
				b, err := yaml.Marshal(settings)
				if err != nil {
					return err
				}
				fmt.Fprint(os.Stdout, string(b))
			case "table":
				printer.PrintTableOfRuntimeSettings(os.Stdout, settings)
			default:
				return fmt.Errorf("unsupported output format: %s", format)
			}

			return nil
		},
	}

	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.Flags().StringP("format", "f", "table", "Output format (table, "+FormatJSON+", "+FormatYAML+")")
	cmd.Flags().String("filter", "", "Filter settings by key prefix")

	return cmd
}

func newSettingsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get configuration value(s)",
		Long:  "Gets a configuration value from the runtime configuration. If no key is provided, returns all settings.",
		Example: `
  phenix settings get
  phenix settings get log.level`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: settingsKeyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var val any
			outputFormat := MustGetString(cmd.Flags(), "format")

			if len(args) == 0 {
				val = getEffectiveSettings()
			} else {
				key := args[0]
				val = getEffectiveValue(key)
				if val == nil {
					return errors.New("setting not found")
				}
			}

			if MustGetBool(cmd.Flags(), "json") {
				outputFormat = FormatJSON
			}

			switch outputFormat {
			case FormatJSON:
				b, err := json.MarshalIndent(val, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout, string(b))
			case "table":
				var m map[string]any
				if v, ok := val.(map[string]any); ok {
					m = v
				} else {
					k := "value"
					if len(args) > 0 {
						k = args[0]
					}
					m = map[string]any{k: val}
				}
				printer.PrintTableOfRuntimeSettings(os.Stdout, m)
			case FormatYAML:
				switch v := val.(type) {
				case map[string]any, []any:
					b, err := yaml.Marshal(v)
					if err != nil {
						return err
					}
					fmt.Fprint(os.Stdout, string(b))
				default:
					fmt.Fprintln(os.Stdout, v)
				}
			default:
				return fmt.Errorf("unsupported output format: %s", outputFormat)
			}

			return nil
		},
	}

	cmd.Flags().StringP("format", "f", FormatYAML, "Output format (table, "+FormatJSON+", "+FormatYAML+")")
	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}

//nolint:funlen // command definition
func newSettingsUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset [key]",
		Short: "Unset configuration value(s)",
		Long:  "Unsets a configuration value in the config file, reverting it to the default. Use --all to reset all settings.",
		Example: `
  phenix settings unset log.level
  phenix settings unset --all`,
		ValidArgsFunction: settingsKeyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			all := MustGetBool(cmd.Flags(), "all")

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

			configFile := viper.ConfigFileUsed()
			if configFile == "" {
				uid, home := getCurrentUserInfo()
				targetDir := "/etc/phenix"

				if uid != "0" {
					targetDir = filepath.Join(home, ".config", "phenix")
				}

				configFile = filepath.Join(targetDir, "config.yaml")

				if _, err := os.Stat(configFile); os.IsNotExist(err) {
					return errors.New("no configuration file found")
				}
			}

			if all {
				err := os.Remove(configFile)
				if err != nil {
					return fmt.Errorf("failed to remove config file %s: %w", configFile, err)
				}

				plog.Info(
					plog.TypeSystem,
					"configuration file deleted; settings reset to defaults",
					"file",
					configFile,
				)

				return nil
			}

			data, err := os.ReadFile(configFile)
			if err != nil {
				return fmt.Errorf("reading config file: %w", err)
			}

			var configMap map[string]any
			if err := yaml.Unmarshal(data, &configMap); err != nil {
				return fmt.Errorf("parsing config file: %w", err)
			}

			var deleteKey func(m map[string]any, k string)

			deleteKey = func(m map[string]any, k string) {
				parts := strings.SplitN(k, ".", keyParts)
				target := parts[0]

				var actualKey string

				for mk := range m {
					if strings.EqualFold(mk, target) {
						actualKey = mk

						break
					}
				}

				if actualKey == "" {
					return
				}

				if len(parts) == 1 {
					delete(m, actualKey)

					return
				}

				if next, ok := m[actualKey].(map[string]any); ok {
					deleteKey(next, parts[1])
				}
			}

			deleteKey(configMap, key)

			newData, err := yaml.Marshal(configMap)
			if err != nil {
				return fmt.Errorf("marshaling config: %w", err)
			}

			if err := os.WriteFile(configFile, newData, 0o600); err != nil {
				return fmt.Errorf("writing config file: %w", err)
			}

			plog.Info(plog.TypeSystem, "configuration value unset", "key", key, "file", configFile)

			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Unset all settings (delete configuration file)")

	return cmd
}

func flattenSettingsMap(prefix string, src map[string]any, dst map[string]any) {
	for k, v := range src {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			flattenSettingsMap(newKey, child, dst)
		} else {
			dst[newKey] = v
		}
	}
}

func init() { //nolint:gochecknoinits // cobra command
	settingsCmd := newSettingsCmd()

	dbCmd := newSettingsDBCmd()
	dbCmd.AddCommand(newSettingsDBListCmd())
	dbCmd.AddCommand(newSettingsDBEditCmd())
	settingsCmd.AddCommand(dbCmd)

	settingsCmd.AddCommand(newSettingsSetCmd())
	settingsCmd.AddCommand(newSettingsListCmd())
	settingsCmd.AddCommand(newSettingsGetCmd())
	settingsCmd.AddCommand(newSettingsUnsetCmd())
	rootCmd.AddCommand(settingsCmd)
}

func inferType(s string) any {
	// Check for boolean
	if strings.ToLower(s) == "true" {
		return true
	}

	if strings.ToLower(s) == "false" {
		return false
	}

	// Check for integer
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}

	// Check for float
	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.Contains(s, ".") {
		return f
	}

	return s
}
