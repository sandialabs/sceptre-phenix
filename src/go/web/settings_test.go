package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/settings"
	"phenix/store"
	"phenix/store/storetest"
)

// settingsTestStore is a store whose List or Update fails when the test
// gives it an error; other calls go to the embedded store.
type settingsTestStore struct {
	store.Store

	listErr, updateErr error
}

func (s settingsTestStore) List(kinds ...string) (store.Configs, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	return s.Store.List(kinds...) //nolint:wrapcheck // test double
}

func (s settingsTestStore) Update(c *store.Config) error {
	if s.updateErr != nil {
		return s.updateErr
	}

	return s.Store.Update(c) //nolint:wrapcheck // test double
}

// settingsTestErrReader is a request body that cannot be read.
type settingsTestErrReader struct{}

func (settingsTestErrReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// settingsTestStored is the settings useSettingsTestStore stores; its logging
// settings are the defaults, so storing it changes no plog file limits.
func settingsTestStored() settings.Settings {
	return settings.Settings{
		PasswordSettings: settings.PasswordSettings{
			LowercaseReq: true, UppercaseReq: true, NumberReq: false, SymbolReq: true, MinLength: 12,
		},
		LoggingSettings: settings.LoggingSettings{MaxFileRotations: 0, MaxFileSize: 100, MaxFileAge: 90},
		TimeoutSettings: settings.TimeoutSettings{Enabled: true, TimeoutMin: 45, WarningMin: 5},
	}
}

// useSettingsTestStore makes a new BoltDB the store, holding
// settingsTestStored, and returns it.
func useSettingsTestStore(t *testing.T) *store.BoltDB {
	t.Helper()

	db := storetest.UseBolt(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("settings.SetDefaults() error = %v", err)
	}

	if err := settings.UpdateAllSettings(settingsTestStored()); err != nil {
		t.Fatalf("settings.UpdateAllSettings(%+v) error = %v", settingsTestStored(), err)
	}

	return db
}

// settingsTestDecode decodes the JSON body of w into v.
func settingsTestDecode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()

	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", w.Body.String(), err)
	}

	if ct := w.Header().Get("Content-Type"); ct != mimeJSON {
		t.Errorf("Content-Type = %q, want %q", ct, mimeJSON)
	}
}

// GET /settings, /settings/password and /settings/timeout answer the stored
// settings as JSON, and 500 with no body when the store cannot list them.
func TestGetSettingsHandlers(t *testing.T) {
	stored := settingsTestStored()

	handlers := map[string]struct {
		handle func(http.ResponseWriter, *http.Request) error
		want   any
		decode func(*testing.T, *httptest.ResponseRecorder) any
	}{
		"GetSettings": {
			handle: GetSettings,
			want:   stored,
			decode: func(t *testing.T, w *httptest.ResponseRecorder) any {
				t.Helper()

				var got settings.Settings
				settingsTestDecode(t, w, &got)

				return got
			},
		},
		"GetPasswordRequirements": {
			handle: GetPasswordRequirements,
			want:   stored.PasswordSettings,
			decode: func(t *testing.T, w *httptest.ResponseRecorder) any {
				t.Helper()

				var got settings.PasswordSettings
				settingsTestDecode(t, w, &got)

				return got
			},
		},
		"GetTimeoutSettings": {
			handle: GetTimeoutSettings,
			want:   stored.TimeoutSettings,
			decode: func(t *testing.T, w *httptest.ResponseRecorder) any {
				t.Helper()

				var got settings.TimeoutSettings
				settingsTestDecode(t, w, &got)

				return got
			},
		},
	}

	for name, h := range handlers {
		t.Run(name+"/stored", func(t *testing.T) {
			useSettingsTestStore(t)

			w := httptest.NewRecorder()

			err := h.handle(w, roleRequest(http.MethodGet, "/settings", "", nil, testRole("settings", "get")))
			if err != nil {
				t.Fatalf("%s() error = %v", name, err)
			}

			if diff := cmp.Diff(h.want, h.decode(t, w)); diff != "" {
				t.Errorf("%s() body mismatch (-want +got):\n%s", name, diff)
			}
		})

		t.Run(name+"/store fails", func(t *testing.T) {
			storetest.Use(t, settingsTestStore{Store: storetest.NewBolt(t), listErr: errors.New("store closed")})

			w := httptest.NewRecorder()
			err := h.handle(w, roleRequest(http.MethodGet, "/settings", "", nil, testRole("settings", "get")))

			if got := handlerStatus(t, w, err); got != http.StatusInternalServerError {
				t.Errorf("%s() status = %d, want %d", name, got, http.StatusInternalServerError)
			}

			if w.Body.Len() != 0 {
				t.Errorf("%s() wrote %q, want nothing", name, w.Body.String())
			}
		})
	}
}

// The settings defaults are stored once per process, on the first request for
// password or timeout settings; a later store gets none.
func TestEnsureSettingDefaultsStoresOnce(t *testing.T) {
	settingDefaultsOnce = sync.Once{}

	first := storetest.UseBolt(t)
	ensureSettingDefaults()

	got, err := first.List("Setting")
	if err != nil {
		t.Fatalf("store.List(Setting) error = %v", err)
	}

	if len(got) != len(settings.DefaultSettings) {
		t.Errorf("settings stored by the first ensureSettingDefaults() = %d, want %d", len(got), len(settings.DefaultSettings))
	}

	second := storetest.UseBolt(t)
	ensureSettingDefaults()

	if got, err := second.List("Setting"); err != nil || len(got) != 0 {
		t.Errorf("settings stored by a second ensureSettingDefaults() = %d (error %v), want 0", len(got), err)
	}
}

// POST /settings answers each failure with its status, and changes no setting
// for a minimum password length out of range or left out. When it cannot read
// the current settings, it applies the new ones but audits no changed names.
func TestSetSettings(t *testing.T) {
	const body = `{"password_settings":{"min_length":10},` +
		`"logging_settings":{"max_file_rotations":0,"max_file_size":100,"max_file_age":90},` +
		`"timeout_settings":{"enabled":false}}`

	changed := []string{
		"password_settings.lowercase_req", "password_settings.min_length", "password_settings.symbol_req",
		"password_settings.uppercase_req", "timeout_settings.enabled", "timeout_settings.timeout_min",
		"timeout_settings.warning_min",
	}

	tests := map[string]struct {
		update             bool
		body               io.Reader
		listErr, updateErr error
		want               int
		outcome            string
		fields             map[string]any
	}{
		"forbidden": {
			body: strings.NewReader(body), want: http.StatusForbidden,
			outcome: "failure", fields: map[string]any{"event.reason": "forbidden"},
		},
		"unreadable body": {update: true, body: settingsTestErrReader{}, want: http.StatusInternalServerError},
		"not JSON":        {update: true, body: strings.NewReader("{"), want: http.StatusBadRequest},
		"empty body":      {update: true, body: strings.NewReader(""), want: http.StatusBadRequest},
		"wrong field type": {
			update: true,
			body:   strings.NewReader(`{"password_settings":{"min_length":"10"}}`),
			want:   http.StatusBadRequest,
		},
		"min length too low": {
			update: true, body: strings.NewReader(`{"password_settings":{"min_length":3}}`), want: http.StatusBadRequest,
			outcome: "failure", fields: map[string]any{"event.reason": nil},
		},
		"min length too high": {
			update: true, body: strings.NewReader(`{"password_settings":{"min_length":1000}}`), want: http.StatusBadRequest,
			outcome: "failure", fields: map[string]any{"event.reason": nil},
		},
		"no min length": {
			update: true, body: strings.NewReader(`{"password_settings":{"uppercase_req":true},"timeout_settings":{"enabled":true}}`),
			want: http.StatusBadRequest, outcome: "failure", fields: map[string]any{"event.reason": nil},
		},
		"store update fails": {
			update: true, body: strings.NewReader(body), updateErr: errors.New("disk full"),
			want: http.StatusInternalServerError, outcome: "failure", fields: map[string]any{"phenix.settings.changed": changed},
		},
		"current settings unreadable": {
			update: true, body: strings.NewReader(body), listErr: errors.New("store closed"),
			want: http.StatusOK, outcome: "success", fields: map[string]any{"phenix.settings.changed": nil},
		},
		"changed": {
			update: true, body: strings.NewReader(body), want: http.StatusOK,
			outcome: "success", fields: map[string]any{"phenix.settings.changed": changed},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := useSettingsTestStore(t)
			storetest.Use(t, settingsTestStore{Store: db, listErr: tc.listErr, updateErr: tc.updateErr})

			events := captureAudit(t)

			role := testRole("settings", "get")
			if tc.update {
				role = testRole("settings", "update")
			}

			req := roleRequest(http.MethodPost, "/settings", "", nil, role)
			req.Body = io.NopCloser(tc.body)

			w := httptest.NewRecorder()
			if got := handlerStatus(t, w, SetSettings(w, req)); got != tc.want {
				t.Errorf("SetSettings(%s) status = %d, want %d", name, got, tc.want)
			}

			storetest.Use(t, db)

			got, err := settings.GetSettings()
			if err != nil {
				t.Fatalf("settings.GetSettings() error = %v", err)
			}

			want := settingsTestStored()
			if tc.want == http.StatusOK {
				want = settings.Settings{
					PasswordSettings: settings.PasswordSettings{MinLength: 10},
					LoggingSettings:  want.LoggingSettings,
					TimeoutSettings:  settings.TimeoutSettings{},
				}
			}

			if diff := cmp.Diff(want, *got); diff != "" {
				t.Errorf("settings after SetSettings(%s) mismatch (-want +got):\n%s", name, diff)
			}

			if tc.outcome == "" {
				if n := countAudit(events(), "settings-update", "failure", nil) +
					countAudit(events(), "settings-update", "success", nil); n != 0 {
					t.Errorf("SetSettings(%s) audited %d events, want none", name, n)
				}

				return
			}

			wantAudit(t, events(), "settings-update", tc.outcome, tc.fields)
		})
	}
}

// settingChanges names, in order, each setting whose value differs, and gives
// the new value of each.
func TestSettingChanges(t *testing.T) {
	t.Parallel()

	stored := settingsTestStored()
	changed := settingsTestStored()
	changed.PasswordSettings.MinLength = 20
	changed.TimeoutSettings.Enabled = false
	changed.LoggingSettings.MaxFileAge = 0

	none := []any{"phenix.settings.changed", []string{}, "phenix.settings.values", map[string]string{}}

	tests := map[string]struct {
		current, requested settings.Settings
		want               []any
	}{
		"same":        {current: stored, requested: stored, want: none},
		"zero values": {current: settings.Settings{}, requested: settings.Settings{}, want: none},
		"three changed": {
			current: stored, requested: changed,
			want: []any{
				"phenix.settings.changed",
				[]string{"logging_settings.max_file_age", "password_settings.min_length", "timeout_settings.enabled"},
				"phenix.settings.values",
				map[string]string{
					"logging_settings.max_file_age": "0", "password_settings.min_length": "20", "timeout_settings.enabled": "false",
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, settingChanges(tc.current, tc.requested)); diff != "" {
				t.Errorf("settingChanges() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// settingChanges names a setting only when the settings differ, and gives a
// value for each name.
func TestSettingChangesProperty(t *testing.T) {
	t.Parallel()

	gen := rapid.Custom(func(t *rapid.T) settings.Settings {
		return settings.Settings{
			PasswordSettings: settings.PasswordSettings{
				LowercaseReq: rapid.Bool().Draw(t, "lower"), UppercaseReq: rapid.Bool().Draw(t, "upper"),
				NumberReq: rapid.Bool().Draw(t, "number"), SymbolReq: rapid.Bool().Draw(t, "symbol"),
				MinLength: rapid.Int32Range(0, 3).Draw(t, "min"),
			},
			LoggingSettings: settings.LoggingSettings{MaxFileAge: rapid.Int32Range(0, 2).Draw(t, "age")},
			TimeoutSettings: settings.TimeoutSettings{
				Enabled: rapid.Bool().Draw(t, "enabled"), TimeoutMin: float64(rapid.IntRange(0, 2).Draw(t, "timeout")),
			},
		}
	})

	rapid.Check(t, func(t *rapid.T) {
		current, requested := gen.Draw(t, "current"), gen.Draw(t, "requested")

		got := settingChanges(current, requested)
		names, _ := got[1].([]string)
		values, _ := got[3].(map[string]string)

		if (len(names) == 0) != (current == requested) {
			t.Fatalf("settingChanges(%+v, %+v) names %v", current, requested, names)
		}

		if len(values) != len(names) {
			t.Fatalf("settingChanges() values %v for names %v", values, names)
		}

		for _, name := range names {
			if _, ok := values[name]; !ok {
				t.Fatalf("settingChanges() names %s without a value", name)
			}
		}
	})
}

// A setting whose name, but not its category, holds secret, token, password
// or key (in lower case) is secret.
func TestSecretSetting(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"password_settings.min_length":   false,
		"password_settings.number_req":   false,
		"timeout_settings.enabled":       false,
		"auth_settings.client_secret":    true,
		"auth_settings.api_token":        true,
		"smtp_settings.password":         true,
		"tls_settings.private_key":       true,
		"tls_settings.keyfile":           true,
		"secret_settings.enabled":        false,
		"no category":                    false,
		"":                               false,
		"a.b.password":                   true,
		"auth_settings.Password":         false,
		"password_settings.max_file_age": false,
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := secretSetting(name); got != want {
				t.Errorf("secretSetting(%q) = %t, want %t", name, got, want)
			}
		})
	}
}
