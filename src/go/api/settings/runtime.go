package settings

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	runtimeConfigDir = "/etc/phenix"
	runtimeAppName   = "phenix"
	runtimeKeyParts  = 2
	runtimeBoolTrue  = "true"
	sourceConfig     = "config"
	sourceEnv        = "env"
	sourceDefault    = "default"
)

type RuntimeSettings struct {
	Settings    []RuntimeSetting     `json:"settings"`
	Environment []RuntimeEnvironment `json:"environment"`
}

type RuntimeSetting struct {
	Key             string `json:"key"`
	Value           any    `json:"value"`
	Type            string `json:"type"`
	EnvVar          string `json:"env_var"`
	Source          string `json:"source"`
	Description     string `json:"description"`
	RestartRequired bool   `json:"restart_required"`
	Sensitive       bool   `json:"sensitive"`
}

type RuntimeEnvironment struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Set       bool   `json:"set"`
	Sensitive bool   `json:"sensitive"`
}

type RuntimeSettingUpdate struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type runtimeSettingInfo struct {
	Type            string
	Description     string
	RestartRequired bool
	Sensitive       bool
}

var runtimeSettingInfoByKey = map[string]runtimeSettingInfo{ //nolint:gochecknoglobals // static metadata
	"base-dir.minimega":       {"string", "Base minimega directory.", true, false},
	"base-dir.phenix":         {"string", "Base phēnix data directory.", true, false},
	"bridge-mode":             {"string", "Bridge naming mode for experiments.", false, false},
	"deploy-mode":             {"string", "Minimega VM deployment mode.", false, false},
	"hostname-suffixes":       {"string", "Hostname suffixes to strip.", false, false},
	"log.console":             {"string", "Console log destination.", false, false},
	"log.level":               {"string", "Global log verbosity.", false, false},
	"log.system.max-age":      {"int", "Maximum age in days for rotated system logs.", false, false},
	"log.system.max-backups":  {"int", "Maximum retained system log backups.", false, false},
	"log.system.max-size":     {"int", "Maximum system log size in MiB before rotation.", false, false},
	"log.system.path":         {"string", "Persistent JSON system log path.", false, false},
	"mount-dir":               {"string", "Base directory for VM filesystem mounts.", true, false},
	"store.endpoint":          {"string", "Data store endpoint.", true, false},
	"ui.base-path":            {"string", "Base path for the web UI.", true, false},
	"ui.features":             {"[]string", "Enabled optional UI features.", true, false},
	"ui.file-server-endpoint": {"string", "Experiment file upload endpoint.", true, false},
	"ui.jwt-lifetime":         {"duration", "Lifetime of JWT authentication tokens.", true, false},
	"ui.jwt-signing-key":      {"string", "Secret key used to sign JWTs.", true, true},
	"ui.listen-endpoint":      {"string", "Web UI listen endpoint.", true, false},
	"ui.logs.level":           {"string", "Log level published to the UI stream.", false, false},
	"ui.logs.minimega-path":   {"string", "Minimega log path published to the UI.", true, false},
	"ui.minimega-console":     {"bool", "Enable minimega console access in the UI.", true, false},
	"ui.proxy-auth-header":    {"string", "Proxy authentication username header.", true, false},
	"ui.tls-cert":             {"string", "TLS certificate path.", true, true},
	"ui.tls-key":              {"string", "TLS private key path.", true, true},
	"ui.users":                {"[]string", "Initial UI users.", true, true},
	"unix-socket":             {"string", "phēnix Unix socket path.", true, false},
	"unix-socket-gid":         {"int", "Group ID allowed to write to the Unix socket.", true, false},
	"use-gre-mesh":            {"bool", "Use GRE tunnels between mesh nodes for VLAN trunking.", false, false},
}

var (
	runtimeFlagOverrideMu sync.RWMutex       //nolint:gochecknoglobals // process-local flag state
	runtimeFlagOverrides  = map[string]any{} //nolint:gochecknoglobals // process-local flag state
)

func GetRuntimeSettings() RuntimeSettings {
	effective := EffectiveRuntimeSettings()
	flat := make(map[string]any)
	FlattenSettingsMap("", effective, flat)

	keys := make([]string, 0, len(flat))
	for key := range flat {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	settings := make([]RuntimeSetting, 0, len(keys))
	for _, key := range keys {
		info := runtimeSettingInfoByKey[key]
		settings = append(settings, RuntimeSetting{
			Key:             key,
			Value:           flat[key],
			Type:            runtimeSettingType(key, flat[key], info.Type),
			EnvVar:          EnvVarForRuntimeKey(key),
			Source:          RuntimeSettingSource(key),
			Description:     info.Description,
			RestartRequired: info.RestartRequired,
			Sensitive:       info.Sensitive || isSensitiveName(key),
		})
	}

	return RuntimeSettings{
		Settings:    settings,
		Environment: GetRuntimeEnvironment(),
	}
}

func EffectiveRuntimeSettings() map[string]any {
	base := viper.AllSettings()
	vMerge := viper.New()
	_ = vMerge.MergeConfigMap(base)

	if vFile := fileViper(); vFile != nil {
		_ = vMerge.MergeConfigMap(vFile.AllSettings())
	}

	merged := vMerge.AllSettings()
	for key, value := range RuntimeFlagOverrides() {
		setNestedKey(merged, key, value)
	}

	return merged
}

func RuntimeSettingSource(key string) string {
	if _, ok := RuntimeFlagOverrides()[key]; ok {
		return "flag"
	}
	if v := fileViper(); v != nil && v.IsSet(key) {
		return sourceConfig
	}
	if _, ok := os.LookupEnv(EnvVarForRuntimeKey(key)); ok {
		return sourceEnv
	}
	return sourceDefault
}

func RegisterRuntimeFlagOverride(key string, value any) {
	runtimeFlagOverrideMu.Lock()
	defer runtimeFlagOverrideMu.Unlock()

	runtimeFlagOverrides[key] = value
}

func RuntimeFlagOverrides() map[string]any {
	runtimeFlagOverrideMu.RLock()
	defer runtimeFlagOverrideMu.RUnlock()

	overrides := make(map[string]any, len(runtimeFlagOverrides))
	maps.Copy(overrides, runtimeFlagOverrides)

	return overrides
}

func IsRuntimeSettingKey(key string) bool {
	_, ok := runtimeSettingInfoByKey[key]
	return ok
}

func SetRuntimeSetting(update RuntimeSettingUpdate) error {
	if strings.TrimSpace(update.Key) == "" {
		return errors.New("runtime setting key cannot be empty")
	}

	return writeRuntimeSetting(update.Key, update.Value)
}

func UnsetRuntimeSetting(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("runtime setting key cannot be empty")
	}

	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		configFile = defaultRuntimeConfigFile()
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			return errors.New("no configuration file found")
		}
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	configMap := make(map[string]any)
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &configMap); err != nil {
			return fmt.Errorf("parsing config file: %w", err)
		}
	}

	deleteNestedKey(configMap, key)

	newData, err := yaml.Marshal(configMap)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(configFile, newData, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

func GetRuntimeEnvironment() []RuntimeEnvironment {
	envNames := map[string]struct{}{}
	for key := range runtimeSettingInfoByKey {
		envNames[EnvVarForRuntimeKey(key)] = struct{}{}
	}
	for _, env := range os.Environ() {
		name, _, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "PHENIX_") || strings.HasPrefix(name, "MM_") {
			envNames[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(envNames))
	for name := range envNames {
		names = append(names, name)
	}
	slices.Sort(names)

	environment := make([]RuntimeEnvironment, 0, len(names))
	for _, name := range names {
		value, set := os.LookupEnv(name)
		sensitive := isSensitiveName(name)
		if sensitive && value != "" {
			value = "********"
		}
		environment = append(environment, RuntimeEnvironment{
			Name:      name,
			Value:     value,
			Set:       set,
			Sensitive: sensitive,
		})
	}

	return environment
}

func EnvVarForRuntimeKey(key string) string {
	name := strings.NewReplacer("-", "_", ".", "_").Replace(key)
	return "PHENIX_" + strings.ToUpper(name)
}

func FlattenSettingsMap(prefix string, src map[string]any, dst map[string]any) {
	for k, v := range src {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			FlattenSettingsMap(newKey, child, dst)
		} else {
			dst[newKey] = v
		}
	}
}

func InferRuntimeValue(s string) any {
	if strings.EqualFold(s, runtimeBoolTrue) {
		return true
	}
	if strings.EqualFold(s, "false") {
		return false
	}

	if strings.Contains(s, ",") {
		return strings.Split(s, ",")
	}

	if i, err := strconv.Atoi(s); err == nil {
		return i
	}

	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.Contains(s, ".") {
		return f
	}

	return s
}

func fileViper() *viper.Viper {
	f := viper.ConfigFileUsed()
	if f == "" {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(f)
	_ = v.ReadInConfig()

	return v
}

func writeRuntimeSetting(key string, value any) error {
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		configFile = defaultRuntimeConfigFile()
		if err := os.MkdirAll(filepath.Dir(configFile), 0o750); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
	}

	data, err := os.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}

	configMap := make(map[string]any)
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &configMap); err != nil {
			return fmt.Errorf("parsing config file: %w", err)
		}
	}

	setNestedKey(configMap, key, value)

	newData, err := yaml.Marshal(configMap)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(configFile, newData, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

func setNestedKey(m map[string]any, key string, value any) {
	parts := strings.SplitN(key, ".", runtimeKeyParts)
	target := matchingKey(m, parts[0])
	if target == "" {
		target = parts[0]
	}

	if len(parts) == 1 {
		m[target] = value
		return
	}

	next, ok := m[target].(map[string]any)
	if !ok {
		next = make(map[string]any)
		m[target] = next
	}

	setNestedKey(next, parts[1], value)
}

func deleteNestedKey(m map[string]any, key string) {
	parts := strings.SplitN(key, ".", runtimeKeyParts)
	target := matchingKey(m, parts[0])
	if target == "" {
		return
	}

	if len(parts) == 1 {
		delete(m, target)
		return
	}

	if next, ok := m[target].(map[string]any); ok {
		deleteNestedKey(next, parts[1])
	}
}

func matchingKey(m map[string]any, key string) string {
	for mk := range m {
		if strings.EqualFold(mk, key) {
			return mk
		}
	}

	return ""
}

func defaultRuntimeConfigFile() string {
	u, err := user.Current()
	if err != nil {
		return filepath.Join(runtimeConfigDir, "config.yaml")
	}

	if u.Uid == "0" {
		return filepath.Join(runtimeConfigDir, "config.yaml")
	}

	return filepath.Join(u.HomeDir, ".config", runtimeAppName, "config.yaml")
}

func runtimeSettingType(key string, value any, configured string) string {
	if configured != "" {
		return configured
	}

	switch value.(type) {
	case bool:
		return "bool"
	case int, int32, int64:
		return "int"
	case float32, float64:
		return "float64"
	case []any, []string:
		return "[]string"
	default:
		if strings.HasSuffix(key, "lifetime") {
			return "duration"
		}
		return "string"
	}
}

func isSensitiveName(name string) bool {
	upper := strings.ToUpper(name)

	return strings.Contains(upper, "JWT") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "TLS_KEY") ||
		strings.Contains(upper, "TLS_CERT") ||
		strings.Contains(upper, "UI_USERS")
}
