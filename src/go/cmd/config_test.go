package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
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
// CLI runs the Topology config hook Builder v2 registers.
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
