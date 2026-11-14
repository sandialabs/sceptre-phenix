package settings

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"phenix/api/runtimesettings"
	"phenix/util/plog"
)

// The runtime settings that hold the limits of the system log file.
const (
	LogMaxSizeKey    = "log.system.max-size"
	LogMaxBackupsKey = "log.system.max-backups"
	LogMaxAgeKey     = "log.system.max-age"
)

var (
	// ErrLogLimitNegative is the error for a log file limit less than 0.
	ErrLogLimitNegative = errors.New("log file limit is less than 0")

	// ErrLogLimitOverridden is the error for a change to a log file limit
	// that a flag or a PHENIX_ environment variable sets. The config file
	// cannot change such a limit.
	ErrLogLimitOverridden = errors.New("a flag or an environment variable sets this log file limit")
)

// LoggingSettings are the limits of the system log file. Each one is a
// runtime setting: max_file_rotations is log.system.max-backups,
// max_file_size is log.system.max-size and max_file_age is log.system.max-age.
type LoggingSettings struct {
	MaxFileRotations int32 `json:"max_file_rotations"`
	MaxFileSize      int32 `json:"max_file_size"`
	MaxFileAge       int32 `json:"max_file_age"`
}

// GetLoggingSettings returns the limits that the system log file uses now.
func GetLoggingSettings() LoggingSettings {
	limits := plog.LogFileLimits()

	return LoggingSettings{
		MaxFileRotations: clampInt32(limits.MaxBackups),
		MaxFileSize:      clampInt32(limits.MaxSize),
		MaxFileAge:       clampInt32(limits.MaxAge),
	}
}

// clampInt32 returns n, or the nearest int32 to n.
func clampInt32(n int) int32 {
	return int32(max(min(n, math.MaxInt32), math.MinInt32)) //nolint:gosec // clamped to the int32 range
}

// logLimitChange is a log file limit that UpdateAllSettings changes.
type logLimitChange struct {
	key   string
	value int32
	apply func(int)
}

// logLimitChanges returns the log file limits in requested that differ from
// those the system log file uses now. It returns an error, and no change, when
// one of them is less than 0, when a flag or a PHENIX_ variable sets one of
// them, or when rs has no YAML config file to write them to.
func logLimitChanges(rs *runtimesettings.Settings, requested LoggingSettings) ([]logLimitChange, error) {
	current := GetLoggingSettings()

	limits := []struct {
		key                string
		current, requested int32
		apply              func(int)
	}{
		{LogMaxSizeKey, current.MaxFileSize, requested.MaxFileSize, plog.ChangeMaxLogFileSize},
		{LogMaxBackupsKey, current.MaxFileRotations, requested.MaxFileRotations, plog.ChangeMaxLogFileBackups},
		{LogMaxAgeKey, current.MaxFileAge, requested.MaxFileAge, plog.ChangeMaxLogFileAge},
	}

	var changes []logLimitChange

	for _, limit := range limits {
		if limit.requested == limit.current {
			continue
		}

		if limit.requested < 0 {
			return nil, fmt.Errorf("%w: %s must be 0 or more, not %d", ErrLogLimitNegative, limit.key, limit.requested)
		}

		if err := checkSettingSource(rs, limit.key, ErrLogLimitOverridden); err != nil {
			return nil, err
		}

		changes = append(changes, logLimitChange{key: limit.key, value: limit.requested, apply: limit.apply})
	}

	if len(changes) == 0 {
		return nil, nil
	}

	file := rs.File()
	if file == "" {
		return nil, fmt.Errorf(
			"%w: phenix ui has no config file, so it cannot save %s", runtimesettings.ErrNoConfigFile, changes[0].key,
		)
	}

	if err := runtimesettings.CheckYAMLFile(file); err != nil {
		return nil, err //nolint:wrapcheck // the error names the file
	}

	return changes, nil
}

// checkSettingSource returns an error that wraps overridden when a flag or a
// PHENIX_ variable sets the runtime setting key.
func checkSettingSource(rs *runtimesettings.Settings, key string, overridden error) error {
	setting, err := rs.Get(key)
	if errors.Is(err, runtimesettings.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading runtime setting %s: %w", key, err)
	}

	switch setting.Source {
	case runtimesettings.SourceFlag:
		return fmt.Errorf(
			"%w: the --%s flag of phenix ui sets %s. Remove the flag and restart phenix ui. Then change the value here",
			overridden, key, key,
		)
	case runtimesettings.SourceEnv:
		return fmt.Errorf(
			"%w: the %s environment variable of phenix ui sets %s. "+
				"Remove the variable and restart phenix ui. Then change the value here",
			overridden, runtimesettings.EnvVar(key), key,
		)
	}

	return nil
}

// applyLogLimitChanges writes each change to the config file of rs, so that it
// stays after a restart, and gives it to the system log file at once.
func applyLogLimitChanges(rs *runtimesettings.Settings, changes []logLimitChange) error {
	for _, change := range changes {
		if _, err := rs.Set(change.key, strconv.Itoa(int(change.value))); err != nil {
			return fmt.Errorf("saving %s: %w", change.key, err)
		}

		change.apply(int(change.value))
	}

	if len(changes) > 0 {
		plog.Debug(plog.TypeSystem, "Updated logging settings successfully")
	}

	return nil
}
