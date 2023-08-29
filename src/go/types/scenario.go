package types

import (
	"fmt"
	"strings"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"

	"github.com/mitchellh/mapstructure"
	"golang.org/x/exp/slices"
)

func init() {
	var spec interface{}

	spec = new(v2.ScenarioSpec)
	_ = spec.(ifaces.ScenarioSpec)

	spec = new(v2.ScenarioApp)
	_ = spec.(ifaces.ScenarioApp)

	spec = new(v2.ScenarioAppHost)
	_ = spec.(ifaces.ScenarioAppHost)
}

func DecodeScenarioFromConfig(c store.Config) (ifaces.ScenarioSpec, error) {
	var (
		iface         interface{}
		latestVersion = version.StoredVersion[c.Kind]
	)

	if c.APIVersion() != latestVersion {
		version := c.Kind + "/" + latestVersion
		upgrader := GetUpgrader(version)

		if upgrader == nil {
			return nil, fmt.Errorf("no upgrader found for scenario version %s", latestVersion)
		}

		var err error

		iface, err = upgrader.Upgrade(c.APIVersion(), c.Spec, c.Metadata)
		if err != nil {
			return nil, fmt.Errorf("upgrading scenario to %s: %w", latestVersion, err)
		}
	} else {
		var err error

		iface, err = version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
		if err != nil {
			return nil, fmt.Errorf("getting versioned spec for config: %w", err)
		}

		if err := mapstructure.Decode(c.Spec, &iface); err != nil {
			return nil, fmt.Errorf("decoding versioned spec: %w", err)
		}
	}

	spec, ok := iface.(ifaces.ScenarioSpec)
	if !ok {
		return nil, fmt.Errorf("invalid spec in config")
	}

	return spec, nil
}

func MakeCustomScenarioFromConfig(c store.Config, disabledApps []string) (ifaces.ScenarioSpec, error) {
	//Get base spec from config, going to use this to create a custom config
	spec, err := DecodeScenarioFromConfig(c)
	if err != nil {
		return nil, fmt.Errorf("Error make custom scenario: %w", err)
	}

	//if app name in disabled app list, set to disabled
	for _, app := range spec.Apps() {
		app.SetDisabled(slices.Contains(disabledApps, app.Name()))
	}

	return spec, nil
}

func MergeScenariosForTopology(scenario ifaces.ScenarioSpec, topology string) error {
	// This will look for `fromScenario` keys in the provided scenario and, if
	// present, replace the config from the specified scenario.
	for _, app := range scenario.Apps() {
		if app.FromScenario() != "" {
			fromScenarioC, _ := store.NewConfig("scenario/" + app.FromScenario())

			if err := store.Get(fromScenarioC); err != nil {
				return fmt.Errorf("scenario %s doesn't exist", app.FromScenario())
			}

			topo, ok := fromScenarioC.Metadata.Annotations["topology"]
			if !ok {
				return fmt.Errorf("topology annotation missing from scenario %s", app.FromScenario())
			}

			if !strings.Contains(topo, topology) {
				return fmt.Errorf("experiment/scenario topology mismatch for scenario %s", app.FromScenario())
			}

			// This will upgrade the scenario to the latest known version if needed.
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

type scenario struct{}

func (scenario) Upgrade(version string, spec map[string]interface{}, md store.ConfigMetadata) (interface{}, error) {
	if version == "v1" {
		var (
			V1 = new(v1.ScenarioSpec)
			V2 = new(v2.ScenarioSpec)
		)

		if err := mapstructure.WeakDecode(spec, &V1); err != nil {
			return nil, fmt.Errorf("decoding scenario into v1 spec: %w", err)
		}

		for _, exp := range V1.AppsF.ExperimentF {
			app := &v2.ScenarioApp{
				NameF:     exp.NameF,
				AssetDirF: exp.AssetDirF,
				MetadataF: exp.MetadataF,
			}

			V2.AppsF = append(V2.AppsF, app)
		}

		for _, host := range V1.AppsF.HostF {
			hosts := make([]*v2.ScenarioAppHost, len(host.HostsF))

			for i, h1 := range host.HostsF {
				hosts[i] = &v2.ScenarioAppHost{
					HostnameF: h1.HostnameF,
					MetadataF: h1.MetadataF,
				}
			}

			app := &v2.ScenarioApp{
				NameF:     host.NameF,
				AssetDirF: host.AssetDirF,
				HostsF:    hosts,
			}

			V2.AppsF = append(V2.AppsF, app)
		}

		return V2, nil
	}

	return nil, fmt.Errorf("unknown version %s to upgrade from", version)
}

func init() {
	RegisterUpgrader("Scenario/v2", new(scenario))
}
