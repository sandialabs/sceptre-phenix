package settings

import (
	"fmt"
	"strconv"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	"phenix/store"
	"phenix/types"
	v2 "phenix/types/version/v2"
)

/*
Adding a new setting:

1. Add to 'Settings' struct in settings.go
2. Add to DefaultSettings in defaults.go
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
}

func GetSettings() (*Settings, error) {
	settings := &Settings{} //nolint:exhaustruct // partial initialization

	var err error

	settingList, err := List()
	if err != nil {
		return nil, fmt.Errorf("error listing settings: %w", err)
	}

	settings.PasswordSettings, err = GetPasswordSettingsFromList(settingList)
	if err != nil {
		return nil, fmt.Errorf("error getting password settings: %w", err)
	}

	settings.LoggingSettings, err = GetLoggingSettingsFromList(settingList)
	if err != nil {
		return nil, fmt.Errorf("error getting logging settings: %w", err)
	}

	return settings, nil
}

func UpdateAllSettings(newSettings Settings) error {
	err := UpdatePasswordSettings(newSettings.PasswordSettings)
	if err != nil {
		return fmt.Errorf("error updating password settings: %w", err)
	}

	err = UpdateLoggingSettings(newSettings.LoggingSettings)
	if err != nil {
		return fmt.Errorf("error updating logging settings: %w", err)
	}

	return nil
}

func GetStoreName(category, name string) string {
	return fmt.Sprintf("%s.%s", category, name)
}

func Update(category, name, value string) (bool, error) {
	oldSetting, err := GetSetting(category, name)
	if err != nil {
		return false, fmt.Errorf("error getting existing setting: %w", err)
	}

	settingName := GetStoreName(category, name)

	if oldSetting.Spec.Value == value {
		// don't need to update
		return false, nil
	}

	oldSetting.Spec.Value = value

	c := store.Config{ //nolint:exhaustruct // partial initialization
		Version: "phenix.sandia.gov/v2",
		Kind:    "Setting",
		Metadata: store.ConfigMetadata{ //nolint:exhaustruct // partial initialization
			Name: settingName,
		},
		Spec: structs.MapDefaultCase(&oldSetting.Spec, structs.CASESNAKE),
	}

	if err = store.Update(&c); err != nil {
		return false, fmt.Errorf("storing setting %s: %w", settingName, err)
	}

	return true, nil
}

// UpdateWithVerification is the same as update but verify the value is of the correct type.
func UpdateWithVerification(category, name, value string) error {
	oldSetting, err := GetSetting(category, name)
	if err != nil {
		return fmt.Errorf("error getting existing setting: %w", err)
	}

	settingName := GetStoreName(category, name)

	if oldSetting.Spec.Value == value {
		// don't need to update
		return nil
	}

	var ok bool
	if ok, err = verify(oldSetting.Spec.Type, value); !ok {
		return fmt.Errorf("error verifying setting: %w", err)
	}

	oldSetting.Spec.Value = value

	c := store.Config{ //nolint:exhaustruct // partial initialization
		Version: "phenix.sandia.gov/v2",
		Kind:    "Setting",
		Metadata: store.ConfigMetadata{ //nolint:exhaustruct // partial initialization
			Name: settingName,
		},
		Spec: structs.MapDefaultCase(&oldSetting.Spec, structs.CASESNAKE),
	}

	if err = store.Update(&c); err != nil {
		return fmt.Errorf("storing setting %s: %w", settingName, err)
	}

	return nil
}

func verify(settingType v2.SettingValueType, value string) (bool, error) {
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
	case v2.SettingValueString:
		// no verification needed
	}

	return true, nil
}

func List() ([]types.Setting, error) {
	configs, err := store.List("Setting")
	if err != nil {
		return nil, fmt.Errorf("getting list of settings from store: %w", err)
	}

	var settings []types.Setting

	for _, c := range configs {
		spec := new(v2.Setting)

		err = mapstructure.Decode(c.Spec, spec)
		if err != nil {
			return nil, fmt.Errorf("decoding image spec: %w", err)
		}

		sett := types.Setting{Metadata: c.Metadata, Spec: spec}
		settings = append(settings, sett)
	}

	if len(settings) < len(DefaultSettings) {
		return setMissingDefaults(settings)
	}

	return settings, nil
}

func GetSetting(category, name string) (*types.Setting, error) {
	combined := fmt.Sprintf("%s.%s", category, name)
	c, _ := store.NewConfig("setting/" + combined)

	err := store.Get(c)
	if err != nil {
		return nil, fmt.Errorf("getting setting config %s from store: %w", name, err)
	}

	spec := new(v2.Setting)

	err = mapstructure.Decode(c.Spec, spec)
	if err != nil {
		return nil, fmt.Errorf("decoding image spec: %w", err)
	}

	sett := &types.Setting{Metadata: c.Metadata, Spec: spec}

	return sett, nil
}

// for consistent parsing / formatting.
func parseInt(value string) (int32, error) {
	i, err := strconv.ParseInt(value, 10, 32)

	return int32(i), err
}

func parseFloat(value string) (float64, error) {
	return strconv.ParseFloat(value, 64)
}

func formatInt(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}
