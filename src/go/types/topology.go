package types

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/mitchellh/mapstructure"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	v1 "phenix/types/version/v1"
	"phenix/util/envsubst"
)

func DecodeTopologyFromConfig(c store.Config) (ifaces.TopologySpec, error) {
	return DecodeTopologyFromConfigWith(c, os.LookupEnv)
}

// DecodeTopologyFromConfigWith is [DecodeTopologyFromConfig] with lookup for
// the variables of an included topology file.
func DecodeTopologyFromConfigWith(c store.Config, lookup envsubst.Lookup) (ifaces.TopologySpec, error) {
	return decodeTopologyRecursive(c, map[string]bool{}, lookup)
}

func decodeTopologyRecursive(
	c store.Config,
	visited map[string]bool,
	lookup envsubst.Lookup,
) (ifaces.TopologySpec, error) {
	if visited[c.Metadata.Name] {
		return nil, fmt.Errorf("cyclic import detected: %s", c.Metadata.Name)
	}

	newVisited := make(map[string]bool)
	maps.Copy(newVisited, visited)

	newVisited[c.Metadata.Name] = true

	spec, err := decodeTopologySpec(c)
	if err != nil {
		return nil, err
	}

	if v1Spec, ok := spec.(*v1.TopologySpec); ok {
		for _, include := range v1Spec.IncludeTopologiesF {
			childConfig, err := LoadTopology(include, lookup)
			if err != nil {
				return nil, fmt.Errorf("loading included topology %s: %w", include, err)
			}

			childSpec, err := decodeTopologyRecursive(*childConfig, newVisited, lookup)
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

// decodeTopologySpec decodes the spec of c, a topology at its stored version.
func decodeTopologySpec(c store.Config) (ifaces.TopologySpec, error) {
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

	return spec, nil
}

// LoadTopology returns the topology config named source: the stored one, else
// the YAML file at that path, with each ${NAME} placeholder replaced by the
// value from lookup. Including a topology loads it this way.
func LoadTopology(source string, lookup envsubst.Lookup) (*store.Config, error) {
	stored, text, err := findTopology(source)
	if err != nil || stored != nil {
		return stored, err
	}

	return store.DecodeConfigYAML(envsubst.Expand(text, lookup))
}

// findTopology returns the stored topology config named source. When the
// store does not have it, it returns the text of the file at that path.
func findTopology(source string) (*store.Config, []byte, error) {
	// Try to load from the store first
	if c, err := store.NewConfig("Topology/" + source); err == nil {
		err := store.Get(c)
		if err == nil {
			return c, nil, nil
		}
	}

	// Fallback to loading from a file (absolute or relative)
	if _, err := os.Stat(source); err == nil {
		b, err := os.ReadFile(source)
		if err != nil {
			return nil, nil, err
		}

		return nil, b, nil
	}

	return nil, nil, fmt.Errorf("could not find topology %s", source)
}

// IncludedTopologyFiles returns each topology file that c includes by a path,
// directly or through other includes, once and as read. path is the file of
// c, or "" for a stored c. lookup fills in a file only to read its includes.
// An include that is stored is used as stored. An include that cannot be read
// is skipped: the decode of the topology reports it.
func IncludedTopologyFiles(c store.Config, path string, lookup envsubst.Lookup) []envsubst.File {
	root := includeKey(&c, "")
	if path != "" {
		root = includeKey(nil, path)
	}

	walk := includeWalk{lookup: lookup, seen: map[string]bool{root: true}, files: nil}
	walk.follow(c)

	return walk.files
}

// includeWalk is the state of one [IncludedTopologyFiles] call.
type includeWalk struct {
	lookup envsubst.Lookup
	seen   map[string]bool
	files  []envsubst.File
}

func (w *includeWalk) follow(c store.Config) {
	spec, err := decodeTopologySpec(c)
	if err != nil {
		return
	}

	v1Spec, ok := spec.(*v1.TopologySpec)
	if !ok {
		return
	}

	for _, include := range v1Spec.IncludeTopologiesF {
		stored, text, err := findTopology(include)

		key := includeKey(stored, include)
		if err != nil || w.seen[key] {
			continue
		}

		w.seen[key] = true

		if stored != nil {
			w.follow(*stored)

			continue
		}

		w.files = append(w.files, envsubst.File{Path: include, Text: text})

		if child, err := store.DecodeConfigYAML(envsubst.Expand(text, w.lookup)); err == nil {
			w.follow(*child)
		}
	}
}

// includeKey names a topology once in a walk: the name of a stored config, or
// the absolute path of a file.
func includeKey(stored *store.Config, path string) string {
	if stored != nil {
		return "stored:" + stored.Metadata.Name
	}

	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	return "file:" + path
}
