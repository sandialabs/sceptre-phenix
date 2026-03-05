package settings

import (
	"fmt"
	"strconv"

	"github.com/activeshadow/structs"

	"phenix/store"
	"phenix/types"
	v2 "phenix/types/version/v2"
)

const (
	DefaultPasswordMinLength = 8
	DefaultLogMaxFileSize    = 100
	DefaultLogMaxFileAge     = 90
)

var DefaultSettings = []v2.Setting{ //nolint:gochecknoglobals // global constant
	{
		Category: "Password",
		Name:     "NumberReq",
		Type:     v2.SettingValueBool,
		Value:    strconv.FormatBool(true),
	},
	{
		Category: "Password",
		Name:     "SymbolReq",
		Type:     v2.SettingValueBool,
		Value:    strconv.FormatBool(true),
	},
	{
		Category: "Password",
		Name:     "LowercaseReq",
		Type:     v2.SettingValueBool,
		Value:    strconv.FormatBool(true),
	},
	{
		Category: "Password",
		Name:     "UppercaseReq",
		Type:     v2.SettingValueBool,
		Value:    strconv.FormatBool(false),
	},
	{Category: "Password", Name: "MinLength", Type: v2.SettingValueInt, Value: formatInt(DefaultPasswordMinLength)},

	{Category: "Logging", Name: "MaxFileRotations", Type: v2.SettingValueInt, Value: formatInt(0)},
	{Category: "Logging", Name: "MaxFileSize", Type: v2.SettingValueInt, Value: formatInt(DefaultLogMaxFileSize)},
	{Category: "Logging", Name: "MaxFileAge", Type: v2.SettingValueInt, Value: formatInt(DefaultLogMaxFileAge)},
}

func GetDefault(category, name string) (v2.Setting, bool) {
	for _, setting := range DefaultSettings {
		if setting.Category == category && setting.Name == name {
			return setting, true
		}
	}

	return v2.Setting{}, false
}

func SetDefaults() error {
	for _, spec := range DefaultSettings {
		// create ignores if the setting exists already
		settingName := GetStoreName(spec.Category, spec.Name)

		c := store.Config{ //nolint:exhaustruct // partial initialization
			Version: "phenix.sandia.gov/v2",
			Kind:    "Setting",
			Metadata: store.ConfigMetadata{ //nolint:exhaustruct // partial initialization
				Name: settingName,
			},
			Spec: structs.MapDefaultCase(&spec, structs.CASESNAKE),
		}

		err := store.Create(&c)
		if err != nil {
			return fmt.Errorf("storing setting %s: %w", settingName, err)
		}
	}

	return nil
}

func setMissingDefaults(existing []types.Setting) ([]types.Setting, error) {
	var fullSettings []types.Setting

	fullSettings = append(fullSettings, existing...)

	for _, spec := range DefaultSettings {
		if existsAlready(spec.Name, spec.Category, existing) {
			continue
		}

		settingName := GetStoreName(spec.Category, spec.Name)

		c := store.Config{ //nolint:exhaustruct // partial initialization
			Version: "phenix.sandia.gov/v2",
			Kind:    "Setting",
			Metadata: store.ConfigMetadata{ //nolint:exhaustruct // partial initialization
				Name: settingName,
			},
			Spec: structs.MapDefaultCase(&spec, structs.CASESNAKE),
		}

		err := store.Create(&c)
		if err != nil {
			return nil, fmt.Errorf("storing setting %s: %w", settingName, err)
		}

		newSetting := types.Setting{Metadata: c.Metadata, Spec: &spec}

		fullSettings = append(fullSettings, newSetting)
	}

	return fullSettings, nil
}

func existsAlready(name, category string, existing []types.Setting) bool {
	for _, exist := range existing {
		if exist.Spec.Name == name && exist.Spec.Category == category {
			// already exists
			return true
		}
	}

	return false
}
