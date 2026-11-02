package settings_test

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/api/settings"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/types"
	v2 "phenix/types/version/v2"
)

var defaultPasswordSettings = settings.PasswordSettings{ //nolint:gochecknoglobals // test fixture
	LowercaseReq: true,
	UppercaseReq: false,
	NumberReq:    true,
	SymbolReq:    true,
	MinLength:    settings.DefaultPasswordMinLength,
}

func passwordSetting(name, value string) types.Setting {
	return types.Setting{
		Spec: &v2.Setting{Category: "Password", Name: name, Value: value},
	}
}

// useMemoryStore makes an in-memory store the phenix store until the test
// ends. It keeps each config as JSON, as BoltDB does, without disk writes.
func useMemoryStore(t *testing.T) {
	t.Helper()

	configs := map[string][]byte{}
	key := func(c *store.Config) string { return c.Kind + "/" + c.Metadata.Name }

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).DoAndReturn(func(kinds ...string) (store.Configs, error) {
		var list store.Configs

		for _, k := range slices.Sorted(maps.Keys(configs)) {
			var c store.Config
			if err := json.Unmarshal(configs[k], &c); err != nil {
				return nil, err
			}

			if slices.Contains(kinds, c.Kind) {
				list = append(list, c)
			}
		}

		return list, nil
	}).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		v, ok := configs[key(c)]
		if !ok {
			return store.ErrNotExist
		}

		return json.Unmarshal(v, c)
	}).AnyTimes()

	put := func(mustExist bool) func(c *store.Config) error {
		return func(c *store.Config) error {
			if _, ok := configs[key(c)]; ok != mustExist {
				if ok {
					return store.ErrExist
				}

				return store.ErrNotExist
			}

			v, err := json.Marshal(c)
			configs[key(c)] = v

			return err
		}
	}
	m.EXPECT().Create(gomock.Any()).DoAndReturn(put(false)).AnyTimes()
	m.EXPECT().Update(gomock.Any()).DoAndReturn(put(true)).AnyTimes()

	storetest.Use(t, m)
}

// useSettingsStore makes a new in-memory store the phenix store and stores
// the default settings in it, as the server does at startup.
func useSettingsStore(t *testing.T) {
	t.Helper()

	useMemoryStore(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("SetDefaults() error = %v", err)
	}
}

func useFailingStore(t *testing.T) {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).Return(nil, errors.New("store down")).AnyTimes()

	storetest.Use(t, m)
}

// TestGetPasswordSettingsDefaults checks that an empty store gives the
// default password settings.
func TestGetPasswordSettingsDefaults(t *testing.T) {
	storetest.UseBolt(t)

	got, err := settings.GetPasswordSettings()
	if err != nil {
		t.Fatalf("GetPasswordSettings() error = %v", err)
	}

	if diff := cmp.Diff(defaultPasswordSettings, got); diff != "" {
		t.Errorf("GetPasswordSettings() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetPasswordSettingsStoreError checks that a store error is returned.
func TestGetPasswordSettingsStoreError(t *testing.T) {
	useFailingStore(t)

	if _, err := settings.GetPasswordSettings(); err == nil {
		t.Error("GetPasswordSettings() error = nil, want an error")
	}
}

// TestGetPasswordSettingsFromList checks that only password settings with
// known names are read.
func TestGetPasswordSettingsFromList(t *testing.T) {
	list := []types.Setting{
		passwordSetting("NumberReq", "true"),
		passwordSetting("SymbolReq", "false"),
		passwordSetting("LowercaseReq", "true"),
		passwordSetting("UppercaseReq", "true"),
		passwordSetting("MinLength", "12"),
		passwordSetting("Unknown", "not a value"),
		{Spec: &v2.Setting{Category: "Logging", Name: "MinLength", Value: "99"}},
	}

	got, err := settings.GetPasswordSettingsFromList(list)
	if err != nil {
		t.Fatalf("GetPasswordSettingsFromList() error = %v", err)
	}

	want := settings.PasswordSettings{
		LowercaseReq: true, UppercaseReq: true, NumberReq: true, SymbolReq: false, MinLength: 12,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetPasswordSettingsFromList() mismatch (-want +got):\n%s", diff)
	}
}

// TestGetPasswordSettingsFromListParseErrors checks that a value that does
// not parse returns an error that names the setting.
func TestGetPasswordSettingsFromListParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"NumberReq", "yes please"},
		{"SymbolReq", ""},
		{"LowercaseReq", "2"},
		{"UppercaseReq", "maybe"},
		{"MinLength", "eight"},
		{"MinLength", "4294967296"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			_, err := settings.GetPasswordSettingsFromList([]types.Setting{passwordSetting(tt.name, tt.value)})
			if err == nil || !strings.Contains(err.Error(), "Password."+tt.name) {
				t.Errorf("GetPasswordSettingsFromList(%s=%q) error = %v, want an error naming Password.%s",
					tt.name, tt.value, err, tt.name)
			}
		})
	}
}

// FuzzUpdatePasswordSettings checks that valid settings read back exactly,
// and that settings with a minimum length out of range are rejected and none
// of them are stored.
func FuzzUpdatePasswordSettings(f *testing.F) {
	f.Add(true, true, true, true, int32(settings.MinPasswordMinLen))
	f.Add(false, false, false, false, int32(settings.MaxPasswordMinLen))
	f.Add(true, false, true, false, int32(settings.MinPasswordMinLen-1))
	f.Add(false, true, false, true, int32(settings.MaxPasswordMinLen+1))
	f.Add(true, true, false, false, int32(-1))

	f.Fuzz(func(t *testing.T, lower, upper, number, symbol bool, minLen int32) {
		useSettingsStore(t)

		before, err := settings.GetPasswordSettings()
		if err != nil {
			t.Fatalf("GetPasswordSettings() error = %v", err)
		}

		in := settings.PasswordSettings{
			LowercaseReq: lower, UppercaseReq: upper, NumberReq: number, SymbolReq: symbol, MinLength: minLen,
		}
		err = settings.UpdatePasswordSettings(in)

		got, getErr := settings.GetPasswordSettings()
		if getErr != nil {
			t.Fatalf("GetPasswordSettings() error = %v", getErr)
		}

		if minLen < settings.MinPasswordMinLen || minLen > settings.MaxPasswordMinLen {
			if err == nil {
				t.Errorf("UpdatePasswordSettings(%+v) error = nil, want an error", in)
			}

			if diff := cmp.Diff(before, got); diff != "" {
				t.Errorf("GetPasswordSettings() after rejected update mismatch (-want +got):\n%s", diff)
			}

			return
		}

		if err != nil {
			t.Fatalf("UpdatePasswordSettings(%+v) error = %v", in, err)
		}

		if diff := cmp.Diff(in, got); diff != "" {
			t.Errorf("GetPasswordSettings() after update mismatch (-want +got):\n%s", diff)
		}
	})
}

// FuzzIsPasswordValid checks IsPasswordValid against the password rules: at
// least the minimum number of characters, at most MaxPasswordBytes bytes, and
// each required character class.
func FuzzIsPasswordValid(f *testing.F) {
	atLimit := strings.Repeat("aA1!", settings.MaxPasswordBytes/4)

	f.Add(atLimit, true, true, true, true, uint8(0))
	f.Add(atLimit+"a", false, false, false, false, uint8(0))
	f.Add(strings.Repeat("aA1!é", 12), true, true, true, true, uint8(0))
	f.Add(strings.Repeat("aA1!é", 13), true, true, true, true, uint8(0))
	f.Add("short1!", true, false, true, true, uint8(0))
	f.Add("lowercase1!", true, true, true, true, uint8(0))
	f.Add("NoNumbers!!", true, true, true, true, uint8(3))
	f.Add("NoSymbols123", true, true, true, true, uint8(4))
	f.Add("ÄÖÜäöü١٢٣€€", true, true, true, true, uint8(0))
	f.Add("Äö١€", true, true, true, true, uint8(0))
	f.Add("", false, false, false, false, uint8(0))
	f.Add("\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8", false, false, false, false, uint8(0))

	f.Fuzz(func(t *testing.T, password string, lower, upper, number, symbol bool, minLenOffset uint8) {
		useSettingsStore(t)

		const span = settings.MaxPasswordMinLen - settings.MinPasswordMinLen + 1
		minLen := settings.MinPasswordMinLen + int32(minLenOffset)%span

		ps := settings.PasswordSettings{
			LowercaseReq: lower, UppercaseReq: upper, NumberReq: number, SymbolReq: symbol, MinLength: minLen,
		}
		if err := settings.UpdatePasswordSettings(ps); err != nil {
			t.Fatalf("UpdatePasswordSettings(%+v) error = %v", ps, err)
		}

		want := utf8.RuneCountInString(password) >= int(minLen) && len(password) <= settings.MaxPasswordBytes &&
			(!lower || strings.ContainsFunc(password, unicode.IsLower)) &&
			(!upper || strings.ContainsFunc(password, unicode.IsUpper)) &&
			(!number || strings.ContainsFunc(password, unicode.IsNumber)) &&
			(!symbol || strings.ContainsFunc(password, func(r rune) bool {
				return unicode.IsSymbol(r) || unicode.IsPunct(r)
			}))

		if got := settings.IsPasswordValid(password); got != want {
			t.Errorf("IsPasswordValid(%q) with %+v = %t, want %t", password, ps, got, want)
		}
	})
}

// TestIsPasswordValidStoreError checks that a password is not valid when the
// settings cannot be read.
func TestIsPasswordValidStoreError(t *testing.T) {
	useFailingStore(t)

	if settings.IsPasswordValid("aA1!aA1!aA1!") {
		t.Error("IsPasswordValid() = true with a failing store, want false")
	}
}

// TestGetPasswordSettingsHTML checks that the HTML lists the length rules
// and only the required character classes.
func TestGetPasswordSettingsHTML(t *testing.T) {
	tests := []struct {
		name string
		ps   settings.PasswordSettings
		want string
	}{
		{
			name: "no character classes",
			ps:   settings.PasswordSettings{MinLength: 10},
			want: "<ol><li>Password requires 10 characters</li>" +
				"<li>Password must be at most 72 bytes</li></ol>",
		},
		{
			name: "every character class",
			ps: settings.PasswordSettings{
				LowercaseReq: true, UppercaseReq: true, NumberReq: true, SymbolReq: true, MinLength: 8,
			},
			want: "<ol><li>Password requires 8 characters</li>" +
				"<li>Password must be at most 72 bytes</li>" +
				"<li>Password requires a lowercase letter</li>" +
				"<li>Password requires an uppercase letter</li>" +
				"<li>Password requires a number</li>" +
				"<li>Password requires a symbol</li></ol>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSettingsStore(t)

			if err := settings.UpdatePasswordSettings(tt.ps); err != nil {
				t.Fatalf("UpdatePasswordSettings(%+v) error = %v", tt.ps, err)
			}

			if got := settings.GetPasswordSettingsHTML(); got != tt.want {
				t.Errorf("GetPasswordSettingsHTML() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGetPasswordSettingsHTMLStoreError checks that the HTML is empty when
// the settings cannot be read.
func TestGetPasswordSettingsHTMLStoreError(t *testing.T) {
	useFailingStore(t)

	if got := settings.GetPasswordSettingsHTML(); got != "" {
		t.Errorf("GetPasswordSettingsHTML() = %q with a failing store, want \"\"", got)
	}
}

// TestUpdatePasswordSettingsStoreError checks that a failed store update
// returns an error that names the setting.
func TestUpdatePasswordSettingsStoreError(t *testing.T) {
	for _, name := range []string{"NumberReq", "SymbolReq", "LowercaseReq", "UppercaseReq", "MinLength"} {
		t.Run(name, func(t *testing.T) {
			m := store.NewMockStore(gomock.NewController(t))
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				c.Spec = map[string]any{"category": "Password", "value": "stale"}

				return nil
			}).AnyTimes()
			m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				if c.Metadata.Name == "Password."+name {
					return errors.New("disk full")
				}

				return nil
			}).AnyTimes()

			storetest.Use(t, m)

			err := settings.UpdatePasswordSettings(defaultPasswordSettings)
			if err == nil || !strings.Contains(err.Error(), "Settings."+name) {
				t.Errorf("UpdatePasswordSettings() error = %v, want an error naming Settings.%s", err, name)
			}
		})
	}
}
