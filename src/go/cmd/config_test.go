package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// TestConfigGetYAMLKeepsStrings gets a config whose strings start with a
// line break or a tab as YAML, and asserts the output loads as the config.
func TestConfigGetYAMLKeepsStrings(t *testing.T) {
	stored := func() store.Config {
		return store.Config{
			Version: "phenix.sandia.gov/v2",
			Kind:    "Scenario",
			Metadata: store.ConfigMetadata{
				Name: "exact", Created: "", Updated: "", Annotations: store.Annotations{"note": "\n\tnote"},
			},
			Spec: map[string]any{"apps": []any{map[string]any{
				"name": "app", "metadata": map[string]any{"description": "\nfirst", "script": "\tfirst\nsecond"},
			}}},
			Status: nil,
		}
	}

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = stored()

		return nil
	})

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // monkey patching for test

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	configCmd := newConfigCmd()
	configCmd.AddCommand(newConfigGetCmd())
	root.AddCommand(configCmd)
	root.SetArgs([]string{"config", "get", "scenario/exact"})

	var output bytes.Buffer
	root.SetOut(&output)

	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("config get returned error: %v", err)
	}

	var loaded store.Config
	if err := yaml.Unmarshal(output.Bytes(), &loaded); err != nil {
		t.Fatalf("output does not load: %v\n%s", err, output.String())
	}

	got, _ := json.Marshal(loaded)
	want, _ := json.Marshal(stored())

	if string(got) != string(want) {
		t.Fatalf("output loads as %s, want %s\n%s", got, want, output.String())
	}
}

// TestConfigDeleteRemovesBuilderDocuments deletes a topology with `phenix
// config delete` and asserts its published Builder documents go with it: the
// CLI runs the Topology config hook Builder registers.
func TestConfigDeleteRemovesBuilderDocuments(t *testing.T) {
	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	data, err := builder.EncodeDocument(bdoc.NewDocument("topo"))
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	document, err := service.PutPublishedDocument(t.Context(), builder.PutPublishedDocumentRequest{
		Target: "topo", Kind: "Topology", Actor: "alice", Document: data,
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %v", err)
	}

	reference, err := document.Reference().EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	topology, err := store.NewConfig("Topology/topo")
	if err != nil {
		t.Fatalf("NewConfig returned error: %v", err)
	}

	topology.Metadata.Annotations = store.Annotations{builder.DocumentAnnotation: reference}

	if err := store.Create(topology); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	root := &cobra.Command{Use: "phenix", SilenceUsage: true}
	configCmd := newConfigCmd()
	configCmd.AddCommand(newConfigDeleteCmd())
	root.AddCommand(configCmd)
	root.SetArgs([]string{"config", "delete", "topology/topo"})

	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("config delete returned error: %v", err)
	}

	if _, err := service.GetPublishedDocument(t.Context(), document.ID); !errors.Is(err, builder.ErrNotFound) {
		t.Fatalf("published document: error = %v, want it deleted with its topology", err)
	}
}

// TestConfigCommandsShowBuilderDocumentAsMap creates a topology from a YAML
// file whose builder-doc annotation is a map, as a topology repository keeps
// it, then gets it as YAML and as JSON and edits it: every command reads and
// writes the map, and the hook refuses a reference that is not valid.
func TestConfigCommandsShowBuilderDocumentAsMap(t *testing.T) {
	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	topology := func(name, reference string) string {
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
		configCmd.AddCommand(newConfigCreateCmd(), newConfigGetCmd(), newConfigEditCmd())
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
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("config get as %s shows builder-doc = %#v, want %#v", name, got, want)
		}
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
	if got := reference(yaml.Unmarshal); !reflect.DeepEqual(got, want) {
		t.Fatalf("config get after the edit shows builder-doc = %#v, want %#v", got, want)
	}

	// The schema refuses an unknown sub-key, and the Topology config hook a
	// sub-key that is not valid, with or without validation.
	for name, test := range map[string]struct {
		reference string
		args      []string
	}{
		"an unknown sub-key":                {reference: "      file: /phenix/builder.yaml\n"},
		"a relative path":                   {reference: "      path: builder.yaml\n"},
		"a relative path, not validated":    {reference: "      path: builder.yaml\n", args: []string{"--skip-validation"}},
		"an unknown sub-key, not validated": {reference: "      file: /phenix/builder.yaml\n", args: []string{"--skip-validation"}},
	} {
		if _, err := run(append([]string{"create", topology("refused", test.reference)}, test.args...)...); err == nil {
			t.Errorf("%s: config create succeeded, want it refused", name)
		}

		refused, _ := store.NewConfig("topology/refused")
		if err := store.Get(refused); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s: the topology was stored: error = %v", name, err)
		}
	}
}

// TestConfigCreateRecognizesBuilderDocuments runs `phenix config create` on
// a directory that holds a config and Builder documents, as a topology
// repository does: the config is created and each document is skipped with a
// log line that says to upload it in the Builder. A Builder document named on
// the command line is refused with an error that names it. Neither publishes
// the document.
func TestConfigCreateRecognizesBuilderDocuments(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()
	nested := filepath.Join(directory, "diagrams")

	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatalf("creating %s: %v", nested, err)
	}

	var generic any
	if err := json.Unmarshal(docsExample(t, builderExample), &generic); err != nil {
		t.Fatalf("decoding the docs example: %v", err)
	}

	asYAML, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the docs example as YAML: %v", err)
	}

	// The names say nothing: the Builder exports a document as plain .json
	// and .yaml.
	documents := []string{
		writeTestFile(t, directory, "a-diagram.json", docsExample(t, builderExample)),
		writeTestFile(t, nested, "pump-station.yaml", asYAML),
	}

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
	if err != nil || len(topologies) != 1 || topologies[0].Metadata.Name != "pump-station" {
		t.Fatalf("topologies = %v, %v, want only pump-station, from the config file", topologies, err)
	}

	if topologies[0].HasAnnotation(builder.DocumentAnnotation) {
		t.Error("the topology names a Builder document, which config create never publishes")
	}

	skipped := logs.Records(t, plogtest.Message("skipped Builder document; upload it in the Builder to publish it"))

	paths := make([]string, 0, len(skipped))
	for _, record := range skipped {
		path, _ := record["path"].(string)
		paths = append(paths, path)
	}

	if !slices.Equal(paths, documents) {
		t.Errorf("skipped %v, want one log line for each of %v", paths, documents)
	}

	if created := logs.Records(t, plogtest.Message("configuration created")); len(created) != 1 {
		t.Errorf("created = %v, want the one config", created)
	}

	// Named on the command line, a Builder document is an error, with or
	// without validation, and files named after it are not read.
	for _, args := range [][]string{{documents[0]}, {documents[1], "--skip-validation"}, {documents[0], directory}} {
		err := run(args...)

		want := args[0] + ` is a Builder document, not a configuration: upload it in the Builder, ` +
			`or send it to the Builder REST API (/api/v1/builder/drafts), and publish it to create its topology`
		if err == nil || err.Error() != want {
			t.Errorf("config create %v: error = %v, want %q", args, err, want)
		}
	}

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	if published, err := service.ListPublishedDocuments(t.Context()); err != nil || len(published) != 0 {
		t.Errorf("published documents = %v, %v, want none", published, err)
	}

	if topologies, err := config.List("topology"); err != nil || len(topologies) != 1 {
		t.Errorf("topologies = %v, %v, want still only pump-station", topologies, err)
	}

	// A file that is neither still fails as it did.
	other := writeTestFile(t, t.TempDir(), "other.json", []byte(`{"$schema": "https://json-schema.org/draft/2020-12/schema"}`))
	if err := run(other); err == nil || !strings.HasPrefix(err.Error(), "Unable to create configuration from "+other) {
		t.Errorf("config create of a file that is no config: error = %v, want the humanized failure", err)
	}
}

// TestConfigCreateSkipsBuilderFiles runs config create on a directory that
// holds a config and the three kinds of Builder files the docs examples
// directory holds: a Builder document, a template file and a package, YAML
// and JSON. The config is created, the document is skipped with its log
// line, and each template file and package with a debug log line that names
// it. Named on the command line, a template file or a package is refused
// with what to do with it instead.
func TestConfigCreateSkipsBuilderFiles(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()

	asJSON := func(name string) []byte {
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

	document := writeTestFile(t, directory, "diagram.json", docsExample(t, builderExample))
	skipped := map[string]string{}

	skipped[writeTestFile(t, directory, "node-templates.yaml", docsExample(t, "node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestFile(t, directory, "templates.json", asJSON("node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestFile(t, directory, "pump-station.package.yaml", docsExample(t, "pump-station.package.yaml"))] = builderFilePackage
	skipped[writeTestFile(t, directory, "package.json", asJSON("pump-station.package.yaml"))] = builderFilePackage

	writeTestFile(t, directory, "pump-station.topology.yaml", docsExample(t, "pump-station.topology.yaml"))

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
	if err != nil || len(topologies) != 1 || topologies[0].Metadata.Name != "pump-station" {
		t.Fatalf("topologies = %v, %v, want only pump-station, from the config file", topologies, err)
	}

	if scenarios, err := config.List("scenario"); err != nil || len(scenarios) != 0 {
		t.Errorf("scenarios = %v, %v, want none: a package's Scenario config is not created", scenarios, err)
	}

	documents := logs.Records(t, plogtest.Message("skipped Builder document; upload it in the Builder to publish it"))
	if len(documents) != 1 || documents[0]["path"] != document {
		t.Errorf("document log lines = %v, want one for %s", documents, document)
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

	if !reflect.DeepEqual(got, skipped) {
		t.Errorf("skipped %v, want %v", got, skipped)
	}

	for path, kind := range skipped {
		want := path + ` is a Builder package, not a configuration: upload it in the Builder to open its diagram`
		if kind == builderFileTemplateFile {
			want = path + ` is a Builder template file, not a configuration: use Import templates in the Builder, ` +
				`or the Builder REST API (POST /api/v1/builder/templates/{owner}/items), to add its Node Templates`
		}

		if err := run(path); err == nil || err.Error() != want {
			t.Errorf("config create %s: error = %v, want %q", path, err, want)
		}
	}

	if topologies, err := config.List("topology"); err != nil || len(topologies) != 1 {
		t.Errorf("topologies = %v, %v, want still only pump-station", topologies, err)
	}
}

// builderExample is the Builder document the docs ship, which names itself
// "Pump station".
const builderExample = "pump-station.builder.json"

// useBoltStore makes a BoltDB of the test's own the phenix store.
func useBoltStore(t *testing.T) {
	t.Helper()

	db := store.NewBoltDB()
	if err := db.Init(store.Endpoint("bolt://" + filepath.Join(t.TempDir(), "phenix.bdb"))); err != nil {
		t.Fatalf("initializing BoltDB returned error: %v", err)
	}

	previous := store.DefaultStore
	store.DefaultStore = db //nolint:reassign // the test's own store

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // restore the store
}

// docsExample returns the content of a file of the Builder docs' examples.
func docsExample(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "content", "builder", "examples", name))
	if err != nil {
		t.Fatalf("reading the docs example: %v", err)
	}

	return data
}

// writeTestFile writes content to the file name of a directory of the
// test's own, and returns its path.
func writeTestFile(t *testing.T, directory, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(directory, name)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}
