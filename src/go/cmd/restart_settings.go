package cmd

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/viper"

	"phenix/util/plog"
)

// liveSettings are the settings, and prefixes ending in a dot, that the config
// watcher applies at once; any other change waits for phenix ui to restart.
//
//nolint:gochecknoglobals // constant list
var liveSettings = []string{"log.", "ui.logs.level", "ui.users", "experiment.disk-check"}

// uiStartSettings holds phenix ui's settings at startup; other commands leave it unset.
//
//nolint:gochecknoglobals // state of the config watcher
var uiStartSettings restartBaseline

// restartBaseline is the settings a running phenix ui started with.
type restartBaseline struct {
	mu    sync.Mutex
	start map[string]any
}

func (b *restartBaseline) record(settings map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.start = settings
}

// pending returns the keys that differ from the startup settings and need a
// restart, or nil before record.
func (b *restartBaseline) pending(current map[string]any) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.start == nil {
		return nil
	}

	return restartRequiredChanges(b.start, current)
}

// watchRestartSettings records the settings phenix ui starts with.
func watchRestartSettings() {
	uiStartSettings.record(flatSettings())
}

// warnRestartSettings logs, by name only, the settings phenix ui applies only on a restart.
func warnRestartSettings() {
	keys := uiStartSettings.pending(flatSettings())
	if len(keys) > 0 {
		plog.Warn(
			plog.TypeSystem, "config change takes effect only when phenix ui restarts", "keys", strings.Join(keys, ", "),
		)
	}
}

// flatSettings are the settings phenix reads at startup, by dotted key.
func flatSettings() map[string]any {
	flat := make(map[string]any)
	flattenSettingsMap("", viper.AllSettings(), flat)

	return flat
}

// restartRequiredChanges returns, sorted, the keys whose values differ between
// before and after, leaving out the live settings.
func restartRequiredChanges(before, after map[string]any) []string {
	var keys []string

	for key, value := range after {
		if isLiveSetting(key) {
			continue
		}

		if old, ok := before[key]; !ok || !reflect.DeepEqual(old, value) {
			keys = append(keys, key)
		}
	}

	for key := range before {
		if _, ok := after[key]; !ok && !isLiveSetting(key) {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	return keys
}

func isLiveSetting(key string) bool {
	for _, live := range liveSettings {
		if key == live || (strings.HasSuffix(live, ".") && strings.HasPrefix(key, live)) {
			return true
		}
	}

	return false
}
