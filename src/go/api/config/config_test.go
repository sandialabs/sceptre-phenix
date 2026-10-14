package config_test

import (
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
		m := useMockStore(t)
		m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(3))

		c, _ := store.NewConfig("topology/" + newName)
		c.IfRevision = 2

		if err := config.Update("topology/a", c); !errors.Is(err, store.ErrConflict) {
			t.Errorf("update to %s at a stale revision: err = %v, want ErrConflict", newName, err)
		}
	}
}

// config edit refuses to save over a config that changed while it was being
// edited, whatever revision the edited YAML gives.
func TestEditIfRevisionConflicts(t *testing.T) {
	// the "editor" sets the revision in the YAML to the config's new one
	editor := filepath.Join(t.TempDir(), "editor")

	script := "#!/bin/sh\nsed 's/revision: 1$/revision: 2/' \"$1\" > \"$1.new\" && mv \"$1.new\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test editor
		t.Fatal(err)
	}

	t.Setenv("EDITOR", editor)

	m := useMockStore(t)
	gomock.InOrder(
		m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(1)), // read for the editor
		m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(2)), // changed meanwhile
	)

	if _, err := config.Edit("topology/a", false); !errors.Is(err, store.ErrConflict) {
		t.Errorf("edit of a config changed meanwhile: err = %v, want ErrConflict", err)
	}
}

// A rename whose delete of the old config fails removes the renamed copy it
// created, although the update named the old config's revision.
func TestUpdateRenameRollsBackCreate(t *testing.T) {
	m := useMockStore(t)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(storedAt(5))
	m.EXPECT().Update(gomock.Any()).Return(store.ErrNotExist)
	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		c.Metadata.Revision = 9

		return nil
	})

	var deleted []string

	m.EXPECT().Delete(gomock.Any()).Times(2).DoAndReturn(func(c *store.Config) error {
		if c.Metadata.Name == "a" {
			return store.ErrConflict // changed by a concurrent writer
		}

		if c.IfRevision != 0 && c.IfRevision != c.Metadata.Revision {
			return store.ErrConflict
		}

		deleted = append(deleted, c.Metadata.Name)

		return nil
	})

	c := validateTestTopology(t, "phenix.sandia.gov/v1", "b", "{nodes: []}")
	c.IfRevision = 5

	if err := config.Update("topology/a", c); !errors.Is(err, store.ErrConflict) {
		t.Errorf("rename with a failing delete: err = %v, want ErrConflict", err)
	}

	if !slices.Equal(deleted, []string{"b"}) {
		t.Errorf("deleted %v, want the renamed copy [b]", deleted)
	}
}
