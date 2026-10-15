package types

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v2 "phenix/types/version/v2"
)

func init() { //nolint:gochecknoinits // interface assertion
	var spec any

	spec = new(v2.ScenarioSpec)
	_, _ = spec.(ifaces.ScenarioSpec)

	spec = new(v2.ScenarioApp)
	_, _ = spec.(ifaces.ScenarioApp)

	spec = new(v2.ScenarioAppHost)
	_, _ = spec.(ifaces.ScenarioAppHost)
}

func DecodeScenarioFromConfig(c store.Config) (ifaces.ScenarioSpec, error) { //nolint:ireturn // interface
	if c.APIVersion() != version.StoredVersion[c.Kind] {
		return nil, fmt.Errorf("unknown scenario version %s", c.APIVersion())
	}

	iface, err := version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
	if err != nil {
		return nil, fmt.Errorf("getting versioned spec for config: %w", err)
	}

	if err := mapstructure.Decode(c.Spec, &iface); err != nil {
		return nil, fmt.Errorf("decoding versioned spec: %w", err)
	}

	spec, ok := iface.(ifaces.ScenarioSpec)
	if !ok {
		return nil, errors.New("invalid spec in config")
	}

	return spec, nil
}

func MakeCustomScenarioFromConfig( //nolint:ireturn // interface
	c store.Config,
	disabledApps []string,
) (ifaces.ScenarioSpec, error) {
	// Get base spec from config, going to use this to create a custom config
	spec, err := DecodeScenarioFromConfig(c)
	if err != nil {
		return nil, fmt.Errorf("error make custom scenario: %w", err)
	}

	// if app name in disabled app list, set to disabled
	for _, app := range spec.Apps() {
		if slices.Contains(disabledApps, app.Name()) {
			app.SetDisabled(true)
		}
	}

	return spec, nil
}

// CheckScenarioTopology checks that annotations, those of the scenario named
// name, mark it for topology, as taking app settings from it requires.
func CheckScenarioTopology(name string, annotations store.Annotations, topology string) error {
	topo, ok := annotations["topology"]
	if !ok {
		return fmt.Errorf("topology annotation missing from scenario %s", name)
	}

	if !strings.Contains(topo, topology) {
		return fmt.Errorf("experiment/scenario topology mismatch for scenario %s", name)
	}

	return nil
}

// FromScenarioConfig reads the stored scenario named name, which an app takes
// its settings from, and checks that it is marked for topology.
func FromScenarioConfig(name, topology string) (*store.Config, error) {
	c, err := store.NewConfig("scenario/" + name)
	if err == nil {
		err = store.Get(c)
	}

	if err != nil {
		return nil, fmt.Errorf("scenario %s doesn't exist", name)
	}

	if err := CheckScenarioTopology(name, c.Metadata.Annotations, topology); err != nil {
		return nil, err
	}

	return c, nil
}

func MergeScenariosForTopology(scenario ifaces.ScenarioSpec, topology string) error {
	// This will look for `fromScenario` keys in the provided scenario and, if
	// present, replace the config from the specified scenario.
	for _, app := range scenario.Apps() {
		if app.FromScenario() != "" {
			fromScenarioC, err := FromScenarioConfig(app.FromScenario(), topology)
			if err != nil {
				return err
			}

			fromScenario, err := DecodeScenarioFromConfig(*fromScenarioC)
			if err != nil {
				return fmt.Errorf("decoding scenario %s from config: %w", app.FromScenario(), err)
			}

			var found bool

			for _, fromApp := range fromScenario.Apps() {
				if fromApp.Name() == app.Name() {
					app.SetAssetDir(fromApp.AssetDir())
					app.SetMetadata(fromApp.Metadata())
					app.SetHosts(fromApp.Hosts())
					app.SetDisabled(fromApp.Disabled())
					app.SetRunPeriodically(fromApp.RunPeriodically())

					found = true

					break
				}
			}

			if !found {
				return fmt.Errorf("no app named %s in scenario %s", app.Name(), app.FromScenario())
			}
		}
	}

	return nil
}
