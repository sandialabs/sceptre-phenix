package util

import (
	"sort"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	"phenix/util/mm"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
)

func ExperimentToProtobuf(
	exp types.Experiment,
	status cache.Status,
	vms []mm.VM,
) *proto.Experiment {
	pb := &proto.Experiment{ //nolint:exhaustruct // partial initialization
		Name:      exp.Spec.ExperimentName(),
		Topology:  exp.Metadata.Annotations["topology"],
		Scenario:  exp.Metadata.Annotations["scenario"],
		StartTime: exp.Status.StartTime(),
		Running:   exp.Running(),
		Status:    string(status),
		VmCount:   uint32(len(vms)), //nolint:gosec // integer overflow conversion int -> uint32
	}

	pb.Vms = make([]*proto.VM, len(vms))
	for i, v := range vms {
		vm := VMToProtobuf(exp.Spec.ExperimentName(), v, exp.Spec.Topology())

		pb.Vms[i] = vm
		if vm.GetDelayedStart() != "" {
			pb.DelayedVms++
		}
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

func VMToProtobuf(exp string, vm mm.VM, topology ifaces.TopologySpec) *proto.VM {
	v := &proto.VM{ //nolint:exhaustruct // partial initialization
		Name:            vm.Name,
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

	if topology == nil {
		return v
	}

	if vm := topology.FindNodeByName(vm.Name); vm != nil {
		v.DelayedStart = vm.Delayed()
		v.External = vm.External()
	}

	return v
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

func UserToProtobuf(u rbac.User) *proto.User {
	role, _ := u.Role()
	user := &proto.User{
		Username:      u.Username(),
		FirstName:     u.FirstName(),
		LastName:      u.LastName(),
		ResourceNames: resourceNamesForRole(role),
		Role:          RoleToProtobuf(role),
	}

	return user
}

func resourceNamesForRole(r rbac.Role) []string {
	rnamemap := make(map[string]struct{})

	for _, p := range r.Spec.Policies {
		var skip bool

		for _, pn := range p.Resources {
			if pn == "disks" || pn == "hosts" || pn == "users" {
				skip = true

				break
			}
		}

		if skip {
			continue
		}

		for _, n := range p.ResourceNames {
			rnamemap[n] = struct{}{}
		}
	}

	rnames := make([]string, 0, len(rnamemap))
	for n := range rnamemap {
		rnames = append(rnames, n)
	}

	sort.Strings(rnames)

	return rnames
}

func RoleToProtobuf(r rbac.Role) *proto.Role {
	policies := make([]*proto.Policy, len(r.Spec.Policies))
	for i, p := range r.Spec.Policies {
		policies[i] = &proto.Policy{
			Resources:     p.Resources,
			ResourceNames: p.ResourceNames,
			Verbs:         p.Verbs,
		}
	}

	role := &proto.Role{
		Name:     r.Spec.Name,
		Policies: policies,
	}

	return role
}
