package types

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/mitchellh/mapstructure"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v1 "phenix/types/version/v1"
)

func DecodeTopologyFromConfig(c store.Config) (ifaces.TopologySpec, error) { //nolint:ireturn // interface
	return decodeTopologyRecursive(c, map[string]bool{})
}

func decodeTopologyRecursive( //nolint:ireturn // interface
	c store.Config,
	visited map[string]bool,
) (ifaces.TopologySpec, error) {
	if visited[c.Metadata.Name] {
		return nil, fmt.Errorf("cyclic import detected: %s", c.Metadata.Name)
	}

	newVisited := make(map[string]bool)
	maps.Copy(newVisited, visited)

	newVisited[c.Metadata.Name] = true

	if c.APIVersion() != version.StoredVersion[c.Kind] {
		return nil, fmt.Errorf("unknown topology version %s", c.APIVersion())
	}

	iface, err := version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
	if err != nil {
		return nil, fmt.Errorf("getting versioned spec for config: %w", err)
	}

	if err := mapstructure.WeakDecode(c.Spec, &iface); err != nil {
		return nil, fmt.Errorf("decoding versioned spec: %w", err)
	}

	spec, ok := iface.(ifaces.TopologySpec)
	if !ok {
		return nil, errors.New("invalid spec in config")
	}

	if v1Spec, ok := spec.(*v1.TopologySpec); ok {
		for _, include := range v1Spec.IncludeTopologiesF {
			childConfig, err := LoadTopology(include)
			if err != nil {
				return nil, fmt.Errorf("loading included topology %s: %w", include, err)
			}

			childSpec, err := decodeTopologyRecursive(*childConfig, newVisited)
			if err != nil {
				return nil, fmt.Errorf("decoding included topology %s: %w", include, err)
			}

			if childV1Spec, ok := childSpec.(*v1.TopologySpec); ok {
				existingHosts := make(map[string]bool)
				for _, n := range v1Spec.NodesF {
					existingHosts[n.GeneralF.HostnameF] = true
				}

				for _, n := range childV1Spec.NodesF {
					// check for duplicate hostnames
					if existingHosts[n.GeneralF.HostnameF] {
						return nil, fmt.Errorf(
							"duplicate node hostname found in included topology %s: %s",
							include,
							n.GeneralF.HostnameF,
						)
					}

					v1Spec.NodesF = append(v1Spec.NodesF, n)
					existingHosts[n.GeneralF.HostnameF] = true
				}
			} else {
				return nil, fmt.Errorf("included topology %s is not v1 compatible", include)
			}
		}
	}

	return spec, nil
}

// LoadTopology returns the topology config named source: the stored one, else
// the YAML file at that path. Including a topology loads it this way.
func LoadTopology(source string) (*store.Config, error) {
	// Try to load from the store first
	if c, err := store.NewConfig("Topology/" + source); err == nil {
		err := store.Get(c)
		if err == nil {
			return c, nil
		}
	}

	// Fallback to loading from a file (absolute or relative)
	if _, err := os.Stat(source); err == nil {
		b, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}

		return store.NewConfigFromYAML(b)
	}

	return nil, fmt.Errorf("could not find topology %s", source)
}
