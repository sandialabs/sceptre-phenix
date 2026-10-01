package settings_test

import (
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/settings"
	"phenix/store"
)

// TestPasswordSettingsStoredByAnotherRequest asserts reading the settings of
// a new server succeeds when another request stores a default first: the
// stored value is read instead of failing on the duplicate.
func TestPasswordSettingsStoredByAnotherRequest(t *testing.T) { //nolint:paralleltest // replaces the default store
	ctrl := gomock.NewController(t)

	m := store.NewMockStore(ctrl)
	m.EXPECT().List(gomock.Eq("Setting")).Return(store.Configs{}, nil)
	m.EXPECT().Create(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Metadata.Name == "Password.SymbolReq" {
			return store.ErrExist
		}

		return nil
	}).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Metadata.Name != "Password.SymbolReq" {
			t.Errorf("Get(%s), want only the setting that existed", c.Metadata.Name)
		}

		c.Spec = map[string]any{"category": "Password", "name": "SymbolReq", "type": "bool", "value": "true"}

		return nil
	})

	original := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // install test double

	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	got, err := settings.GetPasswordSettings()
	if err != nil {
		t.Fatalf("GetPasswordSettings returned error: %v", err)
	}

	if !got.SymbolReq || got.MinLength != settings.DefaultPasswordMinLength {
		t.Errorf("GetPasswordSettings = %+v, want the stored SymbolReq and the default length", got)
	}
}
