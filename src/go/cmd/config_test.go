package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// TestConfigCommandsShowBuilderDocumentAsMap checks that config create, get and edit
// read and write the builder-doc annotation as a map, and that the hook refuses a bad one.
func TestConfigCommandsShowBuilderDocumentAsMap(t *testing.T) {
	storetest.UseBolt(t)

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	topology := func(name, reference string) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), name+".yaml")
		body := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: " + name +
			"\n  annotations:\n    builder-doc:\n" + reference + "spec:\n  nodes: []\n"

		//nolint:gosec // a config file the test creates
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		return path
	}

	run := func(args ...string) (string, error) {
		root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
		configCmd := newConfigCmd()
		configCmd.AddCommand(newConfigCreateCmd(), newConfigGetCmd(), newConfigEditCmd(), newConfigDeleteCmd())
		root.AddCommand(configCmd)
		root.SetArgs(append([]string{"config"}, args...))

		var output bytes.Buffer
		root.SetOut(&output)

		_, err := root.ExecuteC()

		return output.String(), err
	}

	file := topology("site", "      path: /phenix/topologies/site/builder.yaml\n      digest: "+digest+"\n")
	if _, err := run("create", file); err != nil {
		t.Fatalf("config create returned error: %v", err)
	}

	type shown struct {
		Metadata struct {
			Annotations map[string]any `json:"annotations" yaml:"annotations"`
		} `json:"metadata" yaml:"metadata"`
	}

	// reference returns the builder-doc annotation `phenix config get` shows.
	reference := func(unmarshal func([]byte, any) error, args ...string) any {
		t.Helper()

		output, err := run(append([]string{"get", "topology/site"}, args...)...)
		if err != nil {
			t.Fatalf("config get %v returned error: %v", args, err)
		}

		var config shown
		if err := unmarshal([]byte(output), &config); err != nil {
			t.Fatalf("config get %v output does not load: %v\n%s", args, err, output)
		}

		return config.Metadata.Annotations[builder.DocumentAnnotation]
	}

	want := map[string]any{"digest": digest, "path": "/phenix/topologies/site/builder.yaml"}

	for name, got := range map[string]any{
		"YAML": reference(yaml.Unmarshal),
		"JSON": reference(json.Unmarshal, "-o", "json"),
	} {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("config get as %s builder-doc mismatch (-want +got):\n%s", name, diff)
			}
		})
	}

	// An editor that points the reference at another file.
	editor := filepath.Join(t.TempDir(), "editor")
	script := "#!/bin/sh\nsed 's|builder.yaml$|other.yml|' \"$1\" > \"$1.edited\" && mv \"$1.edited\" \"$1\"\n"

	//nolint:gosec // the editor the test runs
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the editor: %v", err)
	}

	t.Setenv("EDITOR", editor)

	if _, err := run("edit", "topology/site"); err != nil {
		t.Fatalf("config edit returned error: %v", err)
	}

	want["path"] = "/phenix/topologies/site/other.yml"
	if diff := cmp.Diff(want, reference(yaml.Unmarshal)); diff != "" {
		t.Errorf("config get after the edit builder-doc mismatch (-want +got):\n%s", diff)
	}

	// The Topology config hook refuses a relative path even without validation.
	file = topology("refused", "      path: builder.yaml\n")
	if _, err := run("create", file, "--skip-validation"); err == nil {
		t.Error("config create of a relative builder-doc path succeeded, want it refused")
	}

	refused, _ := store.NewConfig("topology/refused")
	if err := store.Get(refused); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("the refused topology was stored: error = %v", err)
	}

	// config delete removes the topology, and refuses one that does not exist.
	if _, err := run("delete", "topology/site"); err != nil {
		t.Fatalf("config delete returned error: %v", err)
	}

	if _, err := run("delete", "topology/site"); err == nil {
		t.Error("config delete of a deleted topology succeeded, want it refused")
	}
}

// TestConfigCreateSkipsBuilderFiles checks that config create skips Builder files in a
// directory with a log line, and refuses each one named on the command line.
func TestConfigCreateSkipsBuilderFiles(t *testing.T) {
	storetest.UseBolt(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()
	documents, skipped := writeBuilderFiles(t, directory)

	writeTestFile(t, directory, "pump-station.topology.yaml", docsExample(t, "pump-station.topology.yaml"))
	writeTestFile(t, directory, "notes.txt", []byte("not a config"))

	run := func(args ...string) error {
		root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
		configCmd := newConfigCmd()
		configCmd.AddCommand(newConfigCreateCmd())
		root.AddCommand(configCmd)
		root.SetArgs(append([]string{"config", "create"}, args...))

		_, err := root.ExecuteC()

		return err
	}

	if err := run(directory); err != nil {
		t.Fatalf("config create on the directory returned error: %v", err)
	}

	topologies, err := config.List("topology")
	if err != nil {
		t.Fatalf("config.List(topology) error = %v", err)
	}

	if diff := cmp.Diff([]string{"pump-station"}, configNames(topologies)); diff != "" {
		t.Fatalf("config.List(topology) names mismatch, want only the config file's (-want +got):\n%s", diff)
	}

	if topologies[0].HasAnnotation(builder.DocumentAnnotation) {
		t.Error("the topology names a Builder document, which config create never publishes")
	}

	if scenarios, err := config.List("scenario"); err != nil {
		t.Errorf("config.List(scenario) error = %v", err)
	} else if len(scenarios) != 0 {
		t.Errorf("config.List(scenario) = %v, want none: a package's Scenario config is not created", scenarios)
	}

	if created := logs.Records(t, plogtest.Message("configuration created")); len(created) != 1 {
		t.Errorf("created = %v, want the one config", created)
	}

	logged := logs.Records(t, plogtest.Message("skipped Builder document; upload it in the Builder to publish it"))

	paths := make([]string, 0, len(logged))
	for _, record := range logged {
		path, _ := record["path"].(string)
		paths = append(paths, path)
	}

	if !slices.Equal(paths, documents) {
		t.Errorf("logged skipped documents = %v, want one log line for each of %v", paths, documents)
	}

	got := map[string]string{}

	for _, record := range logs.Records(t, plogtest.Message("skipped Builder file, which is not a configuration")) {
		path, _ := record["path"].(string)
		kind, _ := record["kind"].(string)
		got[path] = kind

		if record["level"] != slog.LevelDebug.String() {
			t.Errorf("the log line for %s is at level %v, want %v", path, record["level"], slog.LevelDebug)
		}
	}

	if diff := cmp.Diff(skipped, got); diff != "" {
		t.Errorf("logged skipped Builder files mismatch (-want +got):\n%s", diff)
	}

	// Named on the command line, a Builder document is an error, with or without
	// validation, and files named after it are not read.
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "a JSON document", args: []string{documents[0]}},
		{name: "a YAML document without validation", args: []string{documents[1], "--skip-validation"}},
		{name: "a document before a directory", args: []string{documents[0], directory}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args...)

			want := test.args[0] + ` is a Builder document, not a configuration: upload it in the Builder, ` +
				`or send it to the Builder REST API (/api/v1/builder/drafts), and publish it to create its topology`
			if err == nil || err.Error() != want {
				t.Errorf("config create %v error = %v, want %q", test.args, err, want)
			}
		})
	}

	for path, kind := range skipped {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if err := run(path); err == nil || err.Error() != wantBuilderFileRefusal(path, kind) {
				t.Errorf("config create %s error = %v, want %q", path, err, wantBuilderFileRefusal(path, kind))
			}
		})
	}

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	if published, err := service.ListPublishedDocuments(t.Context()); err != nil {
		t.Errorf("ListPublishedDocuments() error = %v", err)
	} else if len(published) != 0 {
		t.Errorf("ListPublishedDocuments() = %v, want none", published)
	}

	if topologies, err := config.List("topology"); err != nil {
		t.Errorf("config.List(topology) error = %v", err)
	} else if diff := cmp.Diff([]string{"pump-station"}, configNames(topologies)); diff != "" {
		t.Errorf("config.List(topology) names mismatch, want still only pump-station (-want +got):\n%s", diff)
	}

	other := writeTestFile(t, t.TempDir(), "other.json", []byte(`{"$schema": "https://json-schema.org/draft/2020-12/schema"}`))
	if err := run(other); err == nil || !strings.HasPrefix(err.Error(), "Unable to create configuration from "+other) {
		t.Errorf("config create %s (no config) error = %v, want the humanized failure", other, err)
	}
}

// configNames returns the name of each config.
func configNames(configs store.Configs) []string {
	names := make([]string, 0, len(configs))
	for _, c := range configs {
		names = append(names, c.Metadata.Name)
	}

	return names
}

// writeBuilderFiles writes Builder documents and other Builder files to directory and
// returns the document paths and the kind of each other file.
func writeBuilderFiles(t *testing.T, directory string) ([]string, map[string]string) {
	t.Helper()

	nested := filepath.Join(directory, "diagrams")

	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatalf("creating %s: %v", nested, err)
	}

	asJSON := func(name string) []byte {
		t.Helper()

		var generic any
		if err := yaml.Unmarshal(docsExample(t, name), &generic); err != nil {
			t.Fatalf("decoding the docs example %s: %v", name, err)
		}

		data, err := json.Marshal(generic)
		if err != nil {
			t.Fatalf("encoding the docs example %s as JSON: %v", name, err)
		}

		return data
	}

	var generic any
	if err := json.Unmarshal(docsExample(t, builderExample), &generic); err != nil {
		t.Fatalf("decoding the docs example: %v", err)
	}

	documentYAML, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the docs example as YAML: %v", err)
	}

	// The Builder exports each file as plain .json or .yaml, so the names say nothing.
	documents := []string{
		writeTestFile(t, directory, "a-diagram.json", docsExample(t, builderExample)),
		writeTestFile(t, nested, "pump-station.yaml", documentYAML),
	}

	skipped := map[string]string{}

	skipped[writeTestFile(t, directory, "node-templates.yaml", docsExample(t, "node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestFile(t, directory, "templates.json", asJSON("node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestFile(t, directory, "pump-station.package.yaml", docsExample(t, "pump-station.package.yaml"))] = builderFilePackage
	skipped[writeTestFile(t, directory, "package.json", asJSON("pump-station.package.yaml"))] = builderFilePackage

	layerSet := "$schema: " + bdoc.LayerSetFileSchemaURI + "\nname: Plant levels\nlayers:\n  - id: 1\n    label: Field devices\n"
	skipped[writeTestFile(t, directory, "plant-levels.layers.yaml", []byte(layerSet))] = builderFileLayerSetFile

	layerSetJSON, err := bdoc.JSONFromYAML([]byte(layerSet))
	if err != nil {
		t.Fatalf("encoding the layer set file as JSON: %v", err)
	}

	skipped[writeTestFile(t, directory, "layers.json", layerSetJSON)] = builderFileLayerSetFile

	return documents, skipped
}

// wantBuilderFileRefusal is the error config create gives for a Builder file of kind.
func wantBuilderFileRefusal(path, kind string) string {
	switch kind {
	case builderFileTemplateFile:
		return path + ` is a Builder template file, not a configuration: use Import templates in the Builder, ` +
			`or the Builder REST API (POST /api/v1/builder/templates/items), to add its Node Templates`
	case builderFileLayerSetFile:
		return path + ` is a Builder layer set file, not a configuration: ` +
			`use Upload layer set on the Layers tab of the Builder to add its layer set`
	default:
		return path + ` is a Builder package, not a configuration: upload it in the Builder to open its diagram`
	}
}

// builderExample is the Builder document the docs ship, named "Pump station".
const builderExample = "pump-station.builder.json"

// docsExample returns the content of a file of the Builder docs' examples.
func docsExample(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "content", "builder", "examples", name))
	if err != nil {
		t.Fatalf("reading the docs example: %v", err)
	}

	return data
}

// writeTestFile writes content to directory/name and returns its path.
func writeTestFile(t *testing.T, directory, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(directory, name)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}
