package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"
)

// newSearchViper returns a viper that searches dir for name.
func newSearchViper(dir, name string) *viper.Viper {
	v := viper.New()
	v.SetConfigName(name)
	v.AddConfigPath(dir)

	return v
}

// writeTestFile writes data to name under dir and returns its path.
func writeTestFile(t *testing.T, dir, name, data string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}

	return path
}

// A config file that reads cleanly is used without a warning, and one that
// does not parse is ignored with a warning naming it, rather than silently.
func TestReadConfigReportsBadFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data     string
		wantRead bool
		wantWarn []string
	}{
		"valid": {data: "log:\n  level: debug\n", wantRead: true},
		"bad yaml": {
			data:     "log:\n  level: debug\n bad indent: [\n",
			wantWarn: []string{"phenix: warning: ignoring config file", "config.yaml"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeTestFile(t, dir, "config.yaml", tc.data)

			var warn bytes.Buffer

			v := newSearchViper(dir, "config")

			if got := readConfig(v, dir, &warn); got != tc.wantRead {
				t.Errorf("readConfig() = %t, want %t (warning %q)", got, tc.wantRead, warn.String())
			}

			if len(tc.wantWarn) == 0 && warn.Len() != 0 {
				t.Errorf("readConfig() warned %q, want no warning", warn.String())
			}

			for _, want := range tc.wantWarn {
				if !strings.Contains(warn.String(), want) {
					t.Errorf("readConfig() warning %q does not contain %q", warn.String(), want)
				}
			}
		})
	}
}

// With no config file, readConfig creates an empty one in the given
// directory, which it then reads, so the watcher has a file to watch.
func TestReadConfigCreatesMissingFile(t *testing.T) {
	t.Parallel()

	var (
		search = t.TempDir()
		create = filepath.Join(t.TempDir(), "phenix")
		warn   bytes.Buffer
	)

	v := newSearchViper(search, "config")
	v.AddConfigPath(create)

	if !readConfig(v, create, &warn) {
		t.Fatalf("readConfig() = false, want true (warning %q)", warn.String())
	}

	if warn.Len() != 0 {
		t.Errorf("readConfig() warned %q, want no warning", warn.String())
	}

	want := filepath.Join(create, "config.yaml")

	if diff := cmp.Diff(want, v.ConfigFileUsed()); diff != "" {
		t.Errorf("ConfigFileUsed() mismatch (-want +got):\n%s", diff)
	}
}

// When the config directory cannot be created, readConfig says so instead of
// carrying on without a word.
func TestReadConfigReportsUncreatableDirectory(t *testing.T) {
	t.Parallel()

	var (
		dir  = t.TempDir()
		file = writeTestFile(t, dir, "not-a-dir", "")
		warn bytes.Buffer
	)

	v := newSearchViper(dir, "config")

	if readConfig(v, filepath.Join(file, "phenix"), &warn) {
		t.Fatalf("readConfig() = true, want false")
	}

	if !strings.Contains(warn.String(), "phenix: warning: creating config directory") {
		t.Errorf("readConfig() warning = %q, want one about the config directory", warn.String())
	}
}

// A missing users file is normal and silent; one that does not parse is
// ignored with a warning.
func TestMergeConfigReportsBadFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data       string // no file when empty
		wantMerged bool
		wantWarn   bool
	}{
		"missing": {},
		"valid":   {data: "ui:\n  users: [admin:secret:global-admin]\n", wantMerged: true},
		"bad":     {data: "ui: [\n", wantWarn: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			if tc.data != "" {
				writeTestFile(t, dir, "users.yaml", tc.data)
			}

			var warn bytes.Buffer

			if got := mergeConfig(newSearchViper(dir, "users"), &warn); got != tc.wantMerged {
				t.Errorf("mergeConfig() = %t, want %t", got, tc.wantMerged)
			}

			if got := warn.Len() != 0; got != tc.wantWarn {
				t.Errorf("mergeConfig() warned %q, want a warning %t", warn.String(), tc.wantWarn)
			}
		})
	}
}
