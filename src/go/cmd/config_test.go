package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/store"
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
