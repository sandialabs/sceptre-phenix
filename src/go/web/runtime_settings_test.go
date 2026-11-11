package web

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"pgregory.net/rapid"

	"phenix/api/runtimesettings"
	"phenix/web/rbac"
)

// useRuntimeSettings has the routers the test builds serve the settings of a
// YAML config file with log.level, a flag for log.level and
// log.system.max-size, and the JWT signing key; it returns the file.
func useRuntimeSettings(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: debug\nui:\n  jwt-signing-key: hush\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}

	flags := pflag.NewFlagSet("phenix", pflag.ContinueOnError)
	flags.String("log.level", "info", "")
	flags.Int("log.system.max-size", 100, "")

	_ = v.BindPFlags(flags)

	original := o
	t.Cleanup(func() { o = original })

	o.runtimeSettings = runtimesettings.New(v,
		runtimesettings.WithFlags(flags.Lookup),
		runtimesettings.WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	return path
}

// The /runtimeSettings routes read the settings, with secrets hidden, and
// change the config file, for a role with the runtimeSettings permissions.
func TestRuntimeSettingsRoutes(t *testing.T) {
	var (
		admin  = combinedRole(testRole(resourceRuntimeSettings, "read"), testRole(resourceRuntimeSettings, "update"))
		reader = testRole(resourceRuntimeSettings, "read")
		viewer = combinedRole(testRole("*", "get", "*"), testRole("*", "list", "*"))
	)

	tests := map[string]struct {
		method, path, body string
		role               rbac.Role
		want               int
		wantBody           string // a part of the body, or ""
		wantFile           string // a part of the config file afterward, or ""
	}{
		"list": {method: "GET", path: "/runtimeSettings", role: reader, want: 200, wantBody: `credacted`},
		"list, no secret value": {
			method:   "GET",
			path:     "/runtimeSettings",
			role:     reader,
			want:     200,
			wantBody: `"key":"log.level","value":"debug","default":"info","source":"config file","restart":false`,
		},
		"get": {
			method:   "GET",
			path:     "/runtimeSettings/log.system.max-size",
			role:     reader,
			want:     200,
			wantBody: `"value":100`,
		},
		"get, unknown key": {method: "GET", path: "/runtimeSettings/no.such", role: reader, want: 404},
		"get, not a key":   {method: "GET", path: "/runtimeSettings/a..b", role: reader, want: 400},
		"get as a viewer":  {method: "GET", path: "/runtimeSettings", role: viewer, want: 403},
		"set": {
			method:   "PUT",
			path:     "/runtimeSettings/log.system.max-size",
			body:     `{"value":200}`,
			role:     admin,
			want:     200,
			wantBody: `"changed":true,"restart":false`,
			wantFile: "max-size: 200",
		},
		"set a text": {
			method:   "PUT",
			path:     "/runtimeSettings/ui.listen-endpoint",
			body:     `{"value":":3090"}`,
			role:     admin,
			want:     200,
			wantBody: `"restart":true`,
			wantFile: "listen-endpoint: :3090",
		},
		"set, wrong type": {
			method: "PUT",
			path:   "/runtimeSettings/log.system.max-size",
			body:   `{"value":"big"}`,
			role:   admin,
			want:   400,
		},
		"set, a list":     {method: "PUT", path: "/runtimeSettings/log.level", body: `{"value":["a"]}`, role: admin, want: 400},
		"set, no value":   {method: "PUT", path: "/runtimeSettings/log.level", body: `{}`, role: admin, want: 400},
		"set as a reader": {method: "PUT", path: "/runtimeSettings/log.level", body: `{"value":"warn"}`, role: reader, want: 403},
		"unset":           {method: "DELETE", path: "/runtimeSettings/log.level", role: admin, want: 200, wantBody: `"changed":true`},
		"unset, not in file": {
			method:   "DELETE",
			path:     "/runtimeSettings/ui.base-path",
			role:     admin,
			want:     200,
			wantBody: `"changed":false`,
		},
		"unset as a reader": {method: "DELETE", path: "/runtimeSettings/log.level", role: reader, want: 403},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			path := useRuntimeSettings(t)

			server := serveAs(t, "alice", tt.role).URL
			events := captureAudit(t)

			resp := do(t, tt.method, server+"/api/v1"+tt.path, tt.body)
			body := readBody(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("%s %s = %d, want %d: %s", tt.method, tt.path, resp.StatusCode, tt.want, body)
			}

			if tt.wantBody != "" && !strings.Contains(body, tt.wantBody) {
				t.Errorf("%s %s = %s, want it to contain %s", tt.method, tt.path, body, tt.wantBody)
			}

			if strings.Contains(body, "hush") {
				t.Errorf("%s %s = %s, want no JWT signing key in it", tt.method, tt.path, body)
			}

			if strings.Contains(body, `"changed":true`) {
				e := wantAudit(
					t,
					events(),
					"runtime-setting-change",
					"success",
					map[string]any{"phenix.setting.key": strings.TrimPrefix(tt.path, "/runtimeSettings/")},
				)
				// the timestamp can hold "200" by chance
				attrs := maps.Clone(e)
				delete(attrs, "time")

				if strings.Contains(fmt.Sprint(attrs), "200") || strings.Contains(fmt.Sprint(attrs), ":3090") {
					t.Errorf("%s %s audit = %v, want no value in it", tt.method, tt.path, e)
				}
			}

			if tt.wantFile == "" {
				return
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(data), tt.wantFile) {
				t.Errorf("config file after %s %s = %q, want it to contain %q", tt.method, tt.path, data, tt.wantFile)
			}
		})
	}
}

// runtimeSettingKey takes dotted parts of letters, digits, "-" and "_", and
// nothing else: no empty part, and no "/" that would reach another route.
func TestRuntimeSettingKeyProperty(t *testing.T) {
	part := rapid.StringMatching(`[A-Za-z0-9_-]{1,8}`)

	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(part, 1, 4).Draw(t, "parts")
		key := strings.Join(parts, ".")

		if !runtimeSettingKey.MatchString(key) {
			t.Fatalf("runtimeSettingKey refuses %q", key)
		}

		other := rapid.String().Draw(t, "other")

		valid := other != ""
		for p := range strings.SplitSeq(other, ".") {
			if p == "" || strings.ContainsFunc(p, func(r rune) bool {
				return r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
			}) {
				valid = false
			}
		}

		if got := runtimeSettingKey.MatchString(other); got != valid {
			t.Fatalf("runtimeSettingKey.MatchString(%q) = %t, want %t", other, got, valid)
		}
	})
}

// settingText takes a string as it is and a number or boolean as JSON writes
// it, and refuses any other value.
func TestSettingText(t *testing.T) {
	tests := []struct {
		raw, want string
		ok        bool
	}{
		{raw: `"debug"`, want: "debug", ok: true},
		{raw: `200`, want: "200", ok: true},
		{raw: `1.5`, want: "1.5", ok: true},
		{raw: `true`, want: "true", ok: true},
		{raw: `null`},
		{raw: `["a"]`},
		{raw: `{"a":1}`},
		{raw: ``},
	}

	for _, tt := range tests {
		got, err := settingText(json.RawMessage(tt.raw))
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("settingText(%s) = %q, %v; want %q and ok %t", tt.raw, got, err, tt.want, tt.ok)
		}
	}
}
