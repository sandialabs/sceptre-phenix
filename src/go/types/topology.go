package types

import (
	"fmt"
	"path/filepath"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v0 "phenix/types/version/v0"
	v1 "phenix/types/version/v1"

	"github.com/mitchellh/mapstructure"
)

func DecodeTopologyFromConfig(c store.Config) (ifaces.TopologySpec, error) {
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

	// Process includeTopologies if this is a v1 topology
	if v1Spec, ok := iface.(*v1.TopologySpec); ok {
		if err := processIncludedTopologies(v1Spec); err != nil {
			return nil, fmt.Errorf("processing included topologies: %w", err)
		}
	}

	return spec, nil
}

func processIncludedTopologies(spec *v1.TopologySpec) error {
	if len(spec.IncludeTopologiesF) == 0 {
		return nil
	}

	var allErrors error

	for _, path := range spec.IncludeTopologiesF {
		// Check if it's an absolute path to a file
		if filepath.IsAbs(path) {
			if err := loadTopologyFromFile(spec, path); err != nil {
				allErrors = fmt.Errorf("%w; loading topology from %s: %v", allErrors, path, err)
			}
			continue
		}

		// Otherwise, try to load from store by name
		if err := loadTopologyFromStore(spec, path); err != nil {
			allErrors = fmt.Errorf("%w; loading topology %s from store: %v", allErrors, path, err)
		}
	}

	return allErrors
}

func loadTopologyFromFile(spec *v1.TopologySpec, path string) error {
	c, err := store.NewConfigFromFile(path)
	if err != nil {
		return fmt.Errorf("loading config from file: %w", err)
	}

	var includedSpec struct {
		Nodes []*v1.Node `mapstructure:"nodes"`
	}

	if err := mapstructure.Decode(c.Spec, &includedSpec); err != nil {
		return fmt.Errorf("decoding topology spec: %w", err)
	}

	// Append nodes from included topology to this topology
	spec.NodesF = append(spec.NodesF, includedSpec.Nodes...)

	return nil
}

func loadTopologyFromStore(spec *v1.TopologySpec, name string) error {
	c := &store.Config{
		Kind: "Topology",
		Metadata: store.ConfigMetadata{
			Name: name,
		},
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting topology from store: %w", err)
	}

	var includedSpec struct {
		Nodes []*v1.Node `mapstructure:"nodes"`
	}

	if err := mapstructure.Decode(c.Spec, &includedSpec); err != nil {
		return fmt.Errorf("decoding topology spec: %w", err)
	}

	// Append nodes from included topology to this topology
	spec.NodesF = append(spec.NodesF, includedSpec.Nodes...)

	return nil
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
