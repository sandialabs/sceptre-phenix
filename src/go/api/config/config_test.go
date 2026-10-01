package config_test

import (
	"errors"
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
