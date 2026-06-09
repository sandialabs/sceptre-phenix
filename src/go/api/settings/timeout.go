package settings

import (
	"fmt"
	"strconv"

	"phenix/types"
)

type TimeoutSettings struct {
	Enabled    bool    `json:"enabled"`
	TimeoutMin float64 `json:"timeout_min"`
	WarningMin float64 `json:"warning_min"`
}

func GetTimeoutSettings() (TimeoutSettings, error) {
	settings, err := List()
	if err != nil {
		return TimeoutSettings{}, fmt.Errorf("error listing settings: %w", err)
	}

	return GetTimeoutSettingsFromList(settings)
}

func GetTimeoutSettingsFromList(settings []types.Setting) (TimeoutSettings, error) {
	timeout := TimeoutSettings{} //nolint:exhaustruct // partial initialization
	var err error

	for _, setting := range settings {
		category := setting.Spec.Category
		name := setting.Spec.Name
		if category != "Timeout" {
			continue
		}

		switch name {
		case "Enabled":
			timeout.Enabled, err = strconv.ParseBool(setting.Spec.Value)
			if err != nil {
				return timeout, fmt.Errorf("error parsing %s.%s setting: %w", category, name, err)
			}
		case "TimeoutMin":
			timeout.TimeoutMin, err = parseFloat(setting.Spec.Value)
			if err != nil {
				return timeout, fmt.Errorf("error parsing %s.%s setting: %w", category, name, err)
			}
		case "WarningMin":
			timeout.WarningMin, err = parseFloat(setting.Spec.Value)
			if err != nil {
				return timeout, fmt.Errorf("error parsing %s.%s setting: %w", category, name, err)
			}
		}
	}

	return timeout, nil
}

func UpdateTimeoutSettings(newSettings TimeoutSettings) error {
	if _, err := Update("Timeout", "Enabled", strconv.FormatBool(newSettings.Enabled)); err != nil {
		return fmt.Errorf("error updating Timeout.Enabled: %w", err)
	}

	if _, err := Update("Timeout", "TimeoutMin", formatFloat(newSettings.TimeoutMin)); err != nil {
		return fmt.Errorf("error updating Timeout.TimeoutMin: %w", err)
	}

	if _, err := Update("Timeout", "WarningMin", formatFloat(newSettings.WarningMin)); err != nil {
		return fmt.Errorf("error updating Timeout.WarningMin: %w", err)
	}

	return nil
}
