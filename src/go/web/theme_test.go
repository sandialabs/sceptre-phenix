package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

func TestThemeBootstrap(t *testing.T) {
	themeMu.Lock()
	previous := o
	o.defaultTheme = "dark"
	themeMu.Unlock()
	t.Cleanup(func() {
		themeMu.Lock()
		o = previous
		themeMu.Unlock()
	})

	request := httptest.NewRequest(http.MethodGet, "/theme.js", nil)
	response := httptest.NewRecorder()

	GetThemeBootstrap(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(response.Body.String(), `const defaultTheme = "dark"`) {
		t.Fatalf("bootstrap does not contain default theme: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "phenix.theme") {
		t.Fatal("bootstrap does not read the local theme preference")
	}
	if !strings.Contains(response.Body.String(), "prefers-color-scheme: dark") {
		t.Fatal("bootstrap does not resolve the system theme")
	}
}

func TestSetDefaultThemeRespectsLock(t *testing.T) {
	themeMu.Lock()
	previous := o
	o.defaultTheme = "light"
	o.defaultThemeLocked = true
	themeMu.Unlock()
	t.Cleanup(func() {
		themeMu.Lock()
		o = previous
		themeMu.Unlock()
	})

	if err := SetDefaultTheme("dark"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := currentDefaultTheme().DefaultTheme; got != "light" {
		t.Fatalf("default theme = %q, want locked light", got)
	}
}

func TestSetDefaultThemeSettingPersists(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("log:\n  level: debug\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	setThemeOptionsForTest(t, "system", false, configFile)

	role := rbac.Role{Spec: &v1.RoleSpec{Name: "theme-admin"}}
	role.AddPolicy([]string{"settings"}, nil, []string{"update"})

	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/theme",
		strings.NewReader(`{"default_theme":"dark"}`),
	)
	ctx := context.WithValue(request.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")
	response := httptest.NewRecorder()

	SetDefaultThemeSetting(response, request.WithContext(ctx))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := currentDefaultTheme().DefaultTheme; got != "dark" {
		t.Fatalf("default theme = %q, want dark", got)
	}

	config, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.Contains(string(config), "default-theme: dark") {
		t.Fatalf("config does not contain persisted theme:\n%s", config)
	}
	if !strings.Contains(string(config), "level: debug") {
		t.Fatalf("config did not preserve unrelated settings:\n%s", config)
	}
}

func TestSetDefaultThemeSettingRequiresPermission(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	setThemeOptionsForTest(t, "system", false, configFile)

	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/theme",
		strings.NewReader(`{"default_theme":"dark"}`),
	)
	role := rbac.Role{Spec: &v1.RoleSpec{Name: "theme-viewer"}}
	ctx := context.WithValue(request.Context(), middleware.ContextKeyRole, role)
	response := httptest.NewRecorder()

	SetDefaultThemeSetting(response, request.WithContext(ctx))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatalf("config should not be created, stat error = %v", err)
	}
	if got := currentDefaultTheme().DefaultTheme; got != "system" {
		t.Fatalf("default theme = %q, want system", got)
	}
}

func TestSetDefaultThemeSettingRejectsLockedSetting(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	setThemeOptionsForTest(t, "light", true, configFile)

	role := rbac.Role{Spec: &v1.RoleSpec{Name: "theme-admin"}}
	role.AddPolicy([]string{"settings"}, nil, []string{"update"})

	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/theme",
		strings.NewReader(`{"default_theme":"dark"}`),
	)
	ctx := context.WithValue(request.Context(), middleware.ContextKeyRole, role)
	response := httptest.NewRecorder()

	SetDefaultThemeSetting(response, request.WithContext(ctx))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Fatalf("config should not be created, stat error = %v", err)
	}
	if got := currentDefaultTheme().DefaultTheme; got != "light" {
		t.Fatalf("default theme = %q, want locked light", got)
	}
}

func setThemeOptionsForTest(t *testing.T, value string, locked bool, configFile string) {
	t.Helper()

	themeMu.Lock()
	previous := o
	o.defaultTheme = value
	o.defaultThemeLocked = locked
	o.configFile = configFile
	themeMu.Unlock()

	t.Cleanup(func() {
		themeMu.Lock()
		o = previous
		themeMu.Unlock()
	})
}
