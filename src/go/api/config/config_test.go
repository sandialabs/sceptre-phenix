package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/config"
	"phenix/store"
)

func TestListError(t *testing.T) {
	configs := store.Configs(
		[]store.Config{
			{
				Version: "phenix.sandia.gov/v1",
				Kind:    "Experiment",
				Metadata: store.ConfigMetadata{
					Name: "test-experiment",
				},
			},
		},
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().
		List(gomock.Eq("Topology"), gomock.Eq("Scenario"), gomock.Eq("Experiment"), gomock.Eq("Image")).
		Return(configs, nil).
		AnyTimes()

	store.DefaultStore = m //nolint:reassign // mocking

	_, err := config.List("blech")
	if err == nil {
		t.Log("expected error")
		t.FailNow()
	}
}

func TestCreateEnv(t *testing.T) {
	expected := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Topology",
		Metadata: store.ConfigMetadata{
			Name: "foobar-test-experiment",
		},
	}

	cfg := `
	{
		"apiVersion": "phenix.sandia.gov/v1",
		"kind": "Topology",
		"metadata": {
			"name": "${BRANCH_NAME}-test-experiment"
		}
	}
	`

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().Create(gomock.Eq(&expected)).Return(nil).AnyTimes()

	store.DefaultStore = m //nolint:reassign // mocking

	t.Setenv("BRANCH_NAME", "foobar")
	options := []config.CreateOption{config.CreateFromJSON([]byte(cfg))}

	_, err := config.Create(options...)
	if err != nil {
		t.Log(err)
		t.FailNow()
	}
}

// TestEditKeepsStrings edits a config whose strings start with a line break
// or a tab, with an editor that adds only a comment, and asserts the config
// saved is the config read.
func TestEditKeepsStrings(t *testing.T) {
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

	editor := filepath.Join(t.TempDir(), "editor")
	//nolint:gosec // the editor the test runs
	if err := os.WriteFile(editor, []byte("#!/bin/sh\necho '# edited' >> \"$1\"\n"), 0o700); err != nil {
		t.Fatalf("writing the editor: %v", err)
	}

	t.Setenv("EDITOR", editor)

	var saved store.Config

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		*c = stored()

		return nil
	}).Times(2)
	m.EXPECT().Update(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		saved = *c

		return nil
	})

	previous := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // mocking

	t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // mocking

	if _, err := config.Edit("scenario/exact", false); err != nil {
		t.Fatalf("Edit returned error: %v", err)
	}

	got, _ := json.Marshal(saved)
	want, _ := json.Marshal(stored())

	if string(got) != string(want) {
		t.Fatalf("saved %s, want %s", got, want)
	}
}
