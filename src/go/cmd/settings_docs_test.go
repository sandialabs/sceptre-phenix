package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"phenix/api/runtimesettings"
)

const (
	settingsDocPath = "../../../docs/content/settings.md"
	readmePath      = "../../../README.md"
)

// documentedDefaults holds the defaults the docs show in place of the flag's own: a path derived
// from another setting, or the path a root user gets.
var documentedDefaults = map[string]string{ //nolint:gochecknoglobals // test table
	"base-dir.injects":           "<base-dir.phenix>/injects",
	"base-dir.topologies":        "<base-dir.phenix>/topologies",
	"base-dir.builder-templates": "<base-dir.phenix>/builder/templates",
	"mount-dir":                  "<base-dir.phenix>/mounts",
	"store.endpoint":             rootDefaultStore,
	"log.system.path":            rootDefaultLogPath,
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// documentedFlag is what a settings table says about one flag or setting.
type documentedFlag struct {
	Type    string
	Env     string
	Default string
}

func TestGlobalFlagsMatchSettingsDocs(t *testing.T) {
	got := make(map[string]documentedFlag)

	for _, row := range docTable(t, settingsDocPath, "Global Flags", 4) {
		name := flagName(row[0])
		if name == "help" {
			continue
		}

		got[name] = documentedFlag{Type: firstSpan(row[1]), Default: firstSpan(row[2])}
	}

	want := make(map[string]documentedFlag)

	visitGlobalFlags(func(f *pflag.Flag) {
		want[f.Name] = documentedFlag{Type: f.Value.Type(), Default: flagDefault(f.Name, f)}
	})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s Global Flags table does not match the root flags (-flags +docs):\n%s", settingsDocPath, diff)
	}
}

func TestSettingsReferenceMatchesFlags(t *testing.T) {
	for _, tc := range []struct {
		path    string
		heading string
	}{
		{settingsDocPath, "Settings Reference"},
		{readmePath, "Settings Reference"},
	} {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			var (
				got  = make(map[string]documentedFlag)
				want = make(map[string]documentedFlag)
			)

			for _, row := range docTable(t, tc.path, tc.heading, 4) {
				key := firstSpan(row[0])
				got[key] = documentedFlag{Env: firstSpan(row[1]), Default: firstSpan(row[2])}

				f := settingFlag(rootCmd, viper.GetViper(), key)
				if f == nil {
					t.Errorf("%s documents setting %q, which no flag sets", tc.path, key)

					continue
				}

				want[key] = documentedFlag{Env: envVar(key), Default: flagDefault(key, f)}
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%s Settings Reference does not match the flags (-flags +docs):\n%s", tc.path, diff)
			}
		})
	}
}

// docTable returns the cells of the first Markdown table under heading in path, one slice of
// columns cells per row; the last cell holds the rest of the row.
func docTable(t *testing.T, path, heading string, columns int) [][]string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var (
		rows      [][]string
		inSection bool
		seenRows  int // table lines read, header and separator included
		scanner   = bufio.NewScanner(f)
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		switch {
		case strings.HasPrefix(line, "#"):
			if seenRows > 0 {
				return rows
			}

			inSection = strings.TrimSpace(strings.TrimLeft(line, "#")) == heading
		case !inSection:
		case strings.HasPrefix(line, "|"):
			seenRows++
			if seenRows <= 2 {
				continue
			}

			cells := strings.SplitN(strings.Trim(line, "|"), "|", columns)
			if len(cells) != columns {
				t.Fatalf("%s: row %q has %d cells, want %d", path, line, len(cells), columns)
			}

			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}

			rows = append(rows, cells)
		case seenRows > 0:
			return rows
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	if len(rows) == 0 {
		t.Fatalf("%s has no table under the heading %q", path, heading)
	}

	return rows
}

// firstSpan returns a cell's first code span, or "" for a cell without one (such as "—") or
// with the empty string `""`.
func firstSpan(cell string) string {
	m := codeSpan.FindStringSubmatch(cell)
	if m == nil || m[1] == `""` {
		return ""
	}

	return m[1]
}

// flagName returns the long flag a cell such as "`-h, --help`" or "`--log.level`" names.
func flagName(cell string) string {
	spans := codeSpan.FindAllStringSubmatch(cell, -1)
	if len(spans) == 0 {
		return cell
	}

	fields := strings.Fields(spans[len(spans)-1][1])

	return strings.TrimPrefix(fields[len(fields)-1], "--")
}

func visitGlobalFlags(fn func(*pflag.Flag)) {
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			fn(f)
		}
	})
}

func flagDefault(key string, f *pflag.Flag) string {
	if d, ok := documentedDefaults[key]; ok {
		return d
	}

	if f.DefValue == "[]" {
		return ""
	}

	return f.DefValue
}

func envVar(key string) string {
	return "PHENIX_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(key))
}

// The docs helpers read the first code span of a cell, the long flag a cell
// names, the first table under a heading, and the variable viper reads.
func TestSettingsDocsHelpers(t *testing.T) {
	t.Parallel()

	spans := map[string]string{
		"`a` and `b`": "a", "—": "", "`\"\"`": "", "plain": "", "": "", "`ui.users` (secret)": "ui.users",
	}
	for cell, want := range spans {
		if got := firstSpan(cell); got != want {
			t.Errorf("firstSpan(%q) = %q, want %q", cell, got, want)
		}
	}

	flags := map[string]string{
		"`-h, --help`": "help", "`--log.level`": "log.level", "`-x` or `--no-print-settings`": "no-print-settings",
		"no span": "no span",
	}
	for cell, want := range flags {
		if got := flagName(cell); got != want {
			t.Errorf("flagName(%q) = %q, want %q", cell, got, want)
		}
	}

	for _, key := range append(knownSettings(), "ui.logs.minimega-path", "base-dir.phenix") {
		if got, want := envVar(key), runtimesettings.EnvVar(key); got != want {
			t.Errorf("envVar(%q) = %q, want %q, the variable doctor reads", key, got, want)
		}
	}

	doc := "# Title\n\n## Other\n\n| a | b |\n|---|---|\n| x | y |\n\n## Wanted\n\nSome text.\n\n" +
		"| Key | Rest |\n|---|---|\n| `k1` | one | two |\n|  `k2`  |  three  |\n\n| `after` | gap |\n\n## Next\n"
	path := writeTestFile(t, t.TempDir(), "doc.md", doc)

	want := [][]string{{"`k1`", "one | two"}, {"`k2`", "three"}}
	if diff := cmp.Diff(want, docTable(t, path, "Wanted", 2)); diff != "" {
		t.Errorf("docTable(Wanted) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([][]string{{"x", "y"}}, docTable(t, path, "Other", 2)); diff != "" {
		t.Errorf("docTable(Other) mismatch (-want +got):\n%s", diff)
	}
}
