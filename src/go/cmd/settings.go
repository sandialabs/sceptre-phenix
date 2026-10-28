package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

			if mustGet(cmd.Flags().GetBool, "json") {
				b, err := json.MarshalIndent(s, "", "  ")
				if err != nil {
					return err
				}

				fmt.Fprintln(cmd.OutOrStdout(), string(b))

				return nil
			}

			printer.PrintTableOfSettings(cmd.OutOrStdout(), s)

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

// settingsKeyCompletion completes the first argument with the dotted keys of
// the settings in v.
func settingsKeyCompletion(v *viper.Viper) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		flat := make(map[string]any)
		flattenSettingsMap("", v.AllSettings(), flat)

		var keys []string
		for k := range flat {
			if strings.HasPrefix(k, toComplete) {
				keys = append(keys, k)
			}
		}

		return keys, cobra.ShellCompDirectiveNoFileComp
	}
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
			uid, home := getCurrentUserInfo()
			configFile := settingsFile(v.ConfigFileUsed(), uid, home)

			value, err := settingValue(cmd.Root(), v, args[0], args[1])
			if err != nil {
				return err
			}

			if err := setConfigValue(configFile, args[0], value); err != nil {
				return err
			}

			plog.Info(plog.TypeSystem, "configuration updated", "file", configFile)

			return nil
		},
	}

	return cmd
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

// errNotYAML is the error for a config file phenix settings cannot rewrite.
var errNotYAML = errors.New("phenix settings set and unset rewrite only YAML config files")

func checkYAMLConfigFile(path string) error {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		return nil
	}

	return fmt.Errorf("%w, so edit %s by hand", errNotYAML, path)
}

// errTextNotYAML is the error for text that yaml.v3 writes as YAML that reads
// back as other text, or that does not parse, such as text that starts with a
// newline or has a tab before a newline.
var errTextNotYAML = errors.New("phenix settings set cannot write this text to a YAML config file")

// setConfigValue sets key to val in the YAML config file path, creating the
// file and its directory when they do not exist. It changes only the YAML
// node of the key, so comments, key order and the other keys stay.
func setConfigValue(path, key string, val any) error {
	if err := checkYAMLConfigFile(path); err != nil {
		return err
	}

	texts, _ := val.([]string)
	if text, ok := val.(string); ok {
		texts = []string{text}
	}

	for _, text := range texts {
		var back map[string]string

		data, err := yaml.Marshal(map[string]string{"v": text})
		if err != nil || yaml.Unmarshal(data, &back) != nil || back["v"] != text {
			return fmt.Errorf("%w, so edit %s by hand: %q", errTextNotYAML, path, text)
		}
	}

	var node yaml.Node
	if err := node.Encode(val); err != nil {
		return fmt.Errorf("encoding config value: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	doc, err := parseConfigDoc(data)
	if err != nil {
		return err
	}

	setConfigNode(doc.root, strings.Split(key, "."), &node)

	return doc.write(path)
}

// configDoc is a YAML config file that phenix settings set and unset change
// in place.
type configDoc struct {
	data []byte     // the file as it was
	doc  *yaml.Node // the document node, or nil when data has no YAML content
	root *yaml.Node // the mapping at the top of the file
}

// errConfigNotMapping is the error for a config file that is not a YAML mapping.
var errConfigNotMapping = errors.New("config file is not a YAML mapping of settings")

// parseConfigDoc parses the YAML config file data. Data with no YAML content,
// such as only comments, or a null document, gives an empty mapping.
func parseConfigDoc(data []byte) (*configDoc, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &configDoc{data: data, doc: nil, root: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}, nil
	}

	top := doc.Content[0]

	switch {
	case top.Kind == yaml.MappingNode:
	case top.Kind == yaml.ScalarNode && top.Tag == "!!null":
		doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keepComments(doc.Content[0], top)
	default:
		return nil, fmt.Errorf("parsing config file: %w", errConfigNotMapping)
	}

	return &configDoc{data: data, doc: &doc, root: doc.Content[0]}, nil
}

// write writes the config to path. A file with no YAML content keeps its
// text, such as its comments, before the new settings.
func (c *configDoc) write(path string) error {
	var buf bytes.Buffer

	node := c.doc
	if node == nil {
		node = c.root

		if len(bytes.TrimSpace(c.data)) > 0 {
			buf.Write(c.data)

			if !bytes.HasSuffix(c.data, []byte("\n")) {
				buf.WriteByte('\n')
			}
		}
	}

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(configIndent(c.root))

	if err := enc.Encode(node); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

const (
	defaultConfigIndent = 4 // the indent of yaml.Marshal
	maxConfigIndent     = 9 // the largest indent yaml.v3 writes
	seqItemOffset       = 2 // the "- " before a sequence item
	nodePair            = 2 // a key node and its value node in a mapping
)

// configIndent returns the indent of the first nested block mapping or
// sequence in the mapping m, or defaultConfigIndent when it has none.
func configIndent(m *yaml.Node) int {
	for i := 0; i+1 < len(m.Content); i += nodePair {
		key, value := m.Content[i], m.Content[i+1]

		if value.Style&yaml.FlowStyle != 0 || len(value.Content) == 0 {
			continue
		}

		indent := 0

		switch value.Kind { //nolint:exhaustive // only block collections show the indent
		case yaml.MappingNode:
			indent = value.Content[0].Column - key.Column
		case yaml.SequenceNode:
			indent = value.Content[0].Column - key.Column - seqItemOffset
		}

		if indent >= seqItemOffset && indent <= maxConfigIndent {
			return indent
		}

		if value.Kind == yaml.MappingNode {
			if nested := configIndent(value); nested != defaultConfigIndent {
				return nested
			}
		}
	}

	return defaultConfigIndent
}

// configKeyIndex returns the index in m.Content of the key that matches key
// without regard to case, or -1 when m has no such key.
func configKeyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += nodePair {
		if m.Content[i].Kind == yaml.ScalarNode && strings.EqualFold(m.Content[i].Value, key) {
			return i
		}
	}

	return -1
}

// copyConfigNode returns a deep copy of n without anchors, so a change to
// the copy of an alias does not change the anchor.
func copyConfigNode(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return copyConfigNode(n.Alias)
	}

	c := *n
	c.Anchor = ""
	c.Content = make([]*yaml.Node, len(n.Content))

	for i, child := range n.Content {
		c.Content[i] = copyConfigNode(child)
	}

	return &c
}

// keepComments gives to the comments of from, the node that to replaces.
func keepComments(to, from *yaml.Node) {
	to.HeadComment, to.LineComment, to.FootComment = from.HeadComment, from.LineComment, from.FootComment
}

// setConfigNode sets the value at the dotted key parts in the mapping m to
// value. It appends a missing key at the end of its mapping, and replaces a
// value on the path that is not a mapping with a mapping. An empty mapping
// on the path, such as "{}", becomes a block mapping.
func setConfigNode(m *yaml.Node, parts []string, value *yaml.Node) {
	for {
		if len(m.Content) == 0 {
			m.Style &^= yaml.FlowStyle
		}

		i := configKeyIndex(m, parts[0])

		if i < 0 {
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: parts[0]}
			next := value

			if len(parts) > 1 {
				next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}

			m.Content = append(m.Content, key, next)
			i = len(m.Content) - nodePair
		}

		old := m.Content[i+1]

		if len(parts) == 1 {
			if old != value {
				keepComments(value, old)
				m.Content[i+1] = value
			}

			return
		}

		next := old
		if next.Kind == yaml.AliasNode && next.Alias != nil && next.Alias.Kind == yaml.MappingNode {
			next = copyConfigNode(next)
			keepComments(next, old)
		}

		if next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keepComments(next, old)
		}

		m.Content[i+1] = next
		m, parts = next, parts[1:]
	}
}

// unsetConfigNode removes the dotted key parts from the mapping m, and each
// mapping that the removal leaves empty. It reports whether m had the key.
func unsetConfigNode(m *yaml.Node, parts []string) bool {
	i := configKeyIndex(m, parts[0])
	if i < 0 {
		return false
	}

	if len(parts) > 1 {
		old := m.Content[i+1]

		next := old
		if next.Kind == yaml.AliasNode && next.Alias != nil {
			next = copyConfigNode(next)
			keepComments(next, old)
		}

		if next.Kind != yaml.MappingNode || !unsetConfigNode(next, parts[1:]) {
			return false
		}

		m.Content[i+1] = next

		if len(next.Content) > 0 {
			return true
		}
	}

	m.Content = slices.Delete(m.Content, i, i+nodePair)

	return true
}

func newSettingsListCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandList,
		Short: "List all runtime settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := v.AllSettings()

			if filter := mustGet(cmd.Flags().GetString, "filter"); filter != "" {
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

			format := mustGet(cmd.Flags().GetString, "format")

			if mustGet(cmd.Flags().GetBool, "json") {
				format = FormatJSON
			}

			switch format {
			case FormatJSON:
				b, err := json.MarshalIndent(settings, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			case FormatYAML:
				b, err := yaml.Marshal(settings)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), string(b))
			case "table":
				printer.PrintTableOfRuntimeSettings(cmd.OutOrStdout(), settings)
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

// errSettingNotFound is the error of phenix settings get for a key that has no value.
var errSettingNotFound = errors.New("setting not found")

func newSettingsGetCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get configuration value(s)",
		Long:  "Gets a configuration value from the runtime configuration. If no key is provided, returns all settings.",
		Example: `
  phenix settings get
  phenix settings get log.level`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: settingsKeyCompletion(v),
		RunE: func(cmd *cobra.Command, args []string) error {
			var val any
			outputFormat := mustGet(cmd.Flags().GetString, "format")

			if len(args) == 0 {
				val = v.AllSettings()
			} else {
				key := args[0]
				val = v.Get(key)
				if val == nil {
					return errSettingNotFound
				}
			}

			if mustGet(cmd.Flags().GetBool, "json") {
				outputFormat = FormatJSON
			}

			switch outputFormat {
			case FormatJSON:
				b, err := json.MarshalIndent(val, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
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
				printer.PrintTableOfRuntimeSettings(cmd.OutOrStdout(), m)
			case FormatYAML:
				// a list from a flag, such as ui.users, is a []string, not a []any
				switch reflect.ValueOf(val).Kind() { //nolint:exhaustive // other kinds print as they are
				case reflect.Map, reflect.Slice, reflect.Array:
					b, err := yaml.Marshal(val)
					if err != nil {
						return err
					}
					fmt.Fprint(cmd.OutOrStdout(), string(b))
				default:
					fmt.Fprintln(cmd.OutOrStdout(), val)
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

func newSettingsUnsetCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset [key]",
		Short: "Unset configuration value(s)",
		Long: "Unsets a configuration value in the config file phenix read, so a PHENIX_ environment variable " +
			"or the default applies. Comments and the other keys in the file stay. A key that is not in the " +
			"file does not change the file. Use --all to reset all settings.",
		Example: `
  phenix settings unset log.level
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

			uid, home := getCurrentUserInfo()
			configFile := settingsFile(v.ConfigFileUsed(), uid, home)

			if _, err := os.Stat(configFile); os.IsNotExist(err) {
				return errors.New("no configuration file found")
			}

			if err := checkYAMLConfigFile(configFile); !all && err != nil {
				return err
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

			doc, err := parseConfigDoc(data)
			if err != nil {
				return err
			}

			if !unsetConfigNode(doc.root, strings.Split(key, ".")) {
				plog.Info(
					plog.TypeSystem, "configuration value not set in the config file; the file did not change",
					"key", key, "file", configFile,
				)

				return nil
			}

			if err := doc.write(configFile); err != nil {
				return err
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

func inferType(s string) any {
	// Check for boolean
	if strings.ToLower(s) == boolStringTrue {
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

// errSettingType is the error for text that does not parse as the type of
// the setting phenix settings set changes.
var errSettingType = errors.New("value does not have the type of the setting")

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

// settingValue returns the value phenix settings set writes for text at key.
// For a known setting, it parses text as the flag of the setting does, and
// keeps a duration as its text. For another key, it uses inferType.
func settingValue(root *cobra.Command, v *viper.Viper, key, text string) (any, error) {
	f := settingFlag(root, v, key)
	if f == nil {
		return inferType(text), nil
	}

	typ := f.Value.Type()
	flags := pflag.NewFlagSet(key, pflag.ContinueOnError)

	var want string

	switch typ {
	case "string":
		return text, nil
	case "bool":
		flags.Bool(key, false, "")
		want = "true or false"
	case "int":
		flags.Int(key, 0, "")
		want = "an integer, such as 100"
	case "duration":
		flags.Duration(key, 0, "")
		want = "a duration, such as 90s or 24h"
	case "stringSlice":
		flags.StringSlice(key, nil, "")
		want = "a comma-separated list"
	default:
		return nil, fmt.Errorf(
			"%w: phenix settings set cannot write the %s setting %s, so edit the config file by hand",
			errSettingType, typ, key,
		)
	}

	if err := flags.Set(key, text); err != nil {
		return nil, fmt.Errorf("%w: %s is a %s setting and takes %s, not %q", errSettingType, key, typ, want, text)
	}

	switch typ {
	case "bool":
		return flags.GetBool(key)
	case "int":
		return flags.GetInt(key)
	case "stringSlice":
		return flags.GetStringSlice(key)
	}

	return text, nil
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
