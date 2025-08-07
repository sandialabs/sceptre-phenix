package settings

import (
	"fmt"
	"phenix/store"
	"phenix/types"
	v2 "phenix/types/version/v2"
	"phenix/util/plog"
	"strconv"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
)

/*
Adding a new setting:

1. Add to 'Settings' struct in settings.go
2. Add to DEFAULT_SETTINGS in defaults.go
3. Update Getter and Setter functions
	- If category already exists
		- add to settings category file (i.e. password.go)
		- update GET and UPDATE functions
	- If category is new
		- create new file for the categroy, make Get and Update function
		- Add new category to GetSettings and UpdateAllSettings (settings.go)
4. Update Settings.vue for UI visibility
*/

type Settings struct {
	PasswordSettings PasswordSettings `json:"password_settings"`
	LoggingSettings  LoggingSettings  `json:"logging_settings"`
	TimeoutSettings  TimeoutSettings  `json:"timeout_settings"`
}

func GetSettings() (*Settings, error) {
	plog.Debug(plog.TypeSystem, "Getting all settings")
	settings := &Settings{}
	var err error

	settingList, err := List()
	if err != nil {
		return nil, fmt.Errorf("Error listing settings: %w", err)
	}

	settings.PasswordSettings, err = GetPasswordSettingsFromList(settingList)
	if err != nil {
		return nil, fmt.Errorf("Error getting password settings: %v", err)
	}
	settings.LoggingSettings, err = GetLoggingSettingsFromList(settingList)
	if err != nil {
		return nil, fmt.Errorf("Error getting logging settings: %v", err)
	}

	settings.TimeoutSettings, err = GetTimeoutSettingsFromList(settingList)
	if err != nil {
		return nil, fmt.Errorf("Error getting timeout settings: %v", err)
	}

	return settings, nil
}

func UpdateAllSettings(newSettings Settings) error {
	plog.Debug(plog.TypeSystem, "Updating all settings")

	if err := UpdatePasswordSettings(newSettings.PasswordSettings); err != nil {
		return fmt.Errorf("Error updating password settings: %w", err)
	}

	if err := UpdateLoggingSettings(newSettings.LoggingSettings); err != nil {
		return fmt.Errorf("Error updating logging settings: %w", err)
	}

	if err := UpdateTimeoutSettings(newSettings.TimeoutSettings); err != nil {
		return fmt.Errorf("Error updating timeout settings: %w", err)
	}

	return nil
}

func GetStoreName(category, name string) string {
	return fmt.Sprintf("%s.%s", category, name)
}

func Update(category, name, value string) (bool, error) {
	plog.Debug(plog.TypeSystem, "Updating setting", "category", category, "name", name, "value", value)
	oldSetting, err := GetSetting(category, name)
	if err != nil {
		return false, fmt.Errorf("Error getting existing setting: %v", err)
	}

	settingName := GetStoreName(category, name)

	if oldSetting.Spec.Value == value {
		//don't need to update
		return false, nil
	}
	oldSetting.Spec.Value = value

	c := store.Config{
		Version: "phenix.sandia.gov/v2",
		Kind:    "Setting",
		Metadata: store.ConfigMetadata{
			Name: settingName,
		},
		Spec: structs.MapDefaultCase(&oldSetting.Spec, structs.CASESNAKE),
	}

	if err := store.Update(&c); err != nil {
		return false, fmt.Errorf("storing setting %s: %w", settingName, err)
	}

	return true, nil
}

// Same as update but verify the value is of the correct type
func UpdateWithVerification(category, name, value string) error {
	plog.Debug(plog.TypeSystem, "Updating setting with verification", "category", category, "name", name, "value", value)
	oldSetting, err := GetSetting(category, name)
	if err != nil {
		return fmt.Errorf("Error getting existing setting: %v", err)
	}

	settingName := GetStoreName(category, name)

	if oldSetting.Spec.Value == value {
		// don't need to update
		return nil
	}

	if ok, err := verify(oldSetting.Spec.Type, value); !ok {
		return fmt.Errorf("error verifying setting: %w", err)
	}

	oldSetting.Spec.Value = value

	c := store.Config{
		Version: "phenix.sandia.gov/v2",
		Kind:    "Setting",
		Metadata: store.ConfigMetadata{
			Name: settingName,
		},
		Spec: structs.MapDefaultCase(&oldSetting.Spec, structs.CASESNAKE),
	}

	if err := store.Update(&c); err != nil {
		return fmt.Errorf("storing setting %s: %w", settingName, err)
	}

	return nil
}

func verify(settingType v2.SettingValueType, value string) (bool, error) {
	plog.Debug(plog.TypeSystem, "Verifying setting", "type", settingType, "value", value)
	switch settingType {
	case v2.SettingValueBool:
		_, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("error parsing bool: %w", err)
		}
	case v2.SettingValueInt:
		_, err := parseInt(value)
		if err != nil {
			return false, fmt.Errorf("error parsing int: %w", err)
		}
	case v2.SettingValueFloat:
		_, err := parseFloat(value)
		if err != nil {
			return false, fmt.Errorf("error parsing float: %w", err)
		}
	}
	return true, nil
}

func List() ([]types.Setting, error) {
	plog.Debug(plog.TypeSystem, "Listing all settings")
	configs, err := store.List("Setting")
	if err != nil {
		return nil, fmt.Errorf("getting list of settings from store: %w", err)
	}

	var settings []types.Setting

	for _, c := range configs {
		spec := new(v2.Setting)
		if err := mapstructure.Decode(c.Spec, spec); err != nil {
			return nil, fmt.Errorf("decoding image spec: %w", err)
		}

		sett := types.Setting{Metadata: c.Metadata, Spec: spec}
		settings = append(settings, sett)
	}

	if len(settings) < len(DEFAULT_SETTINGS) {
		return setMissingDefaults(settings)
	}

	return settings, nil
}

func GetSetting(category, name string) (*types.Setting, error) {
	plog.Debug(plog.TypeSystem, "Getting setting", "category", category, "name", name)

	combined := fmt.Sprintf("%s.%s", category, name)
	c, _ := store.NewConfig("setting/" + combined)

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("getting setting config %s from store: %w", name, err)
	}

	spec := new(v2.Setting)
	if err := mapstructure.Decode(c.Spec, spec); err != nil {
		return nil, fmt.Errorf("decoding image spec: %w", err)
	}

	sett := &types.Setting{Metadata: c.Metadata, Spec: spec}

	return sett, nil
}

// for consistent parsing / formatting
func parseInt(value string) (int32, error) {
	i, err := strconv.ParseInt(value, 10, 32)
	return int32(i), err
}

func parseFloat(value string) (float64, error) {
	return strconv.ParseFloat(value, 64)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatInt(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}
