package config

import (
	"testing"

	"phenix/store"

	"github.com/golang/mock/gomock"
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
	m.EXPECT().List(gomock.Eq("Topology"), gomock.Eq("Scenario"), gomock.Eq("Experiment"), gomock.Eq("Image")).Return(configs, nil).AnyTimes()

	store.DefaultStore = m

	_, err := List("blech")
	if err == nil {
		t.Log("expected error")
		t.FailNow()
	}
}
