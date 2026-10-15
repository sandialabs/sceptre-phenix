package web

import (
	"net/http"
	"reflect"
	"testing"

	"phenix/api/settings"
)

// POST /settings refuses a password minimum length out of range, or left
// out, with 400, before it changes any setting.
func TestSetSettingsRefusesMinLength(t *testing.T) {
	useTestStore(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatal(err)
	}

	before, err := settings.GetSettings()
	if err != nil {
		t.Fatal(err)
	}

	server := serveContractAs(t, contractAdmin(), nil).URL

	for _, body := range []string{
		`{"password_settings":{"uppercase_req":true,"min_length":3},"timeout_settings":{"enabled":true}}`,
		`{"password_settings":{"uppercase_req":true},"timeout_settings":{"enabled":true}}`,
	} {
		resp := do(t, http.MethodPost, server+"/api/v1/settings", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %s", body, resp.StatusCode, readBody(t, resp))
		}

		after, err := settings.GetSettings()
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(after, before) {
			t.Errorf("%s: settings changed to %+v, from %+v", body, after, before)
		}
	}
}
