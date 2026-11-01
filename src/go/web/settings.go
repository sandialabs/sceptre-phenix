package web

import (
	"encoding/json"
	"io"
	"net/http"

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

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// SetSettings - POST /settings.
func SetSettings(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

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

	err = settings.UpdateAllSettings(*s)
	if err != nil {
		audit.Failure(r, audit.SettingsUpdate, "changing settings failed", audit.Fields{}, err)
		plog.Error(plog.TypeSystem, "Updating all settings", "err", err)

		return weberror.New(http.StatusInternalServerError, "Error updating settings")
	}

	audit.Success(r, audit.SettingsUpdate, "settings changed", audit.Fields{})

	return nil
}

// GetPasswordRequirements - GET /settings/password.
func GetPasswordRequirements(w http.ResponseWriter, r *http.Request) error {
	_ = settings.SetDefaults()

	passwordReqs, err := settings.GetPasswordSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting password settings:", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(passwordReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling password reqs:", "err", err)

		return weberror.New(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// GetTimeoutSettings - GET /settings/timeout.
func GetTimeoutSettings(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTimeoutSettings")

	_ = settings.SetDefaults()

	timeoutReqs, err := settings.GetTimeoutSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting timeout settings:", "err", err)

		return weberror.New(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(timeoutReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling timeout reqs:", "err", err)

		return weberror.New(http.StatusBadRequest, http.StatusText(http.StatusBadRequest))
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}
