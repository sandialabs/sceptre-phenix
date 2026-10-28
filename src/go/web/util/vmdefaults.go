package util

import (
	"phenix/api/experiment"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/proto"
)

// VMDefaultsToProtobuf converts the saved settings of a VM in the form of the
// VM's own fields: the disk as a full path, and the inject partition that
// phenix uses, 1 when the first drive sets none.
func VMDefaultsToProtobuf(saved experiment.VMDefaults) *proto.VMDefaults {
	pb := &proto.VMDefaults{
		Cpus:            int32(saved.CPUs), //nolint:gosec // G115 VM sizes fit in an int32
		Ram:             int32(saved.RAM),  //nolint:gosec // G115 VM sizes fit in an int32
		Disk:            "",
		InjectPartition: 0,
		Snapshot:        saved.Snapshot,
		Dnb:             saved.DNB,
		Host:            saved.Host,
	}

	if saved.Disk != "" {
		pb.Disk = mm.GetMMFullPath(saved.Disk)
		pb.InjectPartition = 1
	}

	if saved.InjectPartition != nil {
		pb.InjectPartition = int32(*saved.InjectPartition) //nolint:gosec // G115 partition numbers fit in an int32
	}

	return pb
}

// SetVMDefaults sets the defaults of each VM of the experiment expName to the
// settings that the experiment's annotations saved for it. A VM with no saved
// settings keeps null defaults, as do all the VMs when the saved settings do
// not decode.
func SetVMDefaults(expName string, annotations map[string]string, vms ...*proto.VM) {
	saved, err := experiment.VMDefaultsOf(annotations)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading the saved VM settings", "exp", expName, "err", err)

		return
	}

	for _, vm := range vms {
		if settings, ok := saved[vm.GetName()]; ok {
			vm.Defaults = VMDefaultsToProtobuf(settings)
		}
	}
}
