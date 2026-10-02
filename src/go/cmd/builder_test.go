package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/builder"
	"phenix/api/config"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/common"
	"phenix/util/plog/plogtest"
)

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

// runBuilder runs `phenix builder` with args and returns what it wrote to
// standard output.
func runBuilder(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	builderCmd := newBuilderCmd()
	builderCmd.AddCommand(newBuilderPublishCmd())
	root.AddCommand(builderCmd)
	root.SetArgs(append([]string{"builder"}, args...))

	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	_, err := root.ExecuteC()

	return output.String(), err
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

// editedExample returns the docs example with its first device renamed, as
// JSON: a document that publishes another topology.
func editedExample(t *testing.T, hostname string) []byte {
	t.Helper()

	var document map[string]any
	if err := json.Unmarshal(docsExample(t, builderExample), &document); err != nil {
		t.Fatalf("decoding the docs example: %v", err)
	}

	for _, entry := range document["nodes"].([]any) {
		node := entry.(map[string]any)
		if node["kind"] != "device" {
			continue
		}

		device := node["device"].(map[string]any)
		device["hostname"] = hostname
		device["spec"].(map[string]any)["general"].(map[string]any)["hostname"] = hostname
		node["label"] = hostname

		edited, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encoding the edited example: %v", err)
		}

		return edited
	}

	t.Fatal("the docs example has no device")

	return nil
}

// storedTopology returns the stored topology name and the document
// reference it holds, as `phenix config get` shows it.
func storedTopology(t *testing.T, name string) (*store.Config, builder.DocumentReference) {
	t.Helper()

	topology, err := config.Get("topology/"+name, false)
	if err != nil {
		t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	reference, err := builder.DecodeReference(topology.Metadata.Annotations[builder.DocumentAnnotation])
	if err != nil {
		t.Fatalf("topology %s holds no valid document reference: %v", name, err)
	}

	return topology, reference
}

// TestBuilderPublish publishes the docs example from a file: the topology is
// created under the name the Publish dialog proposes, publishing the file
// again or a YAML copy of it changes nothing, and a changed document
// replaces the topology only with --update.
func TestBuilderPublish(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()
	file := writeTestFile(t, directory, "pump-station.json", docsExample(t, builderExample))

	// logged returns the one line the command logged with this message.
	logged := func(message string) map[string]any {
		t.Helper()

		records := logs.Take(t, plogtest.Message(message))
		if len(records) != 1 {
			t.Fatalf("%d log lines say %q, want one; logged: %s", len(records), message, logs.String())
		}

		return records[0]
	}

	output, err := runBuilder("publish", file)
	if err != nil || output != "" {
		t.Fatalf("publish returned %v and wrote %q, want no error and nothing on standard output", err, output)
	}

	topology, reference := storedTopology(t, "Pump-station")

	created := logged("topology created")
	if created["name"] != "Pump-station" || created["document"] != reference.ID || created["digest"] != reference.Digest {
		t.Errorf("logged %v, want the topology, the document %s and the digest %s", created, reference.ID, reference.Digest)
	}

	if reference.Path != "" || reference.ID != builder.PublishedDocumentID("Pump-station", reference.Digest) {
		t.Errorf("reference = %+v, want the digest and the ID it derives, and no path", reference)
	}

	nodes, _ := topology.Spec["nodes"].([]any)
	if len(nodes) == 0 || topology.Version != "phenix.sandia.gov/v1" {
		t.Errorf("topology = %s with %d nodes, want the document's nodes as a v1 topology", topology.Version, len(nodes))
	}

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	// The record names the OS account, and the document is the file's: its
	// author and times are not set by publishing.
	record, data, err := service.GetPublishedDocumentData(t.Context(), reference.ID)
	if err != nil {
		t.Fatalf("GetPublishedDocumentData returned error: %v", err)
	}

	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current returned error: %v", err)
	}

	if record.CreatedBy != current.Username || record.DraftID != "" {
		t.Errorf("record = %+v, want it published by %s from no draft", record, current.Username)
	}

	parsed, err := builder.ParseDocumentText(docsExample(t, builderExample))
	if err != nil || !bytes.Equal(parsed.Data, data) {
		t.Errorf("the stored document is not the file's document (error %v)", err)
	}

	// The same file again, and a YAML copy of it under another name.
	var generic any
	if err := json.Unmarshal(docsExample(t, builderExample), &generic); err != nil {
		t.Fatalf("decoding the docs example: %v", err)
	}

	asYAML, err := yaml.Marshal(generic)
	if err != nil {
		t.Fatalf("encoding the docs example as YAML: %v", err)
	}

	for _, again := range []string{file, writeTestFile(t, directory, "copy.builder.yaml", asYAML)} {
		if _, err := runBuilder("publish", again); err != nil {
			t.Fatalf("publishing %s again returned error: %v", again, err)
		}

		if unchanged := logged("topology already up to date"); unchanged["document"] != reference.ID {
			t.Errorf("%s: logged %v, want the document %s", again, unchanged, reference.ID)
		}
	}

	if after, _ := storedTopology(t, "Pump-station"); after.Metadata.Updated != topology.Metadata.Updated {
		t.Errorf("the topology was written again: updated %s, was %s", after.Metadata.Updated, topology.Metadata.Updated)
	}
}

// TestBuilderPublishUpdate replaces a published topology with a changed
// document only with --update, records the user --user names, and publishes
// the same file under a name of the caller's choice.
func TestBuilderPublishUpdate(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	logs := plogtest.Capture(t)
	directory := t.TempDir()
	file := writeTestFile(t, directory, "pump-station.json", docsExample(t, builderExample))
	edited := writeTestFile(t, directory, "edited.json", editedExample(t, "renamed-host"))

	if _, err := runBuilder("publish", file); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}

	_, reference := storedTopology(t, "Pump-station")

	_, err := runBuilder("publish", edited)
	if err == nil || err.Error() != "topology Pump-station already exists; use --update to replace it" {
		t.Fatalf("publishing a changed document: error = %v, want it to name --update", err)
	}

	if _, kept := storedTopology(t, "Pump-station"); kept != reference {
		t.Fatalf("reference = %+v after a refused publication, want %+v", kept, reference)
	}

	if _, err := runBuilder("publish", edited, "--update", "--user", "operator"); err != nil {
		t.Fatalf("publish --update returned error: %v", err)
	}

	updated, updatedReference := storedTopology(t, "Pump-station")

	records := logs.Records(t, plogtest.Message("topology updated"))
	if len(records) != 1 || records[0]["document"] != updatedReference.ID || updatedReference.ID == reference.ID {
		t.Errorf("reference = %+v, logged %v, want the changed document's, logged once", updatedReference, records)
	}

	if encoded, _ := json.Marshal(updated.Spec); !strings.Contains(string(encoded), "renamed-host") {
		t.Errorf("the updated topology does not hold the renamed device: %s", encoded)
	}

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	if record, err := service.GetPublishedDocument(t.Context(), updatedReference.ID); err != nil || record.CreatedBy != "operator" {
		t.Errorf("record = %+v, %v, want it published by operator", record, err)
	}

	// A name of the caller's choice.
	if _, err := runBuilder("publish", file, "-n", "pump-station"); err != nil {
		t.Fatalf("publish -n returned error: %v", err)
	}

	if _, named := storedTopology(t, "pump-station"); named.Digest != reference.Digest || named.ID == reference.ID {
		t.Errorf("reference = %+v, want the first document's digest under an ID of this topology's", named)
	}
}

// TestBuilderPublishDryRun reports on standard output what a publication
// would do, and stores nothing.
func TestBuilderPublishDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	logs := plogtest.Capture(t)
	file := writeTestFile(t, t.TempDir(), "riverside.json", docsExample(t, "riverside-water.builder.json"))

	output, err := runBuilder("publish", file, "--dry-run", "--name", "riverside")
	if err != nil {
		t.Fatalf("publish --dry-run returned error: %v", err)
	}

	parsed, err := builder.ParseDocumentText(docsExample(t, "riverside-water.builder.json"))
	if err != nil {
		t.Fatalf("ParseDocumentText returned error: %v", err)
	}

	absolute, err := filepath.Abs(file)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}

	for _, want := range []string{
		"Document:     Riverside Water\n",
		"File:         " + absolute + "\n",
		"Digest:       " + parsed.Digest + "\n",
		"Document ID:  " + builder.PublishedDocumentID("riverside", parsed.Digest) + "\n",
		"Topology:     riverside (would be created)\n",
		"Warnings:\n",
		"  - The document's scenario is not published: only the topology is.\n",
		"  - Included topology corp-services was not checked for duplicate hostnames: no stored topology has that name.\n",
		"Nothing was written.\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the report does not hold %q:\n%s", want, output)
		}
	}

	if strings.Contains(output, "Path:") {
		t.Errorf("the report names a path although none is recorded:\n%s", output)
	}

	if records := logs.Records(t, nil); len(records) != 0 {
		t.Errorf("a dry run logged %v, want its report on standard output only", records)
	}

	if topologies, err := config.List("topology"); err != nil || len(topologies) != 0 {
		t.Fatalf("topologies = %v, %v, want none after a dry run", topologies, err)
	}

	service, err := builder.New()
	if err != nil {
		t.Fatalf("builder.New returned error: %v", err)
	}

	if documents, err := service.ListPublishedDocuments(t.Context()); err != nil || len(documents) != 0 {
		t.Fatalf("published documents = %v, %v, want none after a dry run", documents, err)
	}

	// Published, the same dry run says nothing would change, and one of a
	// changed document says what --update would do.
	if _, err := runBuilder("publish", file, "--name", "riverside"); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}

	output, err = runBuilder("publish", file, "--dry-run", "--name", "riverside")
	if err != nil || !strings.Contains(output, "Topology:     riverside (would be left as it is: it already holds this document)\n") {
		t.Errorf("dry run of a published document: %v\n%s", err, output)
	}
}

// TestBuilderPublishRefusals returns what is wrong with the file, the
// document or the stored topology as the error's own text, every blocker on
// a line of its own, and stores nothing.
func TestBuilderPublishRefusals(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	directory := t.TempDir()
	file := writeTestFile(t, directory, "pump-station.json", docsExample(t, builderExample))

	// A document with two interfaces on no VLAN, which share an address.
	blocked := bdoc.NewDocument("Blocked lab")

	for _, hostname := range []string{"alpha", "beta"} {
		blocked.Nodes = append(blocked.Nodes, bdoc.Node{
			ID: bdoc.DeviceNodeID(hostname), Kind: bdoc.NodeKindDevice, Label: hostname,
			Device: &bdoc.Device{
				Hostname: hostname,
				Spec: map[string]any{
					"type":     "VirtualMachine",
					"general":  map[string]any{"hostname": hostname},
					"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "ubuntu.qc2"}}},
					"network": map[string]any{"interfaces": []any{map[string]any{
						"name": "eth0", "type": "ethernet", "proto": "static", "address": "10.0.0.1", "mask": 24, "vlan": "",
					}}},
				},
				Interfaces: []bdoc.InterfaceHandle{},
			},
		})
	}

	blockedData, err := builder.EncodeDocument(blocked)
	if err != nil {
		t.Fatalf("EncodeDocument returned error: %v", err)
	}

	for name, test := range map[string]struct {
		args []string
		// says is text the error holds, and lines how many lines it has.
		says  []string
		lines int
	}{
		"a missing file": {
			args: []string{"publish", filepath.Join(directory, "missing.json")},
			says: []string{"unable to read Builder document: open ", "missing.json: no such file or directory"}, lines: 1,
		},
		"a directory": {
			args: []string{"publish", directory}, says: []string{directory + " is not a regular file"}, lines: 1,
		},
		"a config": {
			args: []string{"publish", writeTestFile(t, directory, "topology.yaml", docsExample(t, "pump-station.topology.yaml"))},
			says: []string{"topology.yaml is not a valid Builder document: ", `unknown field "apiVersion"`}, lines: 1,
		},
		"a name that is not a config name": {
			args: []string{"publish", file, "--name", "pump station"},
			says: []string{`"pump station" is not a topology name`}, lines: 1,
		},
		"a path a reference may not hold": {
			args: []string{"publish", writeTestFile(t, directory, "pump-station.txt", docsExample(t, builderExample)), "--record-path"},
			says: []string{"pump-station.txt cannot be recorded: must end in .json, .yaml or .yml"}, lines: 1,
		},
		"a user that is not a name": {
			args: []string{"publish", file, "--user", "a\tb"},
			says: []string{"the user to record as the publisher must not contain control characters"}, lines: 1,
		},
		"blockers": {
			args: []string{"publish", writeTestFile(t, directory, "blocked.json", blockedData), "--dry-run"},
			says: []string{
				"the document cannot be published as topology Blocked-lab:\n",
				"\n  interface \"eth0\" of device \"alpha\" has no VLAN: connect it to a network, or type a VLAN for it",
				"\n  interface \"eth0\" of device \"beta\" has no VLAN: connect it to a network, or type a VLAN for it",
				"\n  IP address 10.0.0.1 is used by interface \"eth0\" of device \"alpha\" and interface \"eth0\" of device \"beta\"",
			},
			lines: 4,
		},
		"no file":   {args: []string{"publish"}, says: []string{"accepts 1 arg(s), received 0", "Usage:"}},
		"two files": {args: []string{"publish", file, file}, says: []string{"accepts 1 arg(s), received 2", "Usage:"}},
	} {
		t.Run(name, func(t *testing.T) {
			output, err := runBuilder(test.args...)
			if err == nil || output != "" {
				t.Fatalf("returned %v and wrote %q, want an error and nothing on standard output", err, output)
			}

			for _, want := range test.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not say %q:\n%v", want, err)
				}
			}

			if got := len(strings.Split(err.Error(), "\n")); test.lines != 0 && got != test.lines {
				t.Errorf("error has %d lines, want %d:\n%v", got, test.lines, err)
			}

			// What the user is to fix is not hidden behind a log line.
			if strings.Contains(err.Error(), "search error logs") {
				t.Errorf("error is humanized: %v", err)
			}
		})
	}

	if topologies, err := config.List("topology"); err != nil || len(topologies) != 0 {
		t.Fatalf("topologies = %v, %v, want none", topologies, err)
	}
}

// TestBuilderPublishRecordPath records the absolute, clean path of the file
// beside the digest and the ID, and warns when the phenix server would not
// read a Builder file from there.
func TestBuilderPublishRecordPath(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	base := t.TempDir()

	// The paths below are compared as written, so the base directory is
	// named as the working directory is.
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}

	previousBase, previousMounts := common.PhenixBase, common.MountBase
	common.PhenixBase, common.MountBase = base, "" //nolint:reassign // the test's own base directory

	t.Cleanup(func() {
		common.PhenixBase, common.MountBase = previousBase, previousMounts //nolint:reassign // restore the directories
	})

	for _, directory := range []string{filepath.Join(base, "topologies"), filepath.Join(base, "mounts")} {
		if err := os.Mkdir(directory, 0o750); err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
	}

	served := writeTestFile(t, filepath.Join(base, "topologies"), "pump-station.builder.yaml", docsExample(t, builderExample))
	mounted := writeTestFile(t, filepath.Join(base, "mounts"), "pump-station.json", docsExample(t, builderExample))
	outside := writeTestFile(t, t.TempDir(), "pump-station.json", docsExample(t, builderExample))

	if resolved, err := filepath.EvalSymlinks(outside); err == nil {
		outside = resolved
	}

	edited := writeTestFile(t, filepath.Join(base, "topologies"), "edited.json", editedExample(t, "renamed-host"))

	logs := plogtest.Capture(t)

	// A relative path with elements to clean: the path recorded is absolute
	// and clean.
	t.Chdir(filepath.Join(base, "mounts"))

	if _, err := runBuilder("publish", filepath.Join("..", "topologies", ".", "pump-station.builder.yaml"), "--record-path"); err != nil {
		t.Fatalf("publish --record-path returned error: %v", err)
	}

	topology, reference := storedTopology(t, "Pump-station")
	if reference.Path != served || reference.Digest == "" || reference.ID == "" {
		t.Fatalf("reference = %+v, want the digest, the ID and the path %s", reference, served)
	}

	// In the config's YAML, the annotation is the map of the three keys.
	body, err := yaml.Marshal(topology)
	if err != nil {
		t.Fatalf("encoding the topology returned error: %v", err)
	}

	var shown struct {
		Metadata struct {
			Annotations map[string]map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}

	if err := yaml.Unmarshal(body, &shown); err != nil {
		t.Fatalf("decoding the topology returned error: %v", err)
	}

	keys := slices.Sorted(maps.Keys(shown.Metadata.Annotations[builder.DocumentAnnotation]))
	if !slices.Equal(keys, []string{"digest", "id", "path"}) {
		t.Errorf("builder-doc holds the keys %v, want digest, id and path", keys)
	}

	// Below the base directory there is nothing to warn of.
	if warnings := logs.Take(t, plogtest.Level(slog.LevelWarn)); len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a file the server reads", warnings)
	}

	// Outside the base directory and below the mount directory the command
	// still succeeds, and warns that the server reads no file there.
	for name, file := range map[string]string{"outside": outside, "mounted": mounted} {
		if _, err := runBuilder("publish", file, "--record-path", "--name", name); err != nil {
			t.Fatalf("%s: publish --record-path returned error: %v", name, err)
		}

		if _, reference := storedTopology(t, name); reference.Path != file {
			t.Errorf("%s: reference = %+v, want the path %s", name, reference, file)
		}

		warnings := logs.Take(t, plogtest.Level(slog.LevelWarn))
		if !slices.ContainsFunc(warnings, func(record map[string]any) bool {
			message, _ := record["msg"].(string)

			return strings.Contains(message, "does not read Builder files from "+file) && strings.Contains(message, base)
		}) {
			t.Errorf("%s: no warning says the server does not read the file: %v", name, warnings)
		}
	}

	// A dry run reports the path, and the same warning.
	output, err := runBuilder("publish", outside, "--record-path", "--name", "dry", "--dry-run")
	if err != nil || !strings.Contains(output, "Path:         "+outside+"\n") ||
		!strings.Contains(output, "  - The phenix server does not read Builder files from "+outside) {
		t.Errorf("dry run: %v\n%s", err, output)
	}

	// An update without the flag keeps the path recorded.
	if _, err := runBuilder("publish", edited, "--update"); err != nil {
		t.Fatalf("publish --update returned error: %v", err)
	}

	if _, kept := storedTopology(t, "Pump-station"); kept.Path != served || kept.ID == reference.ID {
		t.Errorf("reference = %+v, want the path %s kept beside the new document", kept, served)
	}
}

// TestBuilderHelp pins what the help of the command group says, and that
// the CLI names the Builder without a version and as always available.
func TestBuilderHelp(t *testing.T) {
	t.Parallel()

	group, err := runBuilder("--help")
	if err != nil || !strings.Contains(group, "publish     Create or update a topology from a Builder document") {
		t.Fatalf("builder --help: %v\n%s", err, group)
	}

	publish, err := runBuilder("publish", "--help")
	if err != nil {
		t.Fatalf("builder publish --help returned error: %v", err)
	}

	for _, want := range []string{
		"phenix builder publish </path/to/document> [flags]",
		"--dry-run", "-n, --name string", "--record-path", "--update", "--user string",
		"Scenarios and experiments are not created.",
		"The Configs\n  page opens such a topology in the Builder, not as text;",
	} {
		if !strings.Contains(publish, want) {
			t.Errorf("builder publish --help does not say %q:\n%s", want, publish)
		}
	}

	// Sentences the help wraps over lines.
	sentences := strings.Join(strings.Fields(publish), " ")

	for _, want := range []string{
		"only when nothing has changed it since it was published, or the document was imported from it as it is now.",
		"(default: the user who ran sudo, else the current OS user)",
	} {
		if !strings.Contains(sentences, want) {
			t.Errorf("builder publish --help does not say %q:\n%s", want, publish)
		}
	}

	version := regexp.MustCompile(`(?i)builder[ -]v\d`)
	// The Builder has no switch: no help text may say it can be off.
	switched := regexp.MustCompile(`(?i)turned\s+(on|off)|builder\s+is\s+(on|off)\b|feature`)

	for _, help := range []string{group, publish} {
		if version.MatchString(help) {
			t.Errorf("the help names a Builder version:\n%s", help)
		}

		if found := switched.FindString(help); found != "" {
			t.Errorf("the help says the Builder can be turned on or off (%q):\n%s", found, help)
		}
	}
}

// TestBuilderPublishReportsStoreFailure humanizes a failure that is not the
// user's to fix: a config write the store refuses.
func TestBuilderPublishReportsStoreFailure(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	useBoltStore(t)

	file := writeTestFile(t, t.TempDir(), "pump-station.json", docsExample(t, builderExample))

	failing := &failingConfigStore{Store: store.DefaultStore, err: errors.New("injected store failure")}
	store.DefaultStore = failing //nolint:reassign // useBoltStore restores the store

	_, err := runBuilder("publish", file)
	if err == nil || !strings.HasPrefix(err.Error(), "Unable to publish Builder document "+file+" (search error logs for ") {
		t.Fatalf("error = %v, want the humanized store failure", err)
	}
}

// failingConfigStore is a phenix store that refuses to create a config.
type failingConfigStore struct {
	store.Store

	err error
}

func (s *failingConfigStore) Create(*store.Config) error { return s.err }
