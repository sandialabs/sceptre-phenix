package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
)

const (
	applyCmdTopology = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: demo\n" +
		"spec:\n  nodes:\n  - type: VirtualMachine\n    general:\n      hostname: host-00\n" +
		"    hardware:\n      os_type: linux\n      drives:\n      - image: ubuntu.qc2\n"

	applyCmdScenario = "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: demo\n" +
		"  annotations:\n    topology: demo\nspec:\n  apps: []\n"
)

// writeConfigFiles writes each file under a new folder and returns the folder.
func writeConfigFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return dir
}

// runConfigSubcommand runs "phenix config <args>" with sub as the config
// subcommand, and returns its stdout, its stderr and its error.
func runConfigSubcommand(t *testing.T, sub *cobra.Command, args ...string) (string, string, error) {
	t.Helper()

	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	configCmd := newConfigCmd()
	configCmd.AddCommand(sub)
	root.AddCommand(configCmd)
	root.SetArgs(append([]string{"config"}, args...))

	var stdout, stderr bytes.Buffer

	root.SetOut(&stdout)
	root.SetErr(&stderr)

	_, err := root.ExecuteC()

	return stdout.String(), stderr.String(), err
}

// config apply prints one line for each config, in the order of their
// kinds, and a second apply of the same files changes nothing.
func TestConfigApply(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	dir := writeConfigFiles(t, map[string]string{"a-scenario.yml": applyCmdScenario, "b-topology.yml": applyCmdTopology})

	stdout, stderr, err := runConfigSubcommand(t, newConfigApplyCmd(), "apply", "-f", dir, "--dry-run")
	if err != nil || stderr != "" {
		t.Fatalf("config apply --dry-run = %v, stderr %q, want nil", err, stderr)
	}

	if want := "would create configuration topology/demo\nwould create configuration scenario/demo\n"; stdout != want {
		t.Errorf("config apply --dry-run printed %q, want %q", stdout, want)
	}

	if _, err := config.Get("topology/demo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(topology/demo) after a dry run error = %v, want %v", err, store.ErrNotExist)
	}

	for _, want := range []string{
		"topology/demo created\nscenario/demo created\n",
		"topology/demo unchanged\nscenario/demo unchanged\n",
	} {
		stdout, stderr, err := runConfigSubcommand(t, newConfigApplyCmd(), "apply", "-f", dir)
		if err != nil || stderr != "" || stdout != want {
			t.Errorf("config apply = %q, stderr %q, %v, want %q, nil", stdout, stderr, err, want)
		}
	}

	changed := writeConfigFiles(t, map[string]string{"t.yml": strings.Replace(applyCmdTopology, "ubuntu", "kali", 1)})

	stdout, _, err = runConfigSubcommand(t, newConfigApplyCmd(), "apply", "-f", filepath.Join(changed, "t.yml"))
	if want := "topology/demo updated\n"; err != nil || stdout != want {
		t.Errorf("config apply of a changed topology = %q, %v, want %q, nil", stdout, err, want)
	}
}

// config apply tries every config, prints the error of each that failed,
// and then fails.
func TestConfigApplyFailures(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	dir := writeConfigFiles(t, map[string]string{
		"a.yml":    applyCmdTopology,
		"b.yml":    applyCmdTopology,
		"user.yml": "apiVersion: phenix.sandia.gov/v1\nkind: User\nmetadata:\n  name: alice\nspec: {}\n",
		"bad.yml": strings.Replace(
			strings.Replace(applyCmdTopology, "name: demo", "name: bad", 1),
			"os_type: linux",
			"os_type: [linux]",
			1,
		),
		"broken.yml": "kind: [\n",
	})
	stdout, stderr, err := runConfigSubcommand(t, newConfigApplyCmd(), "apply", "-f", dir)
	if err == nil {
		t.Fatal("config apply with bad files error = nil, want an error")
	}

	if want := "topology/demo created\n"; stdout != want {
		t.Errorf("config apply with bad files printed %q, want %q", stdout, want)
	}

	for _, want := range []string{
		"broken.yml: error: invalid formatting",
		"topology/demo: error: " + filepath.Join(dir, "a.yml") + " also defines topology/demo",
		"user/alice: error: config kind cannot be applied",
		"topology/bad: error: ",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("config apply stderr = %q, want it to contain %q", stderr, want)
		}
	}

	if got := strings.Count(stderr, ": error: "); got != 4 {
		t.Errorf("config apply printed %d errors, want 4:\n%s", got, stderr)
	}
}

// config apply needs -f and takes no arguments.
func TestConfigApplyArgs(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	for _, args := range [][]string{{"apply"}, {"apply", "x.yml"}} {
		if _, _, err := runConfigSubcommand(t, newConfigApplyCmd(), args...); err == nil {
			t.Errorf("config %q error = nil, want an error", args)
		}
	}
}

// config diff prints a diff for a changed config and "new" for one not
// stored, and exits with 0, 1 or 2 as diff does.
func TestConfigDiff(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	if _, err := config.Create(config.CreateFromYAML([]byte(applyCmdTopology))); err != nil {
		t.Fatalf("config.Create(topology) error = %v", err)
	}

	same := writeConfigFiles(t, map[string]string{"t.yml": applyCmdTopology})
	changed := writeConfigFiles(t, map[string]string{
		"t.yml": strings.Replace(applyCmdTopology, "ubuntu", "kali", 1), "s.yml": applyCmdScenario,
	})

	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantCode   int
	}{
		{name: "the same", args: []string{same}, wantStdout: "", wantCode: 0},
		{
			name: "changed and new",
			args: []string{changed},
			wantStdout: "--- stored topology/demo\n+++ " + filepath.Join(changed, "t.yml") + "\n" +
				"@@ -8,6 +8,6 @@\n" +
				"         hostname: host-00\n       hardware:\n         drives:\n" +
				"-          - image: ubuntu.qc2\n+          - image: kali.qc2\n" +
				"         os_type: linux\n       type: VirtualMachine\n" +
				"scenario/demo new\n",
			wantCode: exitDiffers,
		},
		{name: "a missing file", args: []string{filepath.Join(same, "nope.yml")}, wantCode: exitDiffError},
		{name: "no files", args: nil, wantCode: exitDiffError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, _, err := runConfigSubcommand(t, newConfigDiffCmd(), append([]string{"diff"}, test.args...)...)

			code := 0
			if err != nil {
				code = exitCode(err)
			}

			if code != test.wantCode {
				t.Errorf("config diff %q exit code = %d (%v), want %d", test.args, code, err, test.wantCode)
			}

			if diff := cmp.Diff(test.wantStdout, stdout); diff != "" {
				t.Errorf("config diff %q stdout mismatch (-want +got):\n%s", test.args, diff)
			}
		})
	}
}

// applyCancelKey is the context key of the cancel function that the cancel
// hook calls in the create of topology/stop-here.
type applyCancelKey struct{}

var registerApplyCancelHook sync.Once //nolint:gochecknoglobals // hooks are global; register it once

// A signal during the apply of a config stops config apply. The error counts
// only the configs that it applied, and says that the command was canceled,
// also when the canceled config is the last one.
func TestConfigApplyCanceled(t *testing.T) { //nolint:paralleltest // replaces the store
	registerApplyCancelHook.Do(func() {
		config.RegisterConfigHook("Topology", func(ctx context.Context, stage string, c *store.Config) error {
			cancel, ok := ctx.Value(applyCancelKey{}).(context.CancelFunc)
			if !ok || stage != "create" || c.Metadata.Name != "stop-here" {
				return nil
			}

			cancel()

			return ctx.Err()
		})
	})

	topology := func(name string) fileConfig {
		c, err := store.NewConfigFromYAML([]byte(strings.Replace(applyCmdTopology, "name: demo", "name: "+name, 1)))
		if err != nil {
			t.Fatalf("decoding topology %s: %v", name, err)
		}

		return fileConfig{path: name + ".yml", name: "topology/" + name, config: c}
	}

	tests := []struct {
		name    string
		configs []string
		wantErr string
		wantOut string
	}{
		{
			name:    "last",
			configs: []string{"first", "stop-here"},
			wantErr: "applied 1 of 2 configurations: the command was canceled: context canceled",
			wantOut: "topology/first created\n",
		},
		{
			name:    "not last",
			configs: []string{"stop-here", "after"},
			wantErr: "applied 0 of 2 configurations: the command was canceled: context canceled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.UseBolt(t)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			ctx = context.WithValue(ctx, applyCancelKey{}, cancel)

			configs := make([]fileConfig, 0, len(tc.configs))
			for _, name := range tc.configs {
				configs = append(configs, topology(name))
			}

			var stdout, stderr bytes.Buffer

			dry := addDryRunFlag(&cobra.Command{})

			err := applyConfigs(ctx, &stdout, &stderr, configs, dry, config.ApplyWithContext(ctx))
			if err == nil || err.Error() != tc.wantErr || !errors.Is(err, context.Canceled) {
				t.Errorf("applyConfigs() error = %v, want %q that wraps context.Canceled", err, tc.wantErr)
			}

			if got := stdout.String(); got != tc.wantOut {
				t.Errorf("applyConfigs() stdout = %q, want %q", got, tc.wantOut)
			}

			if want := "topology/stop-here: canceled\n"; stderr.String() != want {
				t.Errorf("applyConfigs() stderr = %q, want %q", stderr.String(), want)
			}

			for _, name := range []string{"topology/stop-here", "topology/after"} {
				if _, err := config.Get(name); !errors.Is(err, store.ErrNotExist) {
					t.Errorf("config.Get(%s) error = %v, want %v", name, err, store.ErrNotExist)
				}
			}
		})
	}
}
