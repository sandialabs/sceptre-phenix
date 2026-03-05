package config_test

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/config"
	"phenix/store"
)

func TestListError(t *testing.T) {
	configs := store.Configs(
		[]store.Config{
			{ //nolint:exhaustruct // test data
				Version: "phenix.sandia.gov/v1",
				Kind:    "Experiment",
				Metadata: store.ConfigMetadata{ //nolint:exhaustruct // test data
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
	expected := store.Config{ //nolint:exhaustruct // test data
		Version: "phenix.sandia.gov/v1",
		Kind:    "Topology",
		Metadata: store.ConfigMetadata{ //nolint:exhaustruct // test data
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
