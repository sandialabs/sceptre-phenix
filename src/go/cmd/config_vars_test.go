package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/envsubst"
)

// configVarsTopology is a topology whose name and image are variables.
const configVarsTopology = "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: ${CFGVARS_SITE}\n" +
	"spec:\n  nodes:\n  - type: VirtualMachine\n    general:\n      hostname: host-00\n" +
	"    hardware:\n      os_type: linux\n      drives:\n      - image: ${CFGVARS_IMAGE:ubuntu}.qc2\n"

// configVarsImage returns the image of the first drive of the stored topology
// name, or the error of the read.
func configVarsImage(t *testing.T, name string) (string, error) {
	t.Helper()

	c, err := config.Get("topology/" + name)
	if err != nil {
		return "", err
	}

	nodes, _ := c.Spec["nodes"].([]any)
	node, _ := nodes[0].(map[string]any)
	hardware, _ := node["hardware"].(map[string]any)
	drives, _ := hardware["drives"].([]any)
	drive, _ := drives[0].(map[string]any)
	image, _ := drive["image"].(string)

	return image, nil
}

// config apply and config diff fill in the variables of their files from -e,
// an env file and the shell, and change nothing when a variable has a problem.
func TestConfigApplyAndDiffVariables(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)
	t.Setenv("CFGVARS_IMAGE", "from-the-shell")

	dir := writeConfigFiles(t, map[string]string{"t.yml": configVarsTopology})
	env := filepath.Join(writeConfigFiles(t, map[string]string{"site.env": "CFGVARS_SITE=demo\nCFGVARS_IMAGE=from-the-file\n"}), "site.env")

	problems := []struct {
		name string
		args []string
	}{
		{name: "a variable that is not set", args: []string{dir}},
		{name: "a -e variable that no file uses", args: []string{dir, "-e", "CFGVARS_SITE=demo", "-e", "CFGVARS_EXTRA=1"}},
	}

	for _, tt := range problems {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _, err := runConfigSubcommand(t, newConfigApplyCmd(), append([]string{"apply", "-f"}, tt.args...)...)
			if !errors.Is(err, errVariables) || stdout != "" {
				t.Errorf("config apply = %q, %v, want no output and errVariables", stdout, err)
			}

			_, _, err = runConfigSubcommand(t, newConfigDiffCmd(), append([]string{"diff"}, tt.args...)...)
			if !errors.Is(err, errVariables) || exitCode(err) != exitDiffError {
				t.Errorf("config diff = %v with exit code %d, want errVariables with exit code %d", err, exitCode(err), exitDiffError)
			}

			if _, err := config.Get("topology/demo"); !errors.Is(err, store.ErrNotExist) {
				t.Errorf("config.Get(topology/demo) error = %v, want %v", err, store.ErrNotExist)
			}
		})
	}

	_, _, err := runConfigSubcommand(t, newConfigDiffCmd(), "diff", dir, "-e", "not a name=x")
	if !errors.Is(err, envsubst.ErrBadFlag) || exitCode(err) != exitDiffError {
		t.Errorf("config diff with a bad -e = %v with exit code %d, want ErrBadFlag with exit code %d", err, exitCode(err), exitDiffError)
	}

	stdout, _, err := runConfigSubcommand(t, newConfigDiffCmd(), "diff", dir, "--env-file", env)
	if want := "topology/demo new\n"; stdout != want || exitCode(err) != exitDiffers {
		t.Errorf("config diff = %q with exit code %d, want %q with exit code %d", stdout, exitCode(err), want, exitDiffers)
	}

	// The env file overrides the shell, and -e overrides the env file.
	for _, tt := range []struct {
		args      []string
		want      string
		wantImage string
	}{
		{args: []string{"--env-file", env}, want: "topology/demo created\n", wantImage: "from-the-file.qc2"},
		{args: []string{"--env-file", env, "-e", "CFGVARS_IMAGE=kali"}, want: "topology/demo updated\n", wantImage: "kali.qc2"},
		{args: []string{"-e", "CFGVARS_SITE=demo"}, want: "topology/demo updated\n", wantImage: "from-the-shell.qc2"},
	} {
		stdout, stderr, err := runConfigSubcommand(t, newConfigApplyCmd(), append([]string{"apply", "-f", dir}, tt.args...)...)
		if err != nil || stderr != "" || stdout != tt.want {
			t.Errorf("config apply %q = %q, stderr %q, %v, want %q, nil", tt.args, stdout, stderr, err, tt.want)
		}

		if image, err := configVarsImage(t, "demo"); err != nil || image != tt.wantImage {
			t.Errorf("after config apply %q, the stored image = %q, %v, want %q", tt.args, image, err, tt.wantImage)
		}
	}
}

// config create checks the variables of every file before it stores a config,
// and does not use a PHENIX_ shell variable that -e does not name.
func TestConfigCreateVariables(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)
	t.Setenv("PHENIX_CFGVARS_IMAGE", "hidden")

	fixed := strings.NewReplacer("${CFGVARS_SITE}", "fixed", "${CFGVARS_IMAGE:ubuntu}", "ubuntu").Replace(configVarsTopology)
	hidden := strings.Replace(configVarsTopology, "${CFGVARS_IMAGE:ubuntu}", "${PHENIX_CFGVARS_IMAGE}", 1)
	dir := writeConfigFiles(t, map[string]string{"a.yml": fixed, "b.yml": hidden})
	first, second := filepath.Join(dir, "a.yml"), filepath.Join(dir, "b.yml")

	refused := [][]string{
		{first, second},
		{first, second, "-e", "CFGVARS_SITE=demo"},
		{first, filepath.Join(dir, "missing.yml")},
	}

	for _, args := range refused {
		if _, _, err := runConfigSubcommand(t, newConfigCreateCmd(), append([]string{"create"}, args...)...); err == nil {
			t.Errorf("config create %q error = nil, want an error", args)
		}

		if _, err := config.Get("topology/fixed"); !errors.Is(err, store.ErrNotExist) {
			t.Fatalf("after config create %q, config.Get(topology/fixed) error = %v, want %v", args, err, store.ErrNotExist)
		}
	}

	_, _, err := runConfigSubcommand(
		t, newConfigCreateCmd(), "create", first, second, "-e", "CFGVARS_SITE=demo", "-e", "PHENIX_CFGVARS_IMAGE",
	)
	if err != nil {
		t.Fatalf("config create with every variable set error = %v, want nil", err)
	}

	for name, want := range map[string]string{"fixed": "ubuntu.qc2", "demo": "hidden.qc2"} {
		if image, err := configVarsImage(t, name); err != nil || image != want {
			t.Errorf("the stored image of topology/%s = %q, %v, want %q", name, image, err, want)
		}
	}
}

// createFileConfigs stores the config of each value that names a file, with
// its variables filled in, and stores none when a variable has a problem.
func TestCreateFileConfigsVariables(t *testing.T) { //nolint:paralleltest // replaces the store
	storetest.UseBolt(t)

	scenario := "apiVersion: phenix.sandia.gov/v2\nkind: Scenario\nmetadata:\n  name: ${CFGVARS_SITE}-scn\n" +
		"  annotations:\n    topology: ${CFGVARS_SITE}\nspec:\n  apps: []\n"
	dir := writeConfigFiles(t, map[string]string{"topology.yml": configVarsTopology, "scenario.yaml": scenario})
	topologyFile, scenarioFile := filepath.Join(dir, "topology.yml"), filepath.Join(dir, "scenario.yaml")

	vars := func(flags ...string) *envsubst.Set {
		set, err := envsubst.NewSet(envsubst.Options{Flags: flags, EnvFiles: nil, DefaultEnvFile: "", Environ: nil, Reserved: ""})
		if err != nil {
			t.Fatalf("NewSet(%q) error = %v", flags, err)
		}

		return set
	}

	if _, err := createFileConfigs(t.Context(), vars(), topologyFile, scenarioFile); !errors.Is(err, errVariables) {
		t.Errorf("createFileConfigs with no variable error = %v, want errVariables", err)
	}

	if _, err := createFileConfigs(t.Context(), vars("CFGVARS_SITE=demo"), "stored", ""); !errors.Is(err, errVariables) {
		t.Errorf("createFileConfigs with a -e variable and no file error = %v, want errVariables", err)
	}

	if _, err := config.Get("topology/demo"); !errors.Is(err, store.ErrNotExist) {
		t.Fatalf("config.Get(topology/demo) error = %v, want %v", err, store.ErrNotExist)
	}

	names, err := createFileConfigs(t.Context(), vars("CFGVARS_SITE=demo"), topologyFile, scenarioFile)
	if err != nil || len(names) != 2 || names[0] != "demo" || names[1] != "demo-scn" {
		t.Fatalf("createFileConfigs = %q, %v, want [demo demo-scn], nil", names, err)
	}

	if image, err := configVarsImage(t, "demo"); err != nil || image != "ubuntu.qc2" {
		t.Errorf("the stored image of topology/demo = %q, %v, want ubuntu.qc2", image, err)
	}

	names, err = createFileConfigs(t.Context(), vars(), "demo", "")
	if err != nil || len(names) != 2 || names[0] != "demo" || names[1] != "" {
		t.Errorf("createFileConfigs with names only = %q, %v, want [demo \"\"], nil", names, err)
	}
}
