package workflow

import (
	"cmp"
	"fmt"

	"github.com/mitchellh/mapstructure"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// ReadIncludes reads with read, once each, the topology files that the topology
// of spec, else of the annotation of exp, includes by a path at any depth, for
// any plan, as decoding looks: pending, then stored, then a file. A missing or
// undecodable topology or include ends only its own branch. An error of read
// stops the walk and wraps [ErrUnresolved]. pending is as for [Prepare].
func ReadIncludes(spec Spec, exp *types.Experiment, read types.IncludeReader, pending ...string) error {
	name := cmp.Or(spec.ExperimentTopology(), experimentAnnotation(exp, topologyKey))
	if name == "" {
		return nil
	}

	skip, err := pendingConfigs(pending)
	if err != nil {
		return err
	}

	walk := includeReads{read: read, pending: skip, seen: map[string]bool{name: true}}

	return walk.follow(walk.includesOf(name))
}

// includeReads is the state of one [ReadIncludes] call.
type includeReads struct {
	read    types.IncludeReader
	pending map[string]pendingConfig
	seen    map[string]bool
}

// follow reads the files among includes, the includes of one topology, and
// follows the includes of each include.
func (w *includeReads) follow(includes []string) error {
	for _, include := range includes {
		if w.seen[include] {
			continue
		}

		w.seen[include] = true

		if topo, ok := w.pending[store.ConfigFullName(topologyKey, include)]; ok {
			if err := w.follow(topo.includes); err != nil {
				return err
			}

			continue
		}

		var readErr error

		cfg, err := types.LoadTopology(include, func(path string, text []byte) ([]byte, error) {
			filled, err := w.read(path, text)
			readErr = err

			return filled, err
		})
		if readErr != nil {
			return fmt.Errorf("%w: loading included topology %s: %w", ErrUnresolved, include, readErr)
		}

		if err != nil {
			continue
		}

		if err := w.follow(topologyIncludes(*cfg)); err != nil {
			return err
		}
	}

	return nil
}

// includesOf returns the includes of the topology named name, from its ref
// when it is pending, else from the store.
func (w *includeReads) includesOf(name string) []string {
	if topo, ok := w.pending[store.ConfigFullName(topologyKey, name)]; ok {
		return topo.includes
	}

	cfg, err := readConfig(topologyKey, name)
	if err != nil {
		return nil
	}

	return topologyIncludes(*cfg)
}

// topologyIncludes returns the includeTopologies of cfg, or none when it does
// not decode.
func topologyIncludes(cfg store.Config) []string {
	var spec v1.TopologySpec
	if err := mapstructure.WeakDecode(cfg.Spec, &spec); err != nil {
		return nil
	}

	return spec.IncludedTopologies()
}
