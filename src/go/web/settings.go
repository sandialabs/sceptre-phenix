package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"phenix/api/settings"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// GetSettings - GET /settings.
func GetSettings(w http.ResponseWriter, r *http.Request) error {
	settings, err := settings.GetSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting proto settings", "err:", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(settings)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling settings", "err:", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	_, _ = w.Write(body)

	return nil
}

// SetSettings - POST /settings.
func SetSettings(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("settings", "update") {
		audit.Denied(r, audit.SettingsUpdate, "changing settings not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	s := &settings.Settings{} //nolint:exhaustruct // partial initialization

	err = json.Unmarshal(body, s)
	if err != nil {
		plog.Error(plog.TypeSystem, "Unmarshaling request body", "err", err)

		return weberror.New(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	}

	changed := audit.Fields{}

	if current, err := settings.GetSettings(); err == nil {
		changed.Extra = settingChanges(*current, *s)
	}

	err = settings.UpdateAllSettings(*s)
	if err != nil {
		audit.Failure(r, audit.SettingsUpdate, "changing settings failed", changed, err)
	}

	if errors.Is(err, settings.ErrPasswordMinLength) {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "Updating all settings", "err", err)

		return weberror.New(http.StatusInternalServerError, "Error updating settings")
	}

	audit.Success(r, audit.SettingsUpdate, "settings changed", changed)

	return nil
}

// settingChanges are the audit fields naming the settings that differ between
// current and requested, as category.name (phenix.settings.changed), and
// their requested values (phenix.settings.values), but for any secret's.
func settingChanges(current, requested settings.Settings) []any {
	flatten := func(s settings.Settings) map[string]string {
		var categories map[string]map[string]any

		// a Settings holds only bools and numbers, so this cannot fail
		body, _ := json.Marshal(s)
		_ = json.Unmarshal(body, &categories)

		flat := make(map[string]string)

		for category, values := range categories {
			for name, value := range values {
				flat[category+"."+name] = fmt.Sprint(value)
			}
		}

		return flat
	}

	var (
		before, after = flatten(current), flatten(requested)
		names         = []string{}
		values        = make(map[string]string)
	)

	for _, name := range slices.Sorted(maps.Keys(after)) {
		if after[name] == before[name] {
			continue
		}

		names = append(names, name)

		if !secretSetting(name) {
			values[name] = after[name]
		}
	}

	return []any{"phenix.settings.changed", names, "phenix.settings.values", values}
}

// secretSetting reports whether the setting category.name could hold a
// secret, whose value is never audited.
func secretSetting(name string) bool {
	_, leaf, _ := strings.Cut(name, ".")

	for _, word := range []string{"secret", "token", "password", "key"} {
		if strings.Contains(leaf, word) {
			return true
		}
	}

	return false
}

// GetPasswordRequirements - GET /settings/password.
func GetPasswordRequirements(w http.ResponseWriter, r *http.Request) error {
	ensureSettingDefaults()

	passwordReqs, err := settings.GetPasswordSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting password settings:", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(passwordReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling password settings:", "err", err)

		return weberror.New(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	}

	_, _ = w.Write(body)

	return nil
}

// settingDefaultsOnce stores the default settings once per process: the
// settings pages ask for them often, storing them opens the store for writing,
// and settings.List already fills in any defaults missing afterwards.
var settingDefaultsOnce sync.Once //nolint:gochecknoglobals // process-wide once

func ensureSettingDefaults() {
	settingDefaultsOnce.Do(func() { _ = settings.SetDefaults() })
}

// GetTimeoutSettings - GET /settings/timeout.
func GetTimeoutSettings(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTimeoutSettings")

	ensureSettingDefaults()

	timeoutReqs, err := settings.GetTimeoutSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting timeout settings:", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(timeoutReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling timeout settings:", "err", err)

		return weberror.New(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	}

	_, _ = w.Write(body)

	return nil
}
