package settings_test

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"

	"phenix/api/settings"
	"phenix/store"
	"phenix/store/storetest"
)

// TestPasswordSettingsStoredByAnotherRequest checks that a default another request
// stored first is read instead of failing on the duplicate.
func TestPasswordSettingsStoredByAnotherRequest(t *testing.T) {
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

	storetest.Use(t, m)

	got, err := settings.GetPasswordSettings()
	if err != nil {
		t.Fatalf("GetPasswordSettings() error = %v", err)
	}

	want := settings.PasswordSettings{
		LowercaseReq: true, UppercaseReq: false, NumberReq: true, SymbolReq: true,
		MinLength: settings.DefaultPasswordMinLength,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetPasswordSettings() mismatch (-want +got):\n%s", diff)
	}
}
