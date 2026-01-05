package types

import (
	"fmt"
	"os"
	"path/filepath"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v0 "phenix/types/version/v0"
	v1 "phenix/types/version/v1"

	"github.com/mitchellh/mapstructure"
)

func DecodeTopologyFromConfig(c store.Config) (ifaces.TopologySpec, error) {
	return decodeTopologyRecursive(c, map[string]bool{})
}

func decodeTopologyRecursive(c store.Config, visited map[string]bool) (ifaces.TopologySpec, error) {
	if visited[c.Metadata.Name] {
		return nil, fmt.Errorf("cyclic import detected: %s", c.Metadata.Name)
	}

	newVisited := make(map[string]bool)
	for k, v := range visited {
		newVisited[k] = v
	}
	newVisited[c.Metadata.Name] = true

	var (
		iface         interface{}
		latestVersion = version.StoredVersion[c.Kind]
	)

	if c.APIVersion() != latestVersion {
		version := c.Kind + "/" + latestVersion
		upgrader := GetUpgrader(version)

		if upgrader == nil {
			return nil, fmt.Errorf("no upgrader found for topology version %s", latestVersion)
		}

		var err error

		iface, err = upgrader.Upgrade(c.APIVersion(), c.Spec, c.Metadata)
		if err != nil {
			return nil, fmt.Errorf("upgrading topology to %s: %w", latestVersion, err)
		}
	} else {
		var err error

		iface, err = version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
		if err != nil {
			return nil, fmt.Errorf("getting versioned spec for config: %w", err)
		}

		if err := mapstructure.WeakDecode(c.Spec, &iface); err != nil {
			return nil, fmt.Errorf("decoding versioned spec: %w", err)
		}
	}

	spec, ok := iface.(ifaces.TopologySpec)
	if !ok {
		return nil, fmt.Errorf("invalid spec in config")
	}

	if v1Spec, ok := spec.(*v1.TopologySpec); ok {
		for _, include := range v1Spec.IncludeTopologiesF {
			childConfig, err := loadTopology(include)
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
						return nil, fmt.Errorf("duplicate node hostname found in included topology %s: %s", include, n.GeneralF.HostnameF)
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

func loadTopology(source string) (*store.Config, error) {
	// Try to load from the store first
	if c, err := store.NewConfig("Topology/" + source); err == nil {
		if err := store.Get(c); err == nil {
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

type topology struct{}

func (topology) Upgrade(version string, spec map[string]interface{}, md store.ConfigMetadata) (interface{}, error) {
	// This is a dummy topology upgrader to provide an exmaple of how an upgrader
	// might be coded up. The specs in v0 simply assume that some integer values
	// might be represented as strings when in JSON format.

	if version == "v0" {
		var (
			topoV0 *v0.TopologySpec
			topoV1 *v1.TopologySpec
		)

		// Using WeakDecode here since v0 schema uses strings for some integer
		// values.
		if err := mapstructure.WeakDecode(spec, &topoV0); err != nil {
			return nil, fmt.Errorf("decoding topology into v0 spec: %w", err)
		}

		// Using WeakDecode here since v0 schema uses strings for some integer
		// values.
		if err := mapstructure.WeakDecode(spec, &topoV1); err != nil {
			return nil, fmt.Errorf("decoding topology into v1 spec: %w", err)
		}

		// Previous versions of phenix assumed topologies were stored at
		// /phenix/topologies/<name>, and typically configured injections to use an
		// injections subdirectory. Given this, if an injection source path isn't
		// absolute then assume injections are based in the old topologies
		// directory.
		for _, n := range topoV1.NodesF {
			for _, i := range n.InjectionsF {
				if !filepath.IsAbs(i.SrcF) {
					i.SrcF = fmt.Sprintf("/phenix/topologies/%s/%s", md.Name, i.SrcF)
				}
			}
		}

		return topoV1, nil
	}

	return nil, fmt.Errorf("unknown version %s to upgrade from", version)
}

func init() {
	RegisterUpgrader("Topology/v1", new(topology))
}
