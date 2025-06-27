package settings

import (
	"fmt"
	"phenix/types"
	"phenix/util/plog"
)

type LoggingSettings struct {
	MaxFileRotations int32 `json:"max_file_rotations"`
	MaxFileSize      int32 `json:"max_file_size"`
	MaxFileAge       int32 `json:"max_file_age"`
}

func GetLoggingSettings() (LoggingSettings, error) {
	plog.Debug(plog.TypeSystem, "Getting all log settings")

	settings, err := List()
	if err != nil {
		return LoggingSettings{}, fmt.Errorf("Error listing settings: %w", err)
	}

	return GetLoggingSettingsFromList(settings)
}

func GetLoggingSettingsFromList(settings []types.Setting) (LoggingSettings, error) {

	logsettings := LoggingSettings{}
	var err error

	for _, setting := range settings {
		category := setting.Spec.Category
		name := setting.Spec.Name
		if category != "Logging" {
			continue
		}

		switch name {
		case "MaxFileRotations":
			logsettings.MaxFileRotations, err = parseInt(setting.Spec.Value)
			if err != nil {
				return logsettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "MaxFileSize":
			logsettings.MaxFileSize, err = parseInt(setting.Spec.Value)
			if err != nil {
				return logsettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		case "MaxFileAge":
			logsettings.MaxFileAge, err = parseInt(setting.Spec.Value)
			if err != nil {
				return logsettings, fmt.Errorf("Error parsing %s.%s setting: %w", category, name, err)
			}
		}
	}
	return logsettings, nil
}

func UpdateLoggingSettings(newSettings LoggingSettings) error {
	var err error
	var updated bool

	updated, err = Update("Logging", "MaxFileRotations", formatInt(newSettings.MaxFileRotations))
	if err != nil {
		return fmt.Errorf("Error updating Logging.MaxFileRotations: %w", err)
	}
	if updated {
		plog.ChangeMaxLogFileBackups(int(newSettings.MaxFileRotations))
	}

	updated, err = Update("Logging", "MaxFileSize", formatInt(newSettings.MaxFileSize))
	if err != nil {
		return fmt.Errorf("Error updating Logging.MaxFileSize: %w", err)
	}
	if updated {
		plog.ChangeMaxLogFileSize(int(newSettings.MaxFileSize))
	}

	updated, err = Update("Logging", "MaxFileAge", formatInt(newSettings.MaxFileAge))
	if err != nil {
		return fmt.Errorf("Error updating Logging.MaxFileAge: %w", err)
	}
	if updated {
		plog.ChangeMaxLogFileAge(int(newSettings.MaxFileAge))
	}

	plog.Debug(plog.TypeSystem, "Updated logging settings successfully")
	return nil
}
