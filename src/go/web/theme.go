package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"phenix/util/plog"
	"phenix/util/runtimeconfig"
	"phenix/util/theme"
	"phenix/web/middleware"
)

const maxThemeRequestSize = 1024

var themeMu sync.RWMutex //nolint:gochecknoglobals // runtime server configuration

type defaultThemeResponse struct {
	DefaultTheme string `json:"default_theme"`
	Locked       bool   `json:"locked"`
}

type defaultThemeRequest struct {
	DefaultTheme string `json:"default_theme"`
}

func currentDefaultTheme() defaultThemeResponse {
	themeMu.RLock()
	defer themeMu.RUnlock()

	return defaultThemeResponse{
		DefaultTheme: o.defaultTheme,
		Locked:       o.defaultThemeLocked,
	}
}

func SetDefaultTheme(value string) error {
	mode, err := theme.Parse(value)
	if err != nil {
		return err
	}

	themeMu.Lock()
	defer themeMu.Unlock()

	if !o.defaultThemeLocked {
		o.defaultTheme = string(mode)
	}

	return nil
}

func GetThemeBootstrap(w http.ResponseWriter, _ *http.Request) {
	response := currentDefaultTheme()
	defaultThemeJSON, err := json.Marshal(response.DefaultTheme)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling default theme", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	script := fmt.Sprintf(`(() => {
  const defaultTheme = %s;
  let localTheme = null;
  try {
    localTheme = localStorage.getItem('phenix.theme');
    if (localTheme !== 'light' && localTheme !== 'dark') {
      if (localTheme !== null) localStorage.removeItem('phenix.theme');
      localTheme = null;
    }
  } catch (error) {
    window.__PHENIX_THEME_STORAGE_ERROR__ = String(error);
  }
  const requestedTheme = localTheme || defaultTheme;
  const resolvedTheme = requestedTheme === 'system'
    ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    : requestedTheme;
  window.__PHENIX_DEFAULT_THEME__ = defaultTheme;
  document.documentElement.dataset.theme = resolvedTheme;
  document.documentElement.style.colorScheme = resolvedTheme;
})();
`, defaultThemeJSON)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = io.WriteString(w, script)
}

func GetDefaultThemeSetting(w http.ResponseWriter, _ *http.Request) {
	writeDefaultThemeResponse(w, currentDefaultTheme())
}

func SetDefaultThemeSetting(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromContext(r.Context())
	if !role.Allowed("settings", "update") {
		user := middleware.UserFromContext(r.Context())
		plog.Warn(plog.TypeSecurity, "updating default theme not allowed", "user", user)
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	response := currentDefaultTheme()
	if response.Locked {
		http.Error(
			w,
			"default theme is controlled by the --default-theme command-line flag",
			http.StatusConflict,
		)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxThemeRequestSize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request defaultThemeRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid default theme request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := ensureJSONEOF(decoder); err != nil {
		http.Error(w, "invalid default theme request: "+err.Error(), http.StatusBadRequest)
		return
	}

	mode, err := theme.Parse(request.DefaultTheme)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	themeMu.RLock()
	configFile := o.configFile
	themeMu.RUnlock()

	if err := runtimeconfig.Set(configFile, "ui.default-theme", string(mode)); err != nil {
		plog.Error(plog.TypeSystem, "persisting default theme", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := SetDefaultTheme(string(mode)); err != nil {
		plog.Error(plog.TypeSystem, "applying default theme", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	plog.Info(
		plog.TypeAction,
		"default theme changed",
		"user",
		middleware.UserFromContext(r.Context()),
		"theme",
		mode,
	)
	writeDefaultThemeResponse(w, currentDefaultTheme())
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("request must contain one JSON object")
	}
	return err
}

func writeDefaultThemeResponse(w http.ResponseWriter, response defaultThemeResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		plog.Error(plog.TypeSystem, "writing default theme response", "err", err)
	}
}
