package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		return path
	}

	run := runConfigTestCommand

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

// TestConfigSkipsBuilderFiles checks that config apply skips Builder files in a
// directory with a log line, and that config create refuses each one named on
// the command line.
func TestConfigSkipsBuilderFiles(t *testing.T) {
	storetest.UseBolt(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()
	documents, skipped := writeBuilderFiles(t, directory)

	writeTestBytes(t, directory, "pump-station.topology.yaml", exampleFile(t, "pump-station.topology.yaml"))
	writeTestBytes(t, directory, "notes.txt", []byte("not a config"))

	run := func(args ...string) error {
		_, err := runConfigTestCommand(append([]string{"create"}, args...)...)

		return err
	}

	if out, err := runConfigTestCommand("apply", "-f", directory); err != nil || out != "topology/pump-station created\n" {
		t.Fatalf("config apply on the directory = %q, %v, want the one config created", out, err)
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

	logged := logs.Records(t, plogtest.Message("skipped Builder document. Upload it in the Builder to publish it"))

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
		{name: "a document before a config file", args: []string{documents[0], filepath.Join(directory, "pump-station.topology.yaml")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args...)

			want := test.args[0] + ` is a Builder document, not a configuration. Upload it in the Builder ` +
				`or send it to the Builder REST API (/api/v1/builder/drafts). Then publish it to create its topology`
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

	other := writeTestBytes(t, t.TempDir(), "other.json", []byte(`{"$schema": "https://json-schema.org/draft/2020-12/schema"}`))
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
		if err := yaml.Unmarshal(exampleFile(t, name), &generic); err != nil {
			t.Fatalf("decoding the example %s: %v", name, err)
		}

		data, err := json.Marshal(generic)
		if err != nil {
			t.Fatalf("encoding the example %s as JSON: %v", name, err)
		}

		return data
	}

	var generic any
	if err := json.Unmarshal(exampleFile(t, builderExample), &generic); err != nil {
		t.Fatalf("decoding the example: %v", err)
	}

	documentYAML, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the example as YAML: %v", err)
	}

	// The Builder exports each file as plain .json or .yaml, so the names say nothing.
	documents := []string{
		writeTestBytes(t, directory, "a-diagram.json", exampleFile(t, builderExample)),
		writeTestBytes(t, nested, "pump-station.yaml", documentYAML),
	}

	skipped := map[string]string{}

	skipped[writeTestBytes(t, directory, "node-templates.yaml", exampleFile(t, "node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestBytes(t, directory, "templates.json", asJSON("node-templates.yaml"))] = builderFileTemplateFile
	skipped[writeTestBytes(t, directory, "pump-station.package.yaml", exampleFile(t, "pump-station.package.yaml"))] = builderFilePackage
	skipped[writeTestBytes(t, directory, "package.json", asJSON("pump-station.package.yaml"))] = builderFilePackage

	layerSet := "$schema: " + bdoc.LayerSetFileSchemaURI + "\nname: Plant levels\nlayers:\n  - id: 1\n    label: Field devices\n"
	skipped[writeTestBytes(t, directory, "plant-levels.layers.yaml", []byte(layerSet))] = builderFileLayerSetFile

	layerSetJSON, err := bdoc.JSONFromYAML([]byte(layerSet))
	if err != nil {
		t.Fatalf("encoding the layer set file as JSON: %v", err)
	}

	skipped[writeTestBytes(t, directory, "layers.json", layerSetJSON)] = builderFileLayerSetFile

	return documents, skipped
}

// wantBuilderFileRefusal is the error config create gives for a Builder file of kind.
func wantBuilderFileRefusal(path, kind string) string {
	switch kind {
	case builderFileTemplateFile:
		return path + ` is a Builder template file, not a configuration. To add its Node Templates, use Import templates ` +
			`in the Builder or the Builder REST API (POST /api/v1/builder/templates/items)`
	case builderFileLayerSetFile:
		return path + ` is a Builder layer set file, not a configuration. ` +
			`Use Upload layer set on the Layers tab of the Builder to add its layer set`
	default:
		return path + ` is a Builder package, not a configuration. Upload it in the Builder to open its diagram`
	}
}

// builderExample is the example Builder document named "Pump station".
const builderExample = "pump-station.builder.json"

// exampleFile returns the content of a file of the Builder examples in types/builder/testdata/examples.
func exampleFile(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "types", "builder", "testdata", "examples", name))
	if err != nil {
		t.Fatalf("reading the example: %v", err)
	}

	return data
}

// writeTestBytes writes content to directory/name and returns its path.
func writeTestBytes(t *testing.T, directory, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(directory, name)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// runConfigTestCommand runs "phenix config <args>" with every config
// subcommand, and returns what the command wrote to its output.
func runConfigTestCommand(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	configCmd := newConfigCmd()
	deleteCmd := newConfigDeleteCmd()
	deleteCmd.AddCommand(newConfigDeleteAllCmd())
	configCmd.AddCommand(
		newConfigListCmd(), newConfigGetCmd(), newConfigCreateCmd(), newConfigApplyCmd(), newConfigEditCmd(), deleteCmd,
	)
	root.AddCommand(configCmd)
	root.SetArgs(append([]string{"config"}, args...))

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return output.String(), err
}

// captureStdout returns what fn writes to [os.Stdout]. Tables that pick colors
// from the real stdout, and output from other packages, go there and not to
// the command writer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	read := make(chan []byte)

	go func() {
		data, _ := io.ReadAll(reader)
		read <- data
	}()

	original := os.Stdout
	os.Stdout = writer //nolint:reassign // some output goes to the process stdout

	func() {
		defer func() { os.Stdout = original }() //nolint:reassign // restore the process stdout

		fn()
	}()

	_ = writer.Close()
	data := <-read
	_ = reader.Close()

	return string(data)
}

// configTestTableRows returns the kind and name of each row of the table
// that config list prints.
func configTestTableRows(output string) [][2]string {
	var rows [][2]string

	for line := range strings.Lines(output) {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}

		kind, name := strings.TrimSpace(cells[1]), strings.TrimSpace(cells[3])
		if kind != "KIND" {
			rows = append(rows, [2]string{kind, name})
		}
	}

	return rows
}

// storeConfigTestConfigs stores a config without a spec for each kind/name.
func storeConfigTestConfigs(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		c, err := store.NewConfig(name)
		if err != nil {
			t.Fatalf("store.NewConfig(%s) error = %v", name, err)
		}

		if err := store.Create(c); err != nil {
			t.Fatalf("store.Create(%s) error = %v", name, err)
		}
	}
}

// useConfigTestMockStore makes a store mock the phenix store until the test
// ends. A call that the test does not expect fails the test.
func useConfigTestMockStore(t *testing.T) *store.MockStore {
	t.Helper()

	mock := store.NewMockStore(gomock.NewController(t))
	storetest.Use(t, mock)

	return mock
}

func TestConfigKindArgsValidator(t *testing.T) {
	const form = "expected an argument in the form of <config kind>/<config name>"

	kinds := "[topology scenario experiment image user role]"

	for _, tt := range []struct {
		name  string
		multi bool
		args  []string
		want  string
	}{
		{name: "one config", args: []string{"topology/site"}},
		{name: "kind in capitals", args: []string{"TOPOLOGY/site"}},
		{name: "Unicode name", args: []string{"scenario/día-1"}},
		{name: "no argument", args: nil, want: "expected a single argument, received 0"},
		{name: "two arguments", args: []string{"topology/a", "topology/b"}, want: "expected a single argument, received 2"},
		{name: "no slash", args: []string{"topology"}, want: form},
		{name: "two slashes", args: []string{"topology/a/b"}, want: form},
		{name: "unknown kind", args: []string{"vm/a"}, want: "expects the configuration kind to be one of " + kinds + ", received vm"},
		{name: "all", args: []string{"all/a"}, want: "expects the configuration kind to be one of " + kinds + ", received all"},
		{name: "multi, all", multi: true, args: []string{"all/a"}, want: "expects the configuration kind to be one of " + kinds + ", received all"},
		{name: "multi, no argument", multi: true, args: []string{}, want: "must provide at least one argument"},
		{name: "multi, many", multi: true, args: []string{"topology/a", "image/b", "Role/c"}},
		{name: "multi, one bad", multi: true, args: []string{"topology/a", "b"}, want: form},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := configKindArgsValidator(tt.multi)(nil, tt.args)

			if got := errorText(err); got != tt.want {
				t.Errorf("configKindArgsValidator(%t)(%q) error = %q, want %q", tt.multi, tt.args, got, tt.want)
			}
		})
	}
}

func TestConfigKindValidator(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no argument", args: nil},
		{name: "a kind", args: []string{"role"}},
		{name: "kind in capitals", args: []string{"TOPOLOGY"}},
		{name: "two kinds", args: []string{"topology", "image"}, want: "expected zero or one argument, received 2"},
		{
			name: "all", args: []string{"all"},
			want: "expects the configuration kind to be one of [topology scenario experiment image user role], received all",
		},
		{
			name: "kind and name", args: []string{"topology/site"},
			want: "expects the configuration kind to be one of [topology scenario experiment image user role], received topology/site",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorText(configKindValidator()(nil, tt.args)); got != tt.want {
				t.Errorf("configKindValidator()(%q) error = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// errorText returns the text of err, or "" for no error.
func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// TestConfigCommandTree checks the config commands that init adds to the root
// command, and that config without a subcommand shows its help.
func TestConfigCommandTree(t *testing.T) {
	configCmd, _, err := rootCmd.Find([]string{"cfg"})
	if err != nil || configCmd.Name() != "config" {
		t.Fatalf("rootCmd.Find(cfg) = %v, %v, want the config command", configCmd, err)
	}

	names := make([]string, 0, len(configCmd.Commands()))
	for _, sub := range configCmd.Commands() {
		names = append(names, sub.Name())
	}

	if diff := cmp.Diff([]string{"apply", "create", "delete", "diff", "edit", "get", "list"}, names); diff != "" {
		t.Errorf("config subcommands mismatch (-want +got):\n%s", diff)
	}

	if all, _, err := rootCmd.Find([]string{"config", "delete", "all"}); err != nil || all.CommandPath() != "phenix config delete all" {
		t.Errorf("rootCmd.Find(config delete all) = %v, %v, want phenix config delete all", all, err)
	}

	output, err := runConfigTestCommand()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}

	if !strings.Contains(output, "manage the different kinds of phenix configuration") {
		t.Errorf("config output = %q, want its help", output)
	}
}

func TestConfigListCmd(t *testing.T) {
	storetest.UseBolt(t)

	list := func(t *testing.T, args ...string) string {
		t.Helper()

		output, err := runConfigTestCommand(append([]string{"list"}, args...)...)
		if err != nil {
			t.Fatalf("config list %q returned error: %v", args, err)
		}

		return output
	}

	if output := list(t); !strings.Contains(output, "There are no configurations available") {
		t.Errorf("config list of an empty store = %q, want that there are no configurations", output)
	}

	storeConfigTestConfigs(t, "topology/beta", "topology/alpha", "image/disk", "role/viewer")

	for _, tt := range []struct {
		args []string
		want [][2]string
	}{
		{
			args: nil,
			want: [][2]string{{"Topology", "alpha"}, {"Topology", "beta"}, {"Image", "disk"}, {"Role", "viewer"}},
		},
		{
			args: []string{"all"},
			want: [][2]string{{"Topology", "alpha"}, {"Topology", "beta"}, {"Image", "disk"}, {"Role", "viewer"}},
		},
		{args: []string{"Topology"}, want: [][2]string{{"Topology", "alpha"}, {"Topology", "beta"}}},
		{args: []string{"image"}, want: [][2]string{{"Image", "disk"}}},
		{args: []string{"scenario"}, want: nil},
	} {
		t.Run(strings.Join(append([]string{"list"}, tt.args...), " "), func(t *testing.T) {
			if diff := cmp.Diff(tt.want, configTestTableRows(list(t, tt.args...))); diff != "" {
				t.Errorf("config list %q rows mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}

	// config list has no argument validator, so config.List refuses the kind.
	if _, err := runConfigTestCommand("list", "vm"); errorText(err) != "Unknown config kind provided: vm" {
		t.Errorf("config list vm error = %v, want the unknown kind", err)
	}
}

func TestConfigGetArgsCompletion(t *testing.T) {
	storetest.UseBolt(t)
	storeConfigTestConfigs(t, "topology/alpha", "topology/beta", "image/alpine")

	kinds := []string{"topology/", "scenario/", "experiment/", "image/", "user/", "role/"}
	kindDirective := cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp

	for _, tt := range []struct {
		name       string
		args       []string
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{name: "after the argument", args: []string{"topology/alpha"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "every kind", toComplete: "", want: kinds, directive: kindDirective},
		{name: "a kind", toComplete: "to", want: []string{"topology/"}, directive: kindDirective},
		{name: "no kind", toComplete: "x", directive: kindDirective},
		{
			name: "every name of a kind", toComplete: "topology/",
			want: []string{"topology/alpha", "topology/beta"}, directive: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name: "a name, kind as typed", toComplete: "Topology/al",
			want: []string{"Topology/alpha"}, directive: cobra.ShellCompDirectiveNoFileComp,
		},
		{name: "kind of no configs", toComplete: "scenario/", directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "no name", toComplete: "image/z", directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "unknown kind", toComplete: "vm/", directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "two slashes", toComplete: "topology/alpha/", directive: cobra.ShellCompDirectiveNoFileComp},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := configGetArgsCompletion(nil, tt.args, tt.toComplete)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("configGetArgsCompletion(%q, %q) mismatch (-want +got):\n%s", tt.args, tt.toComplete, diff)
			}

			if directive != tt.directive {
				t.Errorf("configGetArgsCompletion(%q, %q) directive = %v, want %v", tt.args, tt.toComplete, directive, tt.directive)
			}
		})
	}
}

func TestConfigGetCmdOutput(t *testing.T) {
	storetest.UseBolt(t)

	experiment, err := store.NewConfig("experiment/exp")
	if err != nil {
		t.Fatalf("store.NewConfig error = %v", err)
	}

	experiment.Spec = map[string]any{"experimentName": "exp", "baseDir": "/phenix/exp"}

	if err := store.Create(experiment); err != nil {
		t.Fatalf("store.Create(experiment/exp) error = %v", err)
	}

	storeConfigTestConfigs(t, "image/disk")

	type shown struct {
		Kind     string         `json:"kind"     yaml:"kind"`
		Metadata map[string]any `json:"metadata" yaml:"metadata"`
		Spec     map[string]any `json:"spec"     yaml:"spec"`
	}

	for _, tt := range []struct {
		name      string
		args      []string
		unmarshal func([]byte, any) error
		lines     int
		want      shown
	}{
		{
			name: "experiment without its name", args: []string{"experiment/exp"}, unmarshal: yaml.Unmarshal,
			want: shown{Kind: "Experiment", Spec: map[string]any{"baseDir": "/phenix/exp"}},
		},
		{
			name: "JSON", args: []string{"image/disk", "-o", "json"}, unmarshal: json.Unmarshal,
			want: shown{Kind: "Image"},
		},
		{
			name: "pretty JSON", args: []string{"Image/disk", "--output", "json", "--pretty"}, unmarshal: json.Unmarshal,
			want: shown{Kind: "Image"},
		},
		{
			name: "pretty keeps YAML", args: []string{"image/disk", "--pretty"}, unmarshal: yaml.Unmarshal,
			want: shown{Kind: "Image"},
		},
		{
			name: "pretty YAML", args: []string{"image/disk", "-o", "yaml", "-p"}, unmarshal: yaml.Unmarshal,
			want: shown{Kind: "Image"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runConfigTestCommand(append([]string{"get"}, tt.args...)...)
			if err != nil {
				t.Fatalf("config get %q returned error: %v", tt.args, err)
			}

			if lines := strings.Count(output, "\n"); tt.lines != 0 && lines != tt.lines {
				t.Errorf("config get %q printed %d lines, want %d:\n%s", tt.args, lines, tt.lines, output)
			} else if tt.lines == 0 && lines < 2 {
				t.Errorf("config get %q printed %d lines, want it indented:\n%s", tt.args, lines, output)
			}

			var got shown
			if err := tt.unmarshal([]byte(output), &got); err != nil {
				t.Fatalf("config get %q output does not load: %v\n%s", tt.args, err, output)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreFields(shown{}, "Metadata")); diff != "" {
				t.Errorf("config get %q mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown format", args: []string{"image/disk", "-o", "xml"},
			want: `invalid argument "xml" for "-o, --output" flag: unknown output format "xml": use one of yaml, json`,
		},
		{name: "missing config", args: []string{"image/none"}, want: "Unable to get the image/none configuration (search error logs for "},
		{name: "no name", args: []string{"image"}, want: "expected an argument in the form of <config kind>/<config name>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runConfigTestCommand(append([]string{"get"}, tt.args...)...)
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("config get %q error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestConfigCreateCmdArguments(t *testing.T) {
	storetest.UseBolt(t)

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	notes := t.TempDir()
	writeTestBytes(t, notes, "notes.txt", []byte("not a config"))

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no file", args: nil, want: "must provide at least one configuration file"},
		{name: "a missing file", args: []string{missing}, want: "Unable to create configuration from " + missing + " (search error logs for "},
		{name: "a directory", args: []string{notes}, want: notes + " is a directory: "},
		{name: "a file that is not a config file", args: []string{filepath.Join(notes, "notes.txt")}, want: filepath.Join(notes, "notes.txt") + ": not a "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runConfigTestCommand(append([]string{"create"}, tt.args...)...)

			if got := errorText(err); (tt.want == "") != (got == "") || !strings.HasPrefix(got, tt.want) {
				t.Errorf("config create %q error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}

	if configs, err := config.List("all"); err != nil || len(configs) != 0 {
		t.Errorf("config.List(all) = %v, %v, want no configs", configs, err)
	}
}

func TestConfigFilesAt(t *testing.T) {
	directory := t.TempDir()
	nested := filepath.Join(directory, "b")

	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatalf("creating %s: %v", nested, err)
	}

	for _, name := range []string{"a.json", "b/c.yml", "b/d.yaml", "e.txt", "f.YAML", "yaml", "é.yaml"} {
		writeTestBytes(t, directory, name, nil)
	}

	in := func(names ...string) []string {
		paths := make([]string, 0, len(names))
		for _, name := range names {
			paths = append(paths, filepath.Join(directory, name))
		}

		return paths
	}

	for _, tt := range []struct {
		name string
		path string
		want []string
	}{
		{name: "directory tree", path: directory, want: in("a.json", "b/c.yml", "b/d.yaml", "é.yaml")},
		{name: "a config file", path: in("a.json")[0], want: in("a.json")},
		{name: "another file", path: in("e.txt")[0], want: nil},
		{name: "extension in capitals", path: in("f.YAML")[0], want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configFilesAt(tt.path)
			if err != nil {
				t.Fatalf("configFilesAt(%s) error = %v", tt.path, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("configFilesAt(%s) mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}

	if got, err := configFilesAt(filepath.Join(directory, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("configFilesAt(missing) = %q, %v, want os.ErrNotExist", got, err)
	}
}

func TestBuilderFileKindOfOtherFiles(t *testing.T) {
	directory := t.TempDir()

	for name, path := range map[string]string{
		"a missing file": filepath.Join(directory, "missing.json"),
		"a topology":     writeTestBytes(t, directory, "topology.yaml", []byte(configCreateTestTopology)),
		"an empty file":  writeTestBytes(t, directory, "empty.json", nil),
		"not JSON":       writeTestBytes(t, directory, "broken.json", []byte("{")),
	} {
		t.Run(name, func(t *testing.T) {
			if got := builderFileKind(path); got != "" {
				t.Errorf("builderFileKind(%s) = %q, want \"\"", path, got)
			}
		})
	}
}

func TestConfigEditCmd(t *testing.T) {
	storetest.UseBolt(t)
	storeConfigTestConfigs(t, "image/disk")

	logs := plogtest.Capture(t)

	for _, tt := range []struct {
		name, editor string
		args         []string
		want         string
		logged       string
	}{
		{name: "no change", editor: "true", args: []string{"image/disk"}, logged: "configuration not updated"},
		{
			name: "editor fails", editor: "false", args: []string{"image/disk"},
			want: "Unable to edit the image/disk configuration provided (search error logs for ",
		},
		{
			name: "missing config", editor: "true", args: []string{"image/none"},
			want: "Unable to edit the image/none configuration provided (search error logs for ",
		},
		{name: "two configs", editor: "true", args: []string{"image/disk", "image/disk"}, want: "expected a single argument, received 2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("EDITOR", tt.editor)

			_, err := runConfigTestCommand(append([]string{"edit"}, tt.args...)...)
			if got := errorText(err); (tt.want == "") != (got == "") || !strings.HasPrefix(got, tt.want) {
				t.Errorf("config edit %q with EDITOR=%s error = %v, want %q", tt.args, tt.editor, err, tt.want)
			}

			records := logs.Take(t, plogtest.Message(tt.logged))
			if tt.logged != "" && (len(records) != 1 || records[0]["level"] != slog.LevelWarn.String()) {
				t.Errorf("config edit %q logged %v, want one warning %q", tt.args, records, tt.logged)
			}
		})
	}
}

func TestConfigDeleteAllCmd(t *testing.T) {
	storetest.UseBolt(t)
	storeConfigTestConfigs(t, "topology/alpha", "topology/beta", "image/disk", "user/alice")

	remaining := func(t *testing.T) []string {
		t.Helper()

		configs, err := config.List("all")
		if err != nil {
			t.Fatalf("config.List(all) error = %v", err)
		}

		names := make([]string, 0, len(configs))
		for _, c := range configs {
			names = append(names, c.FullName())
		}

		return names
	}

	stored := []string{"Topology/alpha", "Topology/beta", "Image/disk", "User/alice"}

	for _, tt := range []struct {
		args []string
		want []string
		err  string
	}{
		{args: []string{"vm"}, want: stored, err: "expects the configuration kind"},
		{args: []string{"scenario"}, want: stored},
		{args: []string{"TOPOLOGY"}, want: []string{"Image/disk", "User/alice"}},
		{args: nil, want: []string{}},
	} {
		t.Run(strings.Join(append([]string{"all"}, tt.args...), " "), func(t *testing.T) {
			_, err := runConfigTestCommand(append([]string{"delete", "all"}, tt.args...)...)
			if got := errorText(err); (tt.err == "") != (got == "") || !strings.HasPrefix(got, tt.err) {
				t.Errorf("config delete all %q error = %v, want %q", tt.args, err, tt.err)
			}

			if diff := cmp.Diff(tt.want, remaining(t)); diff != "" {
				t.Errorf("configs after config delete all %q mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// TestConfigCommandsReportStoreErrors checks the error of each config command
// when the store fails.
func TestConfigCommandsReportStoreErrors(t *testing.T) {
	failure := errors.New("store is down")

	t.Run("list", func(t *testing.T) {
		useConfigTestMockStore(t).EXPECT().List("Image").Return(nil, failure)

		_, err := runConfigTestCommand("list", "image")
		if !strings.HasPrefix(errorText(err), "Unable to list known configurations (search error logs for ") {
			t.Errorf("config list image error = %v, want the humanized list failure", err)
		}
	})

	t.Run("completion", func(t *testing.T) {
		useConfigTestMockStore(t).EXPECT().List("Image").Return(nil, failure)

		got, directive := configGetArgsCompletion(nil, nil, "image/")
		if got != nil || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("configGetArgsCompletion(image/) = %q, %v, want none and ShellCompDirectiveNoFileComp", got, directive)
		}
	})

	t.Run("delete all, list", func(t *testing.T) {
		useConfigTestMockStore(t).EXPECT().List("Image").Return(nil, failure)

		_, err := runConfigTestCommand("delete", "all", "image")
		if !strings.HasPrefix(errorText(err), "Unable to list known configurations (search error logs for ") {
			t.Errorf("config delete all image error = %v, want the humanized list failure", err)
		}
	})

	t.Run("edit, conflict", func(t *testing.T) {
		editor := filepath.Join(t.TempDir(), "editor")
		if err := os.WriteFile(editor, []byte("#!/bin/sh\necho '# edited' >> \"$1\"\n"), 0o700); err != nil {
			t.Fatalf("writing the editor: %v", err)
		}

		t.Setenv("EDITOR", editor)

		// the second read, after the edit, finds a newer revision
		var reads uint64

		useConfigTestMockStore(t).EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
			reads++
			c.Metadata.Revision = reads

			return nil
		}).Times(2)

		_, err := runConfigTestCommand("edit", "image/disk")
		want := "image/disk changed while you edited it, so run `phenix config edit` again (search error logs for "
		if !strings.HasPrefix(errorText(err), want) {
			t.Errorf("config edit image/disk error = %v, want the humanized conflict", err)
		}
	})

	t.Run("delete all, delete", func(t *testing.T) {
		mock := useConfigTestMockStore(t)
		mock.EXPECT().List("Image").Return(store.Configs{{Kind: "Image", Metadata: store.ConfigMetadata{Name: "disk"}}}, nil)
		mock.EXPECT().Get(gomock.Any()).Return(failure)

		_, err := runConfigTestCommand("delete", "all", "image")
		if !strings.HasPrefix(errorText(err), "Unable to delete one or more configurations (search error logs for ") {
			t.Errorf("config delete all image error = %v, want the humanized delete failure", err)
		}
	})
}
