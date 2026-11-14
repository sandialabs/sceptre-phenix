package settings

import (
	"errors"
	"fmt"
	"strconv"

	"phenix/api/runtimesettings"
	"phenix/util/mm/mmcli"
)

// MinimegaReadConnectionsKey is the runtime setting that caps how many
// read-only commands phenix sends to minimega at once (see
// mmcli.SetReadOnlyLimit).
const MinimegaReadConnectionsKey = "minimega.read-connections"

var (
	// ErrReadConnectionsRange is the error for a read_connections value
	// outside mmcli.MinReadOnlyLimit to mmcli.MaxReadOnlyLimit.
	ErrReadConnectionsRange = errors.New("read_connections is out of range")

	// ErrReadConnectionsOverridden is the error for a change to
	// read_connections that a flag or a PHENIX_ environment variable sets.
	ErrReadConnectionsOverridden = errors.New("a flag or an environment variable sets read_connections")
)

// MinimegaSettings are the settings of phenix's connections to minimega.
// read_connections is the runtime setting minimega.read-connections.
type MinimegaSettings struct {
	ReadConnections int32 `json:"read_connections"`
	// Source names where the value in use comes from: runtimesettings'
	// flag, env, config file or default. A change cannot set it.
	Source string `json:"source"`
}

// GetMinimegaSettings returns the minimega settings in use, and where they
// come from in rs.
func GetMinimegaSettings(rs *runtimesettings.Settings) MinimegaSettings {
	return MinimegaSettings{
		ReadConnections: clampInt32(mmcli.ReadOnlyLimit()),
		Source:          minimegaSettingSource(rs),
	}
}

// minimegaSettingSource names where rs takes minimega.read-connections from.
func minimegaSettingSource(rs *runtimesettings.Settings) string {
	if rs == nil {
		return runtimesettings.SourceDefault
	}

	setting, err := rs.Get(MinimegaReadConnectionsKey)
	if err != nil {
		return runtimesettings.SourceDefault
	}

	return setting.Source
}

// minimegaChange checks a change of the minimega settings to requested. It
// returns false, and no error, when read_connections does not change or is 0,
// as from a client that does not know the setting.
func minimegaChange(rs *runtimesettings.Settings, requested MinimegaSettings) (bool, error) {
	if requested.ReadConnections == 0 || int(requested.ReadConnections) == mmcli.ReadOnlyLimit() {
		return false, nil
	}

	if requested.ReadConnections < mmcli.MinReadOnlyLimit || requested.ReadConnections > mmcli.MaxReadOnlyLimit {
		return false, fmt.Errorf("%w: it must be %d to %d, not %d",
			ErrReadConnectionsRange, mmcli.MinReadOnlyLimit, mmcli.MaxReadOnlyLimit, requested.ReadConnections)
	}

	if err := checkSettingSource(rs, MinimegaReadConnectionsKey, ErrReadConnectionsOverridden); err != nil {
		return false, err
	}

	file := rs.File()
	if file == "" {
		return false, fmt.Errorf(
			"%w: phenix ui has no config file, so it cannot save %s",
			runtimesettings.ErrNoConfigFile, MinimegaReadConnectionsKey,
		)
	}

	if err := runtimesettings.CheckYAMLFile(file); err != nil {
		return false, err //nolint:wrapcheck // the error names the file
	}

	return true, nil
}

// applyMinimegaChange writes read_connections to the config file of rs, so it
// stays after a restart, and applies it at once.
func applyMinimegaChange(rs *runtimesettings.Settings, requested MinimegaSettings) error {
	value := int(requested.ReadConnections)

	if _, err := rs.Set(MinimegaReadConnectionsKey, strconv.Itoa(value)); err != nil {
		return fmt.Errorf("saving %s: %w", MinimegaReadConnectionsKey, err)
	}

	mmcli.SetReadOnlyLimit(value)

	return nil
}
