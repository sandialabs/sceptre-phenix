package experiment

import (
	"encoding/json"
	"errors"
	"fmt"

	"phenix/store"
	ifaces "phenix/types/interfaces"
)

// VMDefaultsAnnotation is the experiment annotation that holds, as a JSON
// object keyed by VM name, the [VMDefaults] of each VM when phenix created
// the experiment. phenix sets it, so a user cannot set or change it.
const VMDefaultsAnnotation = "phenix/vm-defaults"

// ErrNoVMDefaults is the error for a VM that its experiment saved no settings
// for, such as a VM of an experiment created before phenix saved them.
var ErrNoVMDefaults = errors.New("no saved settings for this VM")

// VMDefaults is the settings of one VM that phenix saves when it creates the
// experiment. InjectPartition is nil when the first drive set none, and Host
// is empty when the schedule placed the VM on no host.
type VMDefaults struct {
	CPUs            int    `json:"cpus"`
	RAM             int    `json:"ram"`
	Disk            string `json:"disk"`
	InjectPartition *int   `json:"inject_partition,omitempty"`
	Snapshot        bool   `json:"snapshot"`
	DNB             bool   `json:"dnb"`
	Host            string `json:"host"`
}

// vmDefaultsOf returns the settings of each VM of spec, by name. External
// nodes are not VMs that phenix deploys, so it leaves them out.
func vmDefaultsOf(spec ifaces.ExperimentSpec) map[string]VMDefaults {
	defaults := make(map[string]VMDefaults)

	topo := spec.Topology()
	if topo == nil {
		return defaults
	}

	schedules := spec.Schedules()

	for _, node := range topo.Nodes() {
		if node.External() {
			continue
		}

		name := node.General().Hostname()
		saved := VMDefaults{ //nolint:exhaustruct // the drive fields are set below
			CPUs: node.Hardware().VCPU(),
			RAM:  node.Hardware().Memory(),
			Host: schedules[name],
		}

		if drives := node.Hardware().Drives(); len(drives) > 0 {
			saved.Disk = drives[0].Image()

			if p := drives[0].InjectPartition(); p != nil {
				partition := *p
				saved.InjectPartition = &partition
			}
		}

		if dnb := node.General().DoNotBoot(); dnb != nil {
			saved.DNB = *dnb
		}

		if snapshot := node.General().Snapshot(); snapshot != nil {
			saved.Snapshot = *snapshot
		}

		defaults[name] = saved
	}

	return defaults
}

// recordVMDefaults sets [VMDefaultsAnnotation] on c to the settings of each
// VM of spec, replacing any value that c already has.
func recordVMDefaults(c *store.Config, spec ifaces.ExperimentSpec) {
	// a map of VMDefaults, which has only numbers, strings and bools, always encodes
	encoded, _ := json.Marshal(vmDefaultsOf(spec))

	if c.Metadata.Annotations == nil {
		c.Metadata.Annotations = store.Annotations{}
	}

	c.Metadata.Annotations[VMDefaultsAnnotation] = string(encoded)
}

// keepStoredVMDefaults gives c, a config about to replace c.Stored, the
// [VMDefaultsAnnotation] of c.Stored. It removes the annotation from c when
// c.Stored has none or is nil, so that an update cannot add or change the
// saved settings.
func keepStoredVMDefaults(c *store.Config) {
	var stored store.Annotations
	if c.Stored != nil {
		stored = c.Stored.Metadata.Annotations
	}

	value, ok := stored[VMDefaultsAnnotation]
	if !ok {
		delete(c.Metadata.Annotations, VMDefaultsAnnotation)

		return
	}

	if c.Metadata.Annotations == nil {
		c.Metadata.Annotations = store.Annotations{}
	}

	c.Metadata.Annotations[VMDefaultsAnnotation] = value
}

// VMDefaultsOf returns the saved settings of each VM, by name, from the
// annotations of an experiment. It returns nil and no error when the
// experiment saved none.
func VMDefaultsOf(annotations map[string]string) (map[string]VMDefaults, error) {
	value, ok := annotations[VMDefaultsAnnotation]
	if !ok {
		return nil, nil //nolint:nilnil // no saved settings is not an error
	}

	var defaults map[string]VMDefaults

	if err := json.Unmarshal([]byte(value), &defaults); err != nil {
		return nil, fmt.Errorf("decoding experiment annotation %s: %w", VMDefaultsAnnotation, err)
	}

	return defaults, nil
}
