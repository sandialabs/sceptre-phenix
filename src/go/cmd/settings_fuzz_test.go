package cmd

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/viper"
)

// The value that phenix settings set writes for any text reads back from
// the config file as phenix reads it: text kept as text is the same text,
// and a bool, int or float64 is the number or bool that the text names.
// Only text with a character that is not printable can give errTextNotYAML,
// which leaves the file as it was.
func FuzzSettingsCmdInferType(f *testing.F) {
	for _, seed := range []string{
		"debug", "true", "FALSE", "200", "-7", "007", "1.5", ".5", "1e5", "1.5e3", "NaN", "",
		"yes", "~", "null", "0x10", "a: b", "- x", "#c", " 1", "\xff", "line\nbreak", "'q'",
		"\n0", "\t\n1",
	} {
		f.Add(seed)
	}

	path := filepath.Join(f.TempDir(), "config.yaml")

	f.Fuzz(func(t *testing.T, text string) {
		got := inferType(text)

		if err := os.WriteFile(path, []byte("k: before\n"), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		err := setConfigValue(path, "k", got)
		if errors.Is(err, errTextNotYAML) {
			if utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool { return !unicode.IsPrint(r) }) {
				t.Errorf("setConfigValue(k, %q) = %v, want printable text written", text, err)
			}

			if data, _ := os.ReadFile(path); string(data) != "k: before\n" {
				t.Errorf("setConfigValue(k, %q) = %v and changed the file to %q", text, err, data)
			}

			return
		}

		if err != nil {
			t.Fatalf("setConfigValue(k, inferType(%q)) error = %v", text, err)
		}

		v := viper.New()
		v.SetConfigFile(path)

		if err := v.ReadInConfig(); err != nil {
			t.Fatalf("after setConfigValue(k, inferType(%q)), ReadInConfig() error = %v", text, err)
		}

		switch want := got.(type) {
		case string:
			if want != text {
				t.Fatalf("inferType(%q) = %q, want the text unchanged", text, want)
			}

			if read := v.GetString("k"); read != text {
				t.Errorf("config value for %q reads back as %q", text, read)
			}
		case bool:
			if !strings.EqualFold(text, strconv.FormatBool(want)) {
				t.Fatalf("inferType(%q) = %t, want only true or false to give a bool", text, want)
			}

			if read := v.GetBool("k"); read != want {
				t.Errorf("config value for %q reads back as %t, want %t", text, read, want)
			}
		case int:
			if n, err := strconv.ParseInt(text, 10, 64); err != nil || int(n) != want {
				t.Fatalf("inferType(%q) = %d, which is not the base 10 integer of the text", text, want)
			}

			if read := v.GetInt("k"); read != want {
				t.Errorf("config value for %q reads back as %d, want %d", text, read, want)
			}
		case float64:
			if n, err := strconv.ParseFloat(text, 64); err != nil || n != want || !strings.Contains(text, ".") {
				t.Fatalf("inferType(%q) = %v, want a float only for a number with a decimal point", text, want)
			}

			if read := v.GetFloat64("k"); read != want {
				t.Errorf("config value for %q reads back as %v, want %v", text, read, want)
			}
		default:
			t.Fatalf("inferType(%q) = %#v (%T), want a string, bool, int or float64", text, got, got)
		}
	})
}

// The value that phenix settings set writes for a known setting reads back
// from the config file as the value its flag gives the text. Text that the
// flag refuses gives errSettingType, and only text with a character that is
// not printable can give errTextNotYAML.
func FuzzSettingsCmdSettingValue(f *testing.F) {
	for _, seed := range []string{
		"debug", "0123", "1.50", "true", "0", "yes", "200", "-7", "0x10", "1_000", "90s", "24h", "3600", "",
		"a:b:admin,c:d:user", `"a,b",c`, "a\nb", "\"", "\n0", "\xff", "#c", "- x",
	} {
		f.Add(seed)
	}

	path := filepath.Join(f.TempDir(), "config.yaml")
	root := settingsCmdTypedRoot(viper.New())
	keys := []string{"log.level", "log.system.max-size", "use-gre-mesh", "ui.jwt-lifetime", "ui.users"}

	f.Fuzz(func(t *testing.T, text string) {
		printable := utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool { return !unicode.IsPrint(r) })

		for _, key := range keys {
			got, err := settingValue(root, viper.New(), key, text)
			if errors.Is(err, errSettingType) {
				if want := settingsCmdFlagParses(key, text); want {
					t.Errorf("settingValue(%s, %q) error = %v, want the text the flag accepts", key, text, err)
				}

				continue
			}

			if err != nil {
				t.Fatalf("settingValue(%s, %q) error = %v", key, text, err)
			}

			if err := os.WriteFile(path, []byte("k: before\n"), 0o600); err != nil {
				t.Fatalf("os.WriteFile() error = %v", err)
			}

			err = setConfigValue(path, key, got)
			if errors.Is(err, errTextNotYAML) {
				if printable {
					t.Errorf("setConfigValue(%s, %q) = %v, want printable text written", key, text, err)
				}

				continue
			}

			if err != nil {
				t.Fatalf("setConfigValue(%s, %#v) error = %v", key, got, err)
			}

			v := viper.New()
			v.SetConfigFile(path)

			if err := v.ReadInConfig(); err != nil {
				t.Fatalf("after setConfigValue(%s, %#v), ReadInConfig() error = %v", key, got, err)
			}

			settingsCmdCheckReadBack(t, v, key, text, got)
		}
	})
}

// settingsCmdFlagParses reports whether the flag of key in
// settingsCmdTypedRoot accepts text, as strconv and time parse it.
func settingsCmdFlagParses(key, text string) bool {
	var err error

	switch key {
	case "log.system.max-size":
		_, err = strconv.ParseInt(text, 0, strconv.IntSize)
	case "use-gre-mesh":
		_, err = strconv.ParseBool(text)
	case "ui.jwt-lifetime":
		_, err = time.ParseDuration(text)
	case "ui.users":
		_, err = csv.NewReader(strings.NewReader(text)).Read()
		if text == "" {
			err = nil
		}
	}

	return err == nil
}

// settingsCmdCheckReadBack checks that v reads the value settings set wrote
// for text at key as the flag of key gives it.
func settingsCmdCheckReadBack(t *testing.T, v *viper.Viper, key, text string, got any) {
	t.Helper()

	switch key {
	case "log.level":
		if read := v.GetString(key); got != text || read != text {
			t.Errorf("settings set %s %q wrote %#v and reads back as %q, want the text", key, text, got, read)
		}
	case "log.system.max-size":
		want, _ := strconv.ParseInt(text, 0, strconv.IntSize)
		if read := v.GetInt(key); read != int(want) {
			t.Errorf("settings set %s %q reads back as %d, want %d", key, text, read, want)
		}
	case "use-gre-mesh":
		want, _ := strconv.ParseBool(text)
		if read := v.GetBool(key); read != want {
			t.Errorf("settings set %s %q reads back as %t, want %t", key, text, read, want)
		}
	case "ui.jwt-lifetime":
		want, _ := time.ParseDuration(text)
		if read := v.GetDuration(key); read != want {
			t.Errorf("settings set %s %q reads back as %v, want %v", key, text, read, want)
		}
	case "ui.users":
		if diff := cmp.Diff(got, v.GetStringSlice(key), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("settings set %s %q: list read back mismatch (-want +got):\n%s", key, text, diff)
		}
	}
}
