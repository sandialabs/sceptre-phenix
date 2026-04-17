package experiment_test

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/experiment"
	"phenix/store"
)

func TestList(t *testing.T) {
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
	m.EXPECT().List(gomock.Eq("Experiment")).Return(configs, nil)

	store.DefaultStore = m //nolint:reassign // monkey patching for test

	c, err := experiment.List()
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	if len(c) != 1 {
		t.Log("expecting 1 config")
		t.FailNow()
	}
}
