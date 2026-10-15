package util

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
)

// ExperimentToProtobuf converts the experiment with the given VMs; its
// vm_count and delayed_vms count those VMs.
func ExperimentToProtobuf(
	exp types.Experiment,
	status cache.Status,
	vms []mm.VM,
) *proto.Experiment {
	var (
		pb     = experimentToProtobuf(exp, status)
		nodes  = IndexTopology(exp.Spec.Topology())
		counts vmCounts
	)

	pb.Vms = make([]*proto.VM, len(vms))

	for i, v := range vms {
		vm := VMToProtobufIndexed(pb.GetName(), v, nodes)

		pb.Vms[i] = vm
		counts.add(pb.GetRunning(), vm.GetDoNotBoot(), vm.GetExternal(), vm.GetDelayedStart())
	}

	pb.VmCount, pb.DelayedVms = counts.total, counts.delayed

	return pb
}

// ExperimentSummaryToProtobuf converts the experiment without its VMs. Its
// counts are those ExperimentToProtobuf gives for vm.ListConfigured(exp), read
// straight from the topology instead of from a VM built for every node.
func ExperimentSummaryToProtobuf(exp types.Experiment, status cache.Status) *proto.Experiment {
	pb := experimentToProtobuf(exp, status)

	topology := exp.Spec.Topology()
	if topology == nil {
		return pb
	}

	var (
		nodes  = IndexTopology(topology)
		counts vmCounts
	)

	for _, node := range topology.Nodes() {
		// A VM takes its delay and external flag from the first node with its
		// name, as VMToProtobufIndexed reads them, and do-not-boot from its own.
		first := nodes[node.General().Hostname()]
		dnb := node.General().DoNotBoot() != nil && *node.General().DoNotBoot()

		counts.add(pb.GetRunning(), dnb, first.External(), first.Delayed())
	}

	pb.VmCount, pb.DelayedVms = counts.total, counts.delayed

	return pb
}

// vmCounts is an experiment's vm_count and delayed_vms.
type vmCounts struct {
	total, delayed uint32
}

// add counts one VM: a stopped experiment counts every VM, a running one only
// those that boot and are not external. Delayed VMs count either way.
func (c *vmCounts) add(running, doNotBoot, external bool, delay string) {
	if !running || (!doNotBoot && !external) {
		c.total++
	}

	if delay != "" {
		c.delayed++
	}
}

// experimentToProtobuf converts all of the experiment but its VMs.
func experimentToProtobuf(exp types.Experiment, status cache.Status) *proto.Experiment {
	pb := &proto.Experiment{ //nolint:exhaustruct // partial initialization
		Name:      exp.Spec.ExperimentName(),
		Topology:  exp.Metadata.Annotations["topology"],
		Scenario:  exp.Metadata.Annotations["scenario"],
		StartTime: exp.Status.StartTime(),
		Running:   exp.Running(),
		Status:    string(status),
	}

	apps := make([]string, 0, len(exp.Apps()))

	for _, app := range exp.Apps() {
		apps = append(apps, app.Name())
	}

	pb.Apps = apps

	var aliases map[string]int

	if exp.Running() {
		aliases = exp.Status.VLANs()

		var (
			minVal = 0
			maxVal = 0
		)

		for _, k := range exp.Status.VLANs() {
			if minVal == 0 || k < minVal {
				minVal = k
			}

			if maxVal == 0 || k > maxVal {
				maxVal = k
			}
		}

		pb.VlanMin = uint32(minVal)
		pb.VlanMax = uint32(maxVal)
	} else {
		aliases = exp.Spec.VLANs().Aliases()

		pb.VlanMin = uint32(exp.Spec.VLANs().Min()) //nolint:gosec // integer overflow conversion int -> uint32
		pb.VlanMax = uint32(exp.Spec.VLANs().Max()) //nolint:gosec // integer overflow conversion int -> uint32
	}

	if aliases != nil {
		vlans := make([]*proto.VLAN, 0, len(aliases))

		for alias := range aliases {
			vlan := &proto.VLAN{
				Vlan:  uint32(aliases[alias]), //nolint:gosec // integer overflow conversion int -> uint32
				Alias: alias,
			}

			vlans = append(vlans, vlan)
		}

		pb.Vlans = vlans
		pb.VlanCount = uint32(len(aliases)) //nolint:gosec // integer overflow conversion int -> uint32
	}

	return pb
}

// NodeIndex maps topology hostnames to their nodes so converting a list of VMs
// does one map lookup per VM instead of a linear topology scan.
type NodeIndex map[string]ifaces.NodeSpec

// IndexTopology builds a NodeIndex for the topology. Like FindNodeByName, the
// first node with a given hostname wins. A nil topology yields a nil index.
func IndexTopology(topology ifaces.TopologySpec) NodeIndex {
	if topology == nil {
		return nil
	}

	nodes := topology.Nodes()
	index := make(NodeIndex, len(nodes))

	for _, node := range nodes {
		name := node.General().Hostname()
		if _, ok := index[name]; !ok {
			index[name] = node
		}
	}

	return index
}

// VMToProtobuf converts one VM, looking its node up in the topology. Use
// VMToProtobufIndexed with IndexTopology when converting a list of VMs.
func VMToProtobuf(exp string, vm mm.VM, topology ifaces.TopologySpec) *proto.VM {
	return VMToProtobufIndexed(exp, vm, IndexTopology(topology))
}

// VMToProtobufIndexed converts one VM using a prebuilt topology node index.
func VMToProtobufIndexed(exp string, vm mm.VM, nodes NodeIndex) *proto.VM {
	v := vmToProtobuf(exp, vm)

	if node, ok := nodes[vm.Name]; ok {
		v.DelayedStart = node.Delayed()
		v.External = node.External()
	}

	return v
}

// AnnotationsToProtobuf converts a node's annotations for a single VM's
// response. No annotations convert to an empty object, so a response that
// carries them never reads as one that does not (VM lists send null).
func AnnotationsToProtobuf(annotations map[string]any) (*structpb.Struct, error) {
	out := new(structpb.Struct)

	if len(annotations) == 0 {
		return out, nil
	}

	// through JSON, which takes the nested lists and maps a config decodes to
	body, err := json.Marshal(annotations)
	if err != nil {
		return nil, fmt.Errorf("encoding annotations: %w", err)
	}

	if err := protojson.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("converting annotations: %w", err)
	}

	return out, nil
}

func vmToProtobuf(exp string, vm mm.VM) *proto.VM {
	return &proto.VM{ //nolint:exhaustruct // partial initialization
		Name:            vm.Name,
		Description:     vm.Description,
		Host:            vm.Host,
		Ipv4:            vm.IPv4,
		Cpus:            uint32(vm.CPUs), //nolint:gosec // integer overflow conversion int -> uint32
		Ram:             uint32(vm.RAM),  //nolint:gosec // integer overflow conversion int -> uint32
		Disk:            vm.Disk,
		InjectPartition: uint32(vm.InjectPartition), //nolint:gosec // integer overflow conversion int -> uint32
		Uptime:          vm.Uptime,
		Networks:        vm.Networks,
		Taps:            vm.Taps,
		Captures:        CapturesToProtobuf(vm.Captures),
		DoNotBoot:       vm.DoNotBoot,
		Screenshot:      vm.Screenshot,
		Running:         vm.Running,
		Busy:            vm.Busy,
		Experiment:      exp,
		State:           vm.State,
		CdRom:           vm.CdRom,
		Tags:            vm.Tags,
		CcActive:        vm.CCActive,
		Snapshot:        vm.Snapshot,
	}
}

func CaptureToProtobuf(capture mm.Capture) *proto.Capture {
	return &proto.Capture{
		Vm:        capture.VM,
		Interface: uint32(capture.Interface), //nolint:gosec // integer overflow conversion int -> uint32
		Filepath:  capture.Filepath,
	}
}

func CapturesToProtobuf(captures []mm.Capture) []*proto.Capture {
	pb := make([]*proto.Capture, len(captures))

	for i, capture := range captures {
		pb[i] = CaptureToProtobuf(capture)
	}

	return pb
}

func ExperimentScheduleToProtobuf(exp types.Experiment) *proto.ExperimentSchedule {
	sched := make([]*proto.Schedule, 0, len(exp.Spec.Schedules()))

	for vm, host := range exp.Spec.Schedules() {
		sched = append(sched, &proto.Schedule{Vm: vm, Host: host}) //nolint:exhaustruct // partial initialization
	}

	return &proto.ExperimentSchedule{Schedule: sched}
}
