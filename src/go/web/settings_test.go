package web

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/settings"
)

// POST /settings refuses a password minimum length out of range, or left
// out, with 400, before it changes any setting.
func TestSetSettingsRefusesMinLength(t *testing.T) {
	useTestStore(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("settings.SetDefaults() error = %v", err)
	}

	before, err := settings.GetSettings()
	if err != nil {
		t.Fatalf("settings.GetSettings() error = %v", err)
	}

	server := serveContractAs(t, adminRole(), nil).URL

	for name, body := range map[string]string{
		"min length out of range": `{"password_settings":{"uppercase_req":true,"min_length":3},"timeout_settings":{"enabled":true}}`,
		"no min length":           `{"password_settings":{"uppercase_req":true},"timeout_settings":{"enabled":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPost, server+"/api/v1/settings", body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("POST /api/v1/settings %s = %d, want %d: %s", body, resp.StatusCode, http.StatusBadRequest, readBody(t, resp))
			}

			after, err := settings.GetSettings()
			if err != nil {
				t.Fatalf("settings.GetSettings() error = %v", err)
			}

			if diff := cmp.Diff(before, after); diff != "" {
				t.Errorf("settings after POST /api/v1/settings %s mismatch (-want +got):\n%s", body, diff)
			}
		})
	}
}
