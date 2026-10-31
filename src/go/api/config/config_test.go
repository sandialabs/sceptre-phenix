package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
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

	if _, err := config.List("blech"); err == nil {
		t.Error(`config.List("blech") error = nil, want an error`)
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
apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: ${BRANCH_NAME}-test-experiment
`

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	m := store.NewMockStore(ctrl)
	m.EXPECT().Create(gomock.Eq(&expected)).Return(nil).AnyTimes()

	store.DefaultStore = m //nolint:reassign // mocking

	t.Setenv("BRANCH_NAME", "foobar")
	options := []config.CreateOption{config.CreateFromYAML([]byte(cfg))}

	if _, err := config.Create(options...); err != nil {
		t.Errorf("config.Create(YAML naming ${BRANCH_NAME}) error = %v, want nil", err)
	}
}

// TestEditKeepsStrings checks that an edit adding only a comment saves strings that
// start with a line break or tab unchanged.
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

	storetest.Use(t, m)

	if _, err := config.Edit("scenario/exact", false); err != nil {
		t.Fatalf("config.Edit(scenario/exact) error = %v", err)
	}

	if diff := cmp.Diff(stored(), saved); diff != "" {
		t.Errorf("config.Edit(scenario/exact) saved config mismatch (-want +got):\n%s", diff)
	}
}

// TestUpdateRenameRunsRenameHooks checks that a rename runs the "rename" hooks with the
// old config and returns their error. A kept name or a failed delete runs none.
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

			storetest.Use(t, m)

			c := stored("old")
			c.Metadata.Name = test.as

			err := config.Update("scenario/old", &c)

			switch {
			case test.hookErr != nil && !errors.Is(err, test.hookErr):
				t.Errorf("config.Update(scenario/old) error = %v, want %v", err, test.hookErr)
			case test.deleteErr != nil && !errors.Is(err, test.deleteErr):
				t.Errorf("config.Update(scenario/old) error = %v, want %v", err, test.deleteErr)
			case test.hookErr == nil && test.deleteErr == nil && err != nil:
				t.Errorf("config.Update(scenario/old) error = %v, want nil", err)
			}

			if !slices.Equal(stages, test.stages) {
				t.Errorf("config.Update(scenario/old) hook stages = %q, want %q", stages, test.stages)
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
	type stored struct {
		Kind, Name string
		RoleName   any
	}

	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		got := stored{Kind: c.Kind, Name: c.Metadata.Name, RoleName: c.Spec["roleName"]}
		if diff := cmp.Diff(stored{Kind: "Role", Name: "builder", RoleName: "Builder"}, got); diff != "" {
			t.Errorf("config.CreateDefault(role, builder) stored config mismatch (-want +got):\n%s", diff)
		}

		return nil
	}).Times(1)

	storetest.Use(t, m)

	if _, err := config.CreateDefault("role", "builder"); err != nil {
		t.Errorf("config.CreateDefault(role, builder) error = %v, want nil", err)
	}

	if _, err := config.CreateDefault("Role", "no-such-role"); err == nil {
		t.Error("config.CreateDefault(Role, no-such-role) error = nil, want an error")
	}
}

// TestWorkflowIsNotAConfigKind proves the Workflow schema did not make
// workflow configs listable or storable through the config API. The mock has
// no expectations, so any store call fails the test.
func TestWorkflowIsNotAConfigKind(t *testing.T) {
	if slices.ContainsFunc(config.AllKinds, func(kind string) bool { return strings.EqualFold(kind, "Workflow") }) {
		t.Errorf("config.AllKinds = %v, want no Workflow", config.AllKinds)
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	original := store.DefaultStore
	store.DefaultStore = store.NewMockStore(ctrl)       //nolint:reassign // mocking
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	if _, err := config.List("workflow"); err == nil {
		t.Error(`config.List("workflow") error = nil, want an error`)
	}

	doc := "apiVersion: phenix.sandia.gov/v0\nkind: Workflow\nmetadata:\n  name: wf\nspec:\n  topology: helloworld\n"

	_, err := config.Create(config.CreateFromYAML([]byte(doc)), config.CreateWithValidation())
	if !errors.Is(err, types.ErrValidationFailed) {
		t.Errorf("config.Create(Workflow config) error = %v, want ErrValidationFailed", err)
	}
}
