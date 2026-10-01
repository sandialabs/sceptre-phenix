package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"

	"phenix/api/config"
	"phenix/store"
	"phenix/types"
)

// validateTestTopology parses a topology config with the given apiVersion,
// name and spec, the last as a YAML flow mapping.
func validateTestTopology(t *testing.T, apiVersion, name, spec string) *store.Config {
	t.Helper()

	src := "apiVersion: " + apiVersion + "\nkind: Topology\nmetadata:\n  name: " + name + "\nspec: " + spec + "\n"

	c, err := store.NewConfigFromYAML([]byte(src))
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	return c
}

// installValidateTestStore installs a store mock that fails the test on any
// call it does not expect, and restores the previous store afterwards.
func installValidateTestStore(t *testing.T) *store.MockStore {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	m := store.NewMockStore(ctrl)
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	return m
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	installValidateTestStore(t) // Validate never touches the store

	c := validateTestTopology(t, "phenix.sandia.gov/v1", "demo", "{nodes: []}")
	if err := config.Validate(c); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

// TestValidateMatchesCreateAndUpdate proves that a dry run rejects a config
// with the same error, byte for byte, as a real create or update, so the web
// layer explains both the same way.
func TestValidateMatchesCreateAndUpdate(t *testing.T) {
	tests := []struct {
		name       string
		apiVersion string
		configName string
		spec       string
	}{
		{
			name:       "spec schema",
			apiVersion: "phenix.sandia.gov/v1",
			configName: "demo",
			spec:       "{nodes: [{type: VirtualMachine}]}",
		},
		{
			name:       "config schema",
			apiVersion: "phenix.sandia.gov/v1",
			configName: "bad name",
			spec:       "{nodes: []}",
		},
		{
			name:       "API group",
			apiVersion: "example.com/v1",
			configName: "demo",
			spec:       "{nodes: []}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := installValidateTestStore(t)
			m.EXPECT().Get(gomock.Any()).Return(nil) // config.Update reads the stored config first

			build := func() *store.Config {
				return validateTestTopology(t, tt.apiVersion, tt.configName, tt.spec)
			}

			err := config.Validate(build())
			if !errors.Is(err, types.ErrValidationFailed) {
				t.Fatalf("Validate() error = %v, want ErrValidationFailed", err)
			}

			if !strings.HasPrefix(err.Error(), "validating config: ") {
				t.Errorf("Validate() error = %q, want the prefix %q", err, "validating config: ")
			}

			_, createErr := config.Create(config.CreateFromConfig(build()), config.CreateWithValidation())
			if createErr == nil || createErr.Error() != err.Error() {
				t.Errorf("Create() error:\n got %v\nwant %v", createErr, err)
			}

			if updateErr := config.Update("topology/demo", build()); updateErr == nil || updateErr.Error() != err.Error() {
				t.Errorf("Update() error:\n got %v\nwant %v", updateErr, err)
			}
		})
	}
}
