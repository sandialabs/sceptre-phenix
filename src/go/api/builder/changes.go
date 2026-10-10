package builder

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/mitchellh/mapstructure"

	"phenix/store"
)

// ChangeAction is what a publication does to a config it names. It creates
// the config, updates it, or leaves it as it is because it already holds the
// publication.
type ChangeAction string

const (
	// ChangeCreate creates a config of a name no config of its kind has.
	ChangeCreate ChangeAction = "create"
	// ChangeUpdate replaces an existing config.
	ChangeUpdate ChangeAction = "update"
	// ChangeUnchanged writes nothing: the config already holds the
	// publication.
	ChangeUnchanged ChangeAction = "unchanged"
)

// ItemChange is what a publication does to one item of a list a config
// holds: an included topology, a disk image or a VLAN alias.
type ItemChange string

const (
	// ItemAdded is an item the config gains.
	ItemAdded ItemChange = "added"
	// ItemRemoved is an item the config loses.
	ItemRemoved ItemChange = "removed"
	// ItemKept is an item the config holds before and after, as it is.
	ItemKept ItemChange = "kept"
	// ItemChanged is an item the config holds before and after, with
	// another value: a VLAN alias only.
	ItemChanged ItemChange = "changed"
)

// ScenarioChange is what a publication does to a Scenario config the
// document lists. It adds the topology to the "topology" annotation, or
// leaves the config as it is because the annotation names the topology
// already.
type ScenarioChange string

const (
	// ScenarioAnnotate adds the topology to the scenario's "topology"
	// annotation.
	ScenarioAnnotate ScenarioChange = "annotate"
	// ScenarioUnchanged leaves a scenario whose annotation names the topology
	// already.
	ScenarioUnchanged ScenarioChange = "unchanged"
)

// ConfigChange is what a publication does to a Topology or Experiment
// config.
type ConfigChange struct {
	Name   string       `json:"name"`
	Action ChangeAction `json:"action"`
}

// IncludeChange is what a publication does to one topology the Topology
// config includes (its spec.includeTopologies).
type IncludeChange struct {
	Name   string     `json:"name"`
	Change ItemChange `json:"change"`
}

// ScenarioAnnotation is what a publication does to one Scenario config the
// document lists.
type ScenarioAnnotation struct {
	Name   string         `json:"name"`
	Change ScenarioChange `json:"change"`
}

// ImageChange is what a publication does to one disk image the Topology
// config's devices name in hardware.drives[].image.
type ImageChange struct {
	// Name is the image as the drives name it.
	Name   string     `json:"name"`
	Change ItemChange `json:"change"`
	// Devices are the hostnames of the devices that use the image once the
	// topology is published, or, for a removed image, those that used it.
	Devices []string `json:"devices"`
	// OnServer reports whether the server has a disk image of that file
	// name, and is nil when the server's images could not be read.
	OnServer *bool `json:"onServer"`
}

// VLANAliasChange is what a publication does to the VLAN alias of one
// network of the Experiment config (its spec.vlans.aliases).
type VLANAliasChange struct {
	// Name is the network's name.
	Name string `json:"name"`
	// From is the experiment's alias before, nil for none.
	From *int `json:"from"`
	// To is the alias the publication writes, nil for none.
	To     *int       `json:"to"`
	Change ItemChange `json:"change"`
}

// PublishChanges is what publishing a document to a target changes,
// compared with what is stored now (see [DescribePublishChanges]). Every
// list is sorted by name.
type PublishChanges struct {
	Topology  ConfigChange         `json:"topology"`
	Includes  []IncludeChange      `json:"includes"`
	Scenarios []ScenarioAnnotation `json:"scenarios"`
	Images    []ImageChange        `json:"images"`
	// Experiment is set for a publication that writes an experiment.
	Experiment *ConfigChange `json:"experiment,omitempty"`
	// VLANAliases is set for a publication that writes an experiment, and
	// left out when neither the experiment nor the document has an alias.
	VLANAliases []VLANAliasChange `json:"vlanAliases,omitempty"`
}

// ExperimentState is the experiment a publication writes, as it is stored
// now.
type ExperimentState struct {
	Name string
	// Stored is the stored experiment of that name, or nil when there is
	// none, which the publication creates.
	Stored *store.Config
	// Held reports that Stored already holds the publication, so publishing
	// writes nothing to it.
	Held bool
}

// PublishState is what [DescribePublishChanges] compares: what a
// publication writes, and what is stored now.
type PublishState struct {
	// TopologyName is the name of the Topology config the publication
	// writes.
	TopologyName string
	// Spec is the topology spec the publication writes (see
	// [builder.Topology.Spec]).
	Spec map[string]any
	// VLANAliases are the VLAN aliases an experiment of the publication gets
	// (see [builder.Topology.VLANAliases]).
	VLANAliases map[string]int
	// StoredTopology is the stored topology of that name, or nil when there
	// is none, which the publication creates.
	StoredTopology *store.Config
	// TopologyHeld reports that StoredTopology already holds the
	// publication, so publishing writes nothing to it.
	TopologyHeld bool
	// Scenarios are the stored Scenario configs the document lists. The
	// publication adds the topology to the annotation of each one, unless the
	// annotation names it already. A publication that changes no scenario
	// names none.
	Scenarios []*store.Config
	// Experiment is the experiment the publication writes, nil for a
	// topology-only publication.
	Experiment *ExperimentState
	// ServerImages are the file names of the disk images the server has, or
	// nil when they could not be read.
	ServerImages []string
}

// DescribePublishChanges says what publishing changes, compared with what is
// stored now. It describes:
//   - the Topology config and the topologies it includes
//   - the Scenario configs whose "topology" annotation gains the topology
//   - the disk images the devices of the stored topology use, compared with
//     those the devices of the publication use
//   - for a publication that writes an experiment, the Experiment config and
//     its VLAN aliases (each one added when the experiment is created)
//
// It reads and writes nothing. An error means that it could not decode a
// stored config.
func DescribePublishChanges(state PublishState) (*PublishChanges, error) {
	published, err := decodeSpecParts(state.Spec)
	if err != nil {
		return nil, fmt.Errorf("decoding the published topology: %w", err)
	}

	var stored specParts

	if state.StoredTopology != nil {
		if stored, err = decodeSpecParts(state.StoredTopology.Spec); err != nil {
			return nil, fmt.Errorf("decoding topology %s: %w", state.TopologyName, err)
		}
	}

	changes := &PublishChanges{
		Topology: ConfigChange{
			Name: state.TopologyName, Action: configAction(state.StoredTopology, state.TopologyHeld),
		},
		Includes:    includeChanges(stored.includes, published.includes),
		Scenarios:   scenarioAnnotations(state.Scenarios, state.TopologyName),
		Images:      imageChanges(stored.images, published.images, state.ServerImages),
		Experiment:  nil,
		VLANAliases: nil,
	}

	if state.Experiment == nil {
		return changes, nil
	}

	changes.Experiment = &ConfigChange{
		Name:   state.Experiment.Name,
		Action: configAction(state.Experiment.Stored, state.Experiment.Held),
	}

	current, err := experimentAliases(state.Experiment.Stored)
	if err != nil {
		return nil, fmt.Errorf("decoding experiment %s: %w", state.Experiment.Name, err)
	}

	changes.VLANAliases = aliasChanges(current, state.VLANAliases)

	return changes, nil
}

// NamesDiskImage reports whether the devices of a topology spec name a disk
// image (hardware.drives[].image), as [DescribePublishChanges] reads them.
// A spec it cannot decode names none.
func NamesDiskImage(spec map[string]any) bool {
	parts, err := decodeSpecParts(spec)

	return err == nil && len(parts.images) > 0
}

// HasTopologyAnnotation reports whether value, a scenario's comma-separated
// "topology" annotation, names topology: one of its names, trimmed, is
// exactly topology. A name that only contains it does not count.
func HasTopologyAnnotation(value, topology string) bool {
	for name := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(name) == topology {
			return true
		}
	}

	return false
}

// configAction is what a publication does to a config: creates one that is
// not stored, leaves one that holds the publication, and updates any other.
func configAction(stored *store.Config, held bool) ChangeAction {
	switch {
	case stored == nil:
		return ChangeCreate
	case held:
		return ChangeUnchanged
	}

	return ChangeUpdate
}

// specParts is what of a topology spec [DescribePublishChanges] compares:
// the topologies it includes, and the hostnames of the devices that use each
// disk image.
type specParts struct {
	includes []string
	images   map[string][]string
}

// topologyParts is the part of a topology spec that names included
// topologies and disk images, in the spec's own keys.
type topologyParts struct {
	IncludeTopologies []string `mapstructure:"includeTopologies"`
	Nodes             []struct {
		General struct {
			Hostname string `mapstructure:"hostname"`
		} `mapstructure:"general"`
		Hardware struct {
			Drives []struct {
				Image string `mapstructure:"image"`
			} `mapstructure:"drives"`
		} `mapstructure:"hardware"`
	} `mapstructure:"nodes"`
}

// decodeSpecParts reads the included topologies and the disk images of a
// topology spec. A drive without an image names none.
func decodeSpecParts(spec map[string]any) (specParts, error) {
	var parts topologyParts

	if err := mapstructure.WeakDecode(spec, &parts); err != nil {
		return specParts{includes: nil, images: nil}, err
	}

	images := make(map[string][]string)

	for _, node := range parts.Nodes {
		for _, drive := range node.Hardware.Drives {
			if drive.Image == "" {
				continue
			}

			if !slices.Contains(images[drive.Image], node.General.Hostname) {
				images[drive.Image] = append(images[drive.Image], node.General.Hostname)
			}
		}
	}

	for image := range images {
		slices.Sort(images[image])
	}

	return specParts{includes: parts.IncludeTopologies, images: images}, nil
}

// itemChange is what becomes of an item held before (was) or after (is).
func itemChange(was, is bool) ItemChange {
	switch {
	case was && is:
		return ItemKept
	case is:
		return ItemAdded
	}

	return ItemRemoved
}

// includeChanges compares the topologies a stored topology includes with
// those the publication's includes.
func includeChanges(stored, published []string) []IncludeChange {
	names := slices.Concat(stored, published)
	slices.Sort(names)
	names = slices.Compact(names)

	changes := make([]IncludeChange, 0, len(names))

	for _, name := range names {
		changes = append(changes, IncludeChange{
			Name: name, Change: itemChange(slices.Contains(stored, name), slices.Contains(published, name)),
		})
	}

	return changes
}

// scenarioAnnotations says, for each listed scenario, whether its "topology"
// annotation gains the topology.
func scenarioAnnotations(scenarios []*store.Config, topology string) []ScenarioAnnotation {
	annotations := make([]ScenarioAnnotation, 0, len(scenarios))

	for _, scenario := range scenarios {
		change := ScenarioAnnotate
		if HasTopologyAnnotation(scenario.Metadata.Annotations["topology"], topology) {
			change = ScenarioUnchanged
		}

		annotations = append(annotations, ScenarioAnnotation{Name: scenario.Metadata.Name, Change: change})
	}

	slices.SortFunc(annotations, func(a, b ScenarioAnnotation) int { return cmp.Compare(a.Name, b.Name) })

	return annotations
}

// imageChanges compares the disk images a stored topology's devices use with
// those the publication's devices use. An image is on the server when the
// server has an image of its file name, as the Builder's drive image check
// finds it (validate.js).
func imageChanges(stored, published map[string][]string, server []string) []ImageChange {
	names := slices.Collect(maps.Keys(published))

	for name := range stored {
		if _, ok := published[name]; !ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	changes := make([]ImageChange, 0, len(names))

	for _, name := range names {
		was, used := stored[name]
		devices, uses := published[name]

		if !uses {
			devices = was
		}

		var onServer *bool

		if server != nil {
			has := slices.Contains(server, path.Base(name))
			onServer = &has
		}

		changes = append(changes, ImageChange{
			Name: name, Change: itemChange(used, uses), Devices: append([]string{}, devices...), OnServer: onServer,
		})
	}

	return changes
}

// experimentAliases returns the VLAN aliases of a stored experiment
// (spec.vlans.aliases), or none for no experiment.
func experimentAliases(experiment *store.Config) (map[string]int, error) {
	if experiment == nil {
		return map[string]int{}, nil
	}

	var spec struct {
		VLANs struct {
			Aliases map[string]int `mapstructure:"aliases"`
		} `mapstructure:"vlans"`
	}

	if err := mapstructure.WeakDecode(experiment.Spec, &spec); err != nil {
		return nil, err
	}

	return spec.VLANs.Aliases, nil
}

// aliasChanges compares an experiment's VLAN aliases with those the
// publication writes.
func aliasChanges(current, published map[string]int) []VLANAliasChange {
	names := slices.Collect(maps.Keys(published))

	for name := range current {
		if _, ok := published[name]; !ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	changes := make([]VLANAliasChange, 0, len(names))

	for _, name := range names {
		from, was := current[name]
		to, is := published[name]

		change := VLANAliasChange{Name: name, From: nil, To: nil, Change: itemChange(was, is)}

		if was {
			change.From = &from
		}

		if is {
			change.To = &to
		}

		if was && is && from != to {
			change.Change = ItemChanged
		}

		changes = append(changes, change)
	}

	return changes
}
