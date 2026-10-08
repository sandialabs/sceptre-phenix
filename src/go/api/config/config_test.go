package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
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

// TestUpdateRenameRunsRenameHooks updates a config under a new name, which
// stores it under that name and deletes it under the old one: the kind's
// hooks then run their "rename" stage with the config as it was, and an error
// of theirs is returned. An update that keeps the name, or a rename the store
// refuses, runs none.
func TestUpdateRenameRunsRenameHooks(t *testing.T) {
	stored := func(name string) store.Config {
		return store.Config{
			Version: "phenix.sandia.gov/v2",
			Kind:    "Scenario",
			Metadata: store.ConfigMetadata{
				Name: name, Created: "created", Updated: "", Annotations: store.Annotations{"note": name},
			},
			Spec:   map[string]any{"apps": []any{map[string]any{"name": "app"}}},
			Status: nil,
		}
	}

	var (
		stages  []string
		hookErr error
	)

	config.RegisterConfigHook("Scenario", func(stage string, c *store.Config) error {
		stages = append(stages, stage+" "+c.Metadata.Name+" "+c.Metadata.Annotations["note"])

		if stage == "rename" {
			return hookErr
		}

		return nil
	})

	errHook := errors.New("injected hook failure")
	errStore := errors.New("injected store failure")

	for _, test := range []struct {
		name      string
		as        string
		deleteErr error
		hookErr   error
		stages    []string
	}{
		{name: "renamed", as: "new", stages: []string{"update new old", "rename old old"}},
		{name: "renamed, a hook fails", as: "new", hookErr: errHook, stages: []string{"update new old", "rename old old"}},
		{name: "the old name cannot be deleted", as: "new", deleteErr: errStore, stages: []string{"update new old"}},
		{name: "the name is kept", as: "old", stages: []string{"update old old"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stages, hookErr = nil, test.hookErr

			m := store.NewMockStore(gomock.NewController(t))
			m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				*c = stored("old")

				return nil
			})

			if test.as == "old" {
				m.EXPECT().Update(gomock.Any()).Return(nil)
			} else {
				m.EXPECT().Update(gomock.Any()).Return(store.ErrNotExist)
				m.EXPECT().Create(gomock.Any()).Return(nil)
				m.EXPECT().Delete(gomock.Any()).DoAndReturn(func(c *store.Config) error {
					if c.Metadata.Name != "old" {
						t.Errorf("deleted %s, want the old name", c.Metadata.Name)
					}

					return test.deleteErr
				})
			}

			if test.deleteErr != nil {
				m.EXPECT().Delete(gomock.Any()).Return(nil) // the config stored under the new name
			}

			previous := store.DefaultStore
			store.DefaultStore = m //nolint:reassign // mocking

			t.Cleanup(func() { store.DefaultStore = previous }) //nolint:reassign // mocking

			c := stored("old")
			c.Metadata.Name = test.as

			err := config.Update("scenario/old", &c)

			switch {
			case test.hookErr != nil && !errors.Is(err, test.hookErr):
				t.Errorf("Update error = %v, want the hook's", err)
			case test.deleteErr != nil && !errors.Is(err, test.deleteErr):
				t.Errorf("Update error = %v, want the store's", err)
			case test.hookErr == nil && test.deleteErr == nil && err != nil:
				t.Errorf("Update returned error: %v", err)
			}

			if !slices.Equal(stages, test.stages) {
				t.Errorf("hook stages = %q, want %q", stages, test.stages)
			}
		})
	}
}

// TestCreateDefaultNamesABuiltinConfig asserts CreateDefault stores only a
// config the built-in defaults hold, and says so of any other.
func TestCreateDefaultNamesABuiltinConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Kind != "Role" || c.Metadata.Name != "builder" || c.Spec["roleName"] != "Builder" {
			t.Errorf("CreateDefault stored %s with %v", c.FullName(), c.Spec)
		}

		return nil
	}).Times(1)

	store.DefaultStore = m //nolint:reassign // mocking

	if _, err := config.CreateDefault("role", "builder"); err != nil {
		t.Fatalf("CreateDefault(role, builder) returned error: %v", err)
	}

	if _, err := config.CreateDefault("Role", "no-such-role"); err == nil {
		t.Fatal("CreateDefault of a config the defaults do not hold returned no error")
	}
}

// TestWorkflowIsNotAConfigKind proves the Workflow schema did not make
// workflow configs listable or storable through the config API. The mock has
// no expectations, so any store call fails the test.
func TestWorkflowIsNotAConfigKind(t *testing.T) {
	if slices.ContainsFunc(config.AllKinds, func(kind string) bool { return strings.EqualFold(kind, "Workflow") }) {
		t.Fatalf("AllKinds = %v, includes Workflow", config.AllKinds)
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	original := store.DefaultStore
	store.DefaultStore = store.NewMockStore(ctrl)       //nolint:reassign // mocking
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	if _, err := config.List("workflow"); err == nil {
		t.Error(`List("workflow") returned no error`)
	}

	doc := "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata:\n  name: wf\nspec:\n  topology: helloworld\n"

	_, err := config.Create(config.CreateFromYAML([]byte(doc)), config.CreateWithValidation())
	if !errors.Is(err, types.ErrValidationFailed) {
		t.Errorf("Create(Workflow config) error = %v, want ErrValidationFailed", err)
	}
}
