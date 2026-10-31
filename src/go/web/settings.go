package web

import (
	"encoding/json"
	"io"
	"net/http"

	"phenix/api/settings"
	"phenix/util/plog"
	"phenix/web/middleware"
)

// GetSettings - GET /settings.
func GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := settings.GetSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "getting proto settings", "err:", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(settings)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling settings", "err:", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// SetSettings - POST /settings.
func SetSettings(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
	)

	if !role.Allowed("settings", "update") {
		user := middleware.UserFromContext(ctx)
		plog.Warn(
			plog.TypeSystem,
			"setting settings not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	s := &settings.Settings{} //nolint:exhaustruct // partial initialization

	err = json.Unmarshal(body, s)
	if err != nil {
		plog.Error(plog.TypeSystem, "Unmarshaling request body", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	err = settings.UpdateAllSettings(*s)
	if err != nil {
		plog.Error(plog.TypeSystem, "Updating all settings", "err", err)
		http.Error(w, "Error updating settings", http.StatusInternalServerError)

		return
	}

	user := middleware.UserFromContext(ctx)
	plog.Info(
		plog.TypeSystem,
		"settings changed",
		"user",
		user,
	)
}

// GetPasswordRequirements - GET /settings/password.
func GetPasswordRequirements(w http.ResponseWriter, r *http.Request) {
	_ = settings.SetDefaults()

	passwordReqs, err := settings.GetPasswordSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting password settings:", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(passwordReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling password reqs:", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetTimeoutSettings - GET /settings/timeout.
func GetTimeoutSettings(w http.ResponseWriter, r *http.Request) {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetTimeoutSettings")

	_ = settings.SetDefaults()

	timeoutReqs, err := settings.GetTimeoutSettings()
	if err != nil {
		plog.Error(plog.TypeSystem, "Getting timeout settings:", "err", err)
		http.Error(
			w,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	body, err := json.Marshal(timeoutReqs)
	if err != nil {
		plog.Error(plog.TypeSystem, "Marshalling timeout reqs:", "err", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}
