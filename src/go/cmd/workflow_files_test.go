package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigs(t *testing.T) {
	logs := captureLogs(t)
	dir := t.TempDir()
	topologyJSON := `{"apiVersion": "phenix.sandia.gov/v1", "kind": "Topology", "metadata": {"name": "z"}, "spec": {"nodes": []}}`
	lowerTopology := strings.Replace(wfTopologyYAML, "kind: Topology", "kind: topology", 1)
	imageYAML := "apiVersion: phenix.sandia.gov/v1\nkind: Image\nmetadata:\n  name: img\nspec: {}\n"
	roleYAML := "apiVersion: phenix.sandia.gov/v1\nkind: Role\nmetadata:\n  name: r\nspec: {}\n"

	writeWorkflowTree(t, dir, map[string]string{
		"README.md":        "# not a config\n",
		"a-scenario.yml":   wfScenarioYAML,
		"c-image.yaml":     imageYAML,
		"d-role.YAML":      roleYAML,
		"notes.txt":        "\tnot yaml",
		"sub/topology.yml": lowerTopology,
		"z-topology.json":  topologyJSON,
		".hidden.yml":      "\tnot yaml",
		".git/config":      "\tnot yaml",
	})

	got, err := loadConfigs(dir)
	if err != nil {
		t.Fatalf("loadConfigs() error = %v", err)
	}

	want := []configFile{
		{Path: filepath.Join(dir, "sub", "topology.yml"), Kind: "topology", ContentType: mimeYAML, Body: []byte(lowerTopology)},
		{Path: filepath.Join(dir, "z-topology.json"), Kind: kindTopology, ContentType: mimeJSON, Body: []byte(topologyJSON)},
		{Path: filepath.Join(dir, "a-scenario.yml"), Kind: "Scenario", ContentType: mimeYAML, Body: []byte(wfScenarioYAML)},
		{Path: filepath.Join(dir, "c-image.yaml"), Kind: "Image", ContentType: mimeYAML, Body: []byte(imageYAML)},
		{Path: filepath.Join(dir, "d-role.YAML"), Kind: "Role", ContentType: mimeYAML, Body: []byte(roleYAML)},
	}

	if !reflect.DeepEqual(got, want) {
		for _, file := range got {
			t.Logf("got %s kind=%q type=%q body=%q", file.Path, file.Kind, file.ContentType, file.Body)
		}

		t.Fatal("loadConfigs() did not return the expected files in upsert order")
	}

	skipped := logs.all(t, "skipping a file that is not a config")
	if len(skipped) != 2 {
		t.Fatalf("got %d skipped-file records, want 2 (README.md and notes.txt)", len(skipped))
	}

	for i, name := range []string{"README.md", "notes.txt"} {
		checkFields(t, skipped[i], map[string]string{"level": "DEBUG", "step": "preflight", "file": filepath.Join(dir, name)})
	}
}

func TestCompareConfigs(t *testing.T) {
	topologyA := configFile{Path: "/a.yml", Kind: kindTopology}
	topologyB := configFile{Path: "/b.yml", Kind: "topology"}
	scenarioA := configFile{Path: "/a.yml", Kind: "Scenario"}
	scenarioB := configFile{Path: "/b.yml", Kind: "Scenario"}

	tests := []struct {
		name string
		a, b configFile
		want int
	}{
		{name: "a topology sorts before the rest", a: topologyB, b: scenarioA, want: -1},
		{name: "the rest sort after a topology", a: scenarioA, b: topologyB, want: 1},
		{name: "topologies sort by path", a: topologyA, b: topologyB, want: -1},
		{name: "the rest sort by path", a: scenarioB, b: scenarioA, want: 1},
		{name: "same path", a: scenarioA, b: scenarioA, want: 0},
	}

	for _, tt := range tests {
		if got := compareConfigs(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: compareConfigs() = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestLoadConfigsErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "syntax error has the line",
			content: "metadata:\n\tname: x\n",
			want:    []string{"bad.yml:2: found character that cannot start any token"},
		},
		{
			name:    "syntax error on the first line",
			content: "\tkind: Topology\n",
			want:    []string{"bad.yml: found character that cannot start any token"},
		},
		{
			name:    "missing kind has line and column",
			content: "apiVersion: phenix.sandia.gov/v1\nmetadata:\n  name: foo\n",
			want:    []string{"bad.yml:1:1: missing kind"},
		},
		{
			name:    "kind must be a string",
			content: "apiVersion: phenix.sandia.gov/v1\nkind: [a]\n",
			want:    []string{"bad.yml:2:7: kind must be a non-empty string"},
		},
		{name: "kind must not be empty", content: "kind: \"\"\n", want: []string{"bad.yml:1:7: kind must be a non-empty string"}},
		{
			name:    "second document",
			content: wfTopologyYAML + "---\n" + wfScenarioYAML,
			want:    []string{"bad.yml:7:1: found a second YAML document"},
		},
		{
			name:    "syntax error in a second document",
			content: wfTopologyYAML + "---\nkind: Topology\nspec: {a: b\n",
			want:    []string{"bad.yml:", "did not find expected ',' or '}'"},
		},
		{name: "top level must be a mapping", content: "- a\n- b\n", want: []string{"bad.yml:1:1: expected a mapping at the top level"}},
		{name: "empty file", content: "", want: []string{"bad.yml: no YAML document"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkflowTree(t, dir, map[string]string{"bad.yml": tt.content})

			_, err := loadConfigs(dir)
			if err == nil {
				t.Fatal("loadConfigs() error = nil, want a parse error")
			}

			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("loadConfigs() error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestLoadConfigsSymlinks checks that a symlink to a regular file is read as
// a config, and that a symlink to a directory, whatever its name, is skipped
// with a warning: the walk does not follow it, so its configs are not
// deployed.
func TestLoadConfigsSymlinks(t *testing.T) {
	logs := captureLogs(t)
	dir := t.TempDir()
	outside := t.TempDir()

	writeWorkflowTree(t, dir, map[string]string{"topology.yml": wfTopologyYAML})
	writeWorkflowTree(t, outside, map[string]string{"scenario.yml": wfScenarioYAML, "shared/image.yml": wfTopologyYAML})

	for link, target := range map[string]string{
		"linked.yml": filepath.Join(outside, "scenario.yml"),
		"shared":     filepath.Join(outside, "shared"),
		"link.yml":   filepath.Join(outside, "shared"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatalf("creating symlink %s: %v", link, err)
		}
	}

	got, err := loadConfigs(dir)
	if err != nil {
		t.Fatalf("loadConfigs() error = %v", err)
	}

	want := []configFile{
		{Path: filepath.Join(dir, "topology.yml"), Kind: kindTopology, ContentType: mimeYAML, Body: []byte(wfTopologyYAML)},
		{Path: filepath.Join(dir, "linked.yml"), Kind: "Scenario", ContentType: mimeYAML, Body: []byte(wfScenarioYAML)},
	}

	if !reflect.DeepEqual(got, want) {
		for _, file := range got {
			t.Logf("got %s kind=%q", file.Path, file.Kind)
		}

		t.Fatal("loadConfigs() did not return the topology and the symlinked file")
	}

	skipped := logs.all(t, "skipping a symlinked directory; its configs are not deployed")
	if len(skipped) != 2 {
		t.Fatalf("got %d symlinked-directory records, want 2 (link.yml and shared)", len(skipped))
	}

	for i, name := range []string{"link.yml", "shared"} {
		checkFields(t, skipped[i], map[string]string{
			"level": "WARN", "type": "SYSTEM", "step": "preflight", "file": filepath.Join(dir, name),
		})
	}
}

func TestLoadConfigsReadErrors(t *testing.T) {
	t.Run("unreadable directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}

		dir := t.TempDir()
		sub := filepath.Join(dir, "sub")

		if err := os.Mkdir(sub, 0o000); err != nil {
			t.Fatalf("creating %s: %v", sub, err)
		}

		t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

		if _, err := loadConfigs(dir); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("loadConfigs() error = %v, want a permission error", err)
		}
	})
}

// TestParseConfigFileLimits checks that parseConfigFile reads only regular
// files of a bounded size, so a symlink to a device or a large file cannot
// exhaust memory.
func TestParseConfigFileLimits(t *testing.T) {
	t.Run("symlink to a directory", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "link.yml")

		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatalf("creating symlink: %v", err)
		}

		_, err := parseConfigFile(link, true)
		if err == nil || err.Error() != link+": not a regular file" {
			t.Fatalf("parseConfigFile() error = %v, want %q", err, link+": not a regular file")
		}
	})

	t.Run("symlink to a device", func(t *testing.T) {
		if _, err := os.Stat("/dev/zero"); err != nil {
			t.Skip("/dev/zero is not available")
		}

		dir := t.TempDir()
		link := filepath.Join(dir, "zero.yml")

		if err := os.Symlink("/dev/zero", link); err != nil {
			t.Fatalf("creating symlink: %v", err)
		}

		// A regular-file check must refuse the device before any read, so the
		// call returns at once and allocates nothing.
		_, err := parseConfigFile(link, true)
		if err == nil || err.Error() != link+": not a regular file" {
			t.Fatalf("parseConfigFile() error = %v, want %q", err, link+": not a regular file")
		}
	})

	t.Run("stat error keeps the reading config wording", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone.yml")

		_, err := parseConfigFile(missing, true)
		if err == nil || !strings.HasPrefix(err.Error(), "reading config: ") {
			t.Fatalf("parseConfigFile() error = %v, want a reading config error", err)
		}
	})

	t.Run("open error keeps the reading config wording", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores file permissions")
		}

		dir := t.TempDir()
		path := filepath.Join(dir, "denied.yml")

		if err := os.WriteFile(path, []byte(wfTopologyYAML), 0o000); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

		_, err := parseConfigFile(path, true)
		if err == nil || !strings.HasPrefix(err.Error(), "reading config: ") {
			t.Fatalf("parseConfigFile() error = %v, want a reading config error", err)
		}
	})
}

func TestParseConfigFileWithoutKind(t *testing.T) {
	dir := t.TempDir()
	writeWorkflowTree(t, dir, map[string]string{"workflow.json": `{"spec": {}}`})

	got, err := parseConfigFile(filepath.Join(dir, "workflow.json"), false)
	if err != nil {
		t.Fatalf("parseConfigFile() error = %v", err)
	}

	if got.Kind != "" || got.ContentType != mimeJSON || string(got.Body) != `{"spec": {}}` {
		t.Errorf("parseConfigFile() = kind %q type %q body %q", got.Kind, got.ContentType, got.Body)
	}
}

// TestParseConfigFileAccepts checks files that the local syntax check must
// pass: placeholders the raw YAML can't hold, and what only the server checks.
func TestParseConfigFileAccepts(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		content  string
		wantKind string
	}{
		{
			name:     "placeholder as a JSON number",
			file:     "image.json",
			content:  `{"apiVersion": "phenix.sandia.gov/v1", "kind": "Image", "metadata": {"name": "img"}, "spec": {"size": ${SIZE}}}`,
			wantKind: "Image",
		},
		{
			name:     "placeholder in a flow mapping",
			file:     "topology.yml",
			content:  "kind: Topology\nspec:\n  vlans: {mgmt: ${VLAN}}\n",
			wantKind: kindTopology,
		},
		{
			name:     "placeholder default with a colon",
			file:     "topology.yml",
			content:  "kind: Topology\nspec:\n  image: ${IMG: ubuntu.qc2}\n",
			wantKind: kindTopology,
		},
		{name: "placeholder kind is left to the server", file: "topology.yml", content: "kind: ${KIND}\n"},
		{name: "kind in another case", file: "topology.yml", content: "kind: topology\n", wantKind: "topology"},
		{name: "a kind the server does not store is left to it", file: workflowConfigName, content: wfWorkflowYAML, wantKind: "Workflow"},
		{
			name:     "a name with a slash is left to the server",
			file:     "topology.yml",
			content:  strings.Replace(wfTopologyYAML, "name: foo", "name: a/b", 1),
			wantKind: kindTopology,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkflowTree(t, dir, map[string]string{tt.file: tt.content})

			got, err := parseConfigFile(filepath.Join(dir, tt.file), true)
			if err != nil {
				t.Fatalf("parseConfigFile() error = %v", err)
			}

			if string(got.Body) != tt.content || got.ContentType != contentTypeFor(tt.file) {
				t.Errorf("parseConfigFile() = type %q body %q, want the file as read", got.ContentType, got.Body)
			}

			if tt.wantKind != "" && got.Kind != tt.wantKind {
				t.Errorf("parseConfigFile() kind = %q, want %q", got.Kind, tt.wantKind)
			}
		})
	}
}

func TestNeutralizePlaceholders(t *testing.T) {
	tests := map[string]string{
		"mask: ${MASK}\n":             "mask: xxxxxxx\n",
		"image: ${IMG: ubuntu.qc2}\n": "image: xxxxxx xxxxxxxxxxx\n",
		"a: ${A:x\n  y}\nb: 1\n":      "a: xxxxx\n  xx\nb: 1\n",
		"cost: $5 and ${not a var}\n": "cost: $5 and ${not a var}\n",
	}

	for body, want := range tests {
		if got := string(neutralizePlaceholders([]byte(body))); got != want {
			t.Errorf("neutralizePlaceholders(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := map[string]string{
		"topology.json": "application/json",
		"TOPOLOGY.JSON": "application/json",
		"topology.yml":  "application/x-yaml",
		"topology.yaml": "application/x-yaml",
		"topology":      "application/x-yaml",
	}

	for path, want := range tests {
		if got := contentTypeFor(path); got != want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", path, got, want)
		}
	}
}
