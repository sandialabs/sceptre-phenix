package config_test

import (
	"context"
	"errors"
	"fmt"
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

// List refuses a kind that is not a config kind.
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

	m := useMockStore(t)
	m.EXPECT().
		List(gomock.Eq("Topology"), gomock.Eq("Scenario"), gomock.Eq("Experiment"), gomock.Eq("Image")).
		Return(configs, nil).
		AnyTimes()

	if _, err := config.List("blech"); err == nil {
		t.Error(`config.List("blech") error = nil, want an error`)
	}
}

// Create expands ${BRANCH_NAME} in the YAML it is given.
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

	m := useMockStore(t)
	m.EXPECT().Create(gomock.Eq(&expected)).Return(nil).AnyTimes()

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
// The "update" hooks see the stored config as c.Stored, which Update clears after.
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

	config.RegisterConfigHook("Scenario", func(_ context.Context, stage string, c *store.Config) error {
		stages = append(stages, stage+" "+c.Metadata.Name+" "+c.Metadata.Annotations["note"])

		if stage == "update" && (c.Stored == nil || c.Stored.Metadata.Name != "old") {
			stages = append(stages, fmt.Sprintf("update stored %+v, want the config named old", c.Stored))
		}

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

			if c.Stored != nil {
				t.Errorf("config.Update(scenario/old) left c.Stored = %+v, want nil", c.Stored)
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

// useMockStore makes a gomock store the package store until the test ends.
// The mock fails the test on any call it does not expect.
func useMockStore(t *testing.T) *store.MockStore {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))

	original := store.DefaultStore
	store.DefaultStore = m                              //nolint:reassign // mocking
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	return m
}

// storedAt makes a mocked Get read the named config at a revision.
func storedAt(revision uint64) func(*store.Config) error {
	return func(c *store.Config) error {
		c.Metadata.Created = "then"
		c.Metadata.Revision = revision

		return nil
	}
}

// An update that names a revision the config has moved past is refused before
// hooks run or anything is written, including on the rename path.
func TestUpdateIfRevisionConflicts(t *testing.T) {
	for _, newName := range []string{"a", "renamed"} {
		t.Run("to "+newName, func(t *testing.T) {
			m := useMockStore(t)
			m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(3))

			c, err := store.NewConfig("topology/" + newName)
			if err != nil {
				t.Fatalf("store.NewConfig(%q) error = %v", "topology/"+newName, err)
			}

			c.IfRevision = 2

			if err := config.Update("topology/a", c); !errors.Is(err, store.ErrConflict) {
				t.Errorf("Update(topology/a, topology/%s at stale revision 2) error = %v, want %v", newName, err, store.ErrConflict)
			}
		})
	}
}

// config edit refuses to save over a config that changed while it was being
// edited, whatever revision the edited YAML gives.
func TestEditIfRevisionConflicts(t *testing.T) {
	// the "editor" sets the revision in the YAML to the config's new one, and fails if the YAML has no revision 1
	editor := filepath.Join(t.TempDir(), "editor")

	script := "#!/bin/sh\ngrep -q 'revision: 1$' \"$1\" || exit 1\n" +
		"sed 's/revision: 1$/revision: 2/' \"$1\" > \"$1.new\" && mv \"$1.new\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the test editor: %v", err)
	}

	t.Setenv("EDITOR", editor)

	m := useMockStore(t)
	gomock.InOrder(
		m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(1)), // read for the editor
		m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(2)), // changed meanwhile
	)

	if _, err := config.Edit("topology/a", false); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Edit(topology/a) of a config changed meanwhile error = %v, want %v", err, store.ErrConflict)
	}
}

// A rename whose delete of the old config fails removes the renamed copy, leaving only the old config.
func TestFailedRenameLeavesOnlyTheOldConfig(t *testing.T) {
	m := useMockStore(t)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(5))
	m.EXPECT().Update(gomock.Any()).Return(store.ErrNotExist)
	m.EXPECT().Create(gomock.Any()).Return(nil)

	var deleted []string

	m.EXPECT().Delete(gomock.Any()).Times(2).DoAndReturn(func(c *store.Config) error {
		if c.Metadata.Name == "a" {
			return store.ErrConflict // changed by a concurrent writer
		}

		deleted = append(deleted, c.Metadata.Name)

		return nil
	})

	c := validateTestTopology(t, "phenix.sandia.gov/v1", "b", "{nodes: []}")
	c.IfRevision = 5

	if err := config.Update("topology/a", c); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Update(topology/a, topology/b) with a failing delete error = %v, want %v", err, store.ErrConflict)
	}

	// only the renamed copy is removed
	if diff := cmp.Diff([]string{"b"}, deleted); diff != "" {
		t.Errorf("Update(topology/a, topology/b) deleted configs mismatch (-want +got):\n%s", diff)
	}
}
