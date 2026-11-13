package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// resourceRuntimeSettings is the RBAC resource of the /runtimeSettings routes.
// Its verbs are read and update, not get and list, so that a role that may get
// and list every resource, such as Global Viewer, does not get it.
const resourceRuntimeSettings = "runtimeSettings"

// runtimeSettingKey matches a setting key: dotted parts of letters, digits,
// "-" and "_", such as log.system.max-size.
var runtimeSettingKey = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

// runtimeSettingsRoutes serves the /runtimeSettings routes over settings.
type runtimeSettingsRoutes struct {
	settings *runtimesettings.Settings
}

// newRuntimeSettingsRoutes serves the routes over settings, or, when it is
// nil, over an empty set of settings without a config file.
func newRuntimeSettingsRoutes(settings *runtimesettings.Settings) runtimeSettingsRoutes {
	if settings == nil {
		settings = runtimesettings.New(viper.New())
	}

	return runtimeSettingsRoutes{settings: settings}
}

// runtimeSettingRequest is the body of PUT /runtimeSettings/{key}.
type runtimeSettingRequest struct {
	Value json.RawMessage `json:"value"`
}

// settingText returns the text of a setting's value in a request body: a
// string as it is, or a number or boolean as JSON writes it. Any other value
// is a 400.
func settingText(raw json.RawMessage) (string, error) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", weberror.New(http.StatusBadRequest, "give the value of the setting")
	}

	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return string(raw), nil
	default:
		return "", weberror.New(http.StatusBadRequest,
			"the value must be a string, a number or a boolean; give a list as comma-separated text")
	}
}

// runtimeSettingVars returns the setting key of the request, or a 400 for a
// key that is not one.
func runtimeSettingVars(r *http.Request) (string, error) {
	key := mux.Vars(r)["key"]
	if !runtimeSettingKey.MatchString(key) {
		return "", weberror.New(http.StatusBadRequest, fmt.Sprintf("%q is not a setting key, such as log.level", key))
	}

	return key, nil
}

// GetRuntimeSettings - GET /runtimeSettings.
func (rs runtimeSettingsRoutes) GetRuntimeSettings(w http.ResponseWriter, r *http.Request) error {
	if !middleware.RoleFromContext(r.Context()).Allowed(resourceRuntimeSettings, "read") {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	return writeJSON(w, map[string]any{"settings": rs.settings.List()})
}

// GetRuntimeSetting - GET /runtimeSettings/{key}.
func (rs runtimeSettingsRoutes) GetRuntimeSetting(w http.ResponseWriter, r *http.Request) error {
	if !middleware.RoleFromContext(r.Context()).Allowed(resourceRuntimeSettings, "read") {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	key, err := runtimeSettingVars(r)
	if err != nil {
		return err
	}

	setting, err := rs.settings.Get(key)
	if errors.Is(err, runtimesettings.ErrNotFound) {
		return weberror.New(http.StatusNotFound, err.Error())
	}

	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	return writeJSON(w, setting)
}

// SetRuntimeSetting - PUT /runtimeSettings/{key}.
//
// It writes the value to the config file of phenix ui, as phenix settings set
// does. The audit event names the key, never the value.
func (rs runtimeSettingsRoutes) SetRuntimeSetting(w http.ResponseWriter, r *http.Request) error {
	key := mux.Vars(r)["key"]
	changed := audit.Fields{Extra: []any{"phenix.setting.key", key}}

	if !middleware.RoleFromContext(r.Context()).Allowed(resourceRuntimeSettings, "update") {
		audit.Denied(r, audit.RuntimeSettingChange, "changing runtime settings not allowed", changed)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if _, err := runtimeSettingVars(r); err != nil {
		return err
	}

	var req runtimeSettingRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req); err != nil {
		return weberror.New(http.StatusBadRequest, "invalid request body: "+err.Error())
	}

	text, err := settingText(req.Value)
	if err != nil {
		return err
	}

	change, err := rs.settings.Set(key, text)
	if err != nil {
		audit.Failure(r, audit.RuntimeSettingChange, "changing runtime setting failed", changed, err)

		return runtimeSettingError(err)
	}

	audit.Success(r, audit.RuntimeSettingChange, "changed runtime setting", changed)

	return writeJSON(w, change)
}

// UnsetRuntimeSetting - DELETE /runtimeSettings/{key}.
//
// It removes the key from the config file of phenix ui, as phenix settings
// unset does, so a PHENIX_ variable or the default applies.
func (rs runtimeSettingsRoutes) UnsetRuntimeSetting(w http.ResponseWriter, r *http.Request) error {
	key := mux.Vars(r)["key"]
	changed := audit.Fields{Extra: []any{"phenix.setting.key", key}}

	if !middleware.RoleFromContext(r.Context()).Allowed(resourceRuntimeSettings, "update") {
		audit.Denied(r, audit.RuntimeSettingChange, "changing runtime settings not allowed", changed)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if _, err := runtimeSettingVars(r); err != nil {
		return err
	}

	change, err := rs.settings.Unset(key)
	if err != nil {
		audit.Failure(r, audit.RuntimeSettingChange, "unsetting runtime setting failed", changed, err)

		return runtimeSettingError(err)
	}

	if change.Changed {
		audit.Success(r, audit.RuntimeSettingChange, "unset runtime setting", changed)
	}

	return writeJSON(w, change)
}

// runtimeSettingError is the answer for an error of Set or Unset: 400 for a
// value that the setting refuses, 409 for a config file that phenix cannot
// rewrite or that does not exist, and 500 otherwise.
func runtimeSettingError(err error) error {
	switch {
	case errors.Is(err, runtimesettings.ErrSettingType), errors.Is(err, runtimesettings.ErrTextNotYAML):
		return weberror.New(http.StatusBadRequest, err.Error())
	case errors.Is(err, runtimesettings.ErrNotYAML), errors.Is(err, runtimesettings.ErrNoConfigFile):
		return weberror.New(http.StatusConflict, err.Error())
	default:
		return weberror.New(http.StatusInternalServerError, err.Error())
	}
}

// writeJSON answers 200 with body as JSON.
func writeJSON(w http.ResponseWriter, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(data) //nolint:gosec // G705 JSON, not HTML

	return nil
}
