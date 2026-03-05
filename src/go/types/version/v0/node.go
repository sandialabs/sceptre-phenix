package v0

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ifaces "phenix/types/interfaces"
)

type Node struct {
	AnnotationsF map[string]any    `json:"annotations" mapstructure:"annotations" structs:"annotations" yaml:"annotations"`
	LabelsF      map[string]string `json:"labels"      mapstructure:"labels"      structs:"labels"      yaml:"labels"`
	TypeF        string            `json:"type"        mapstructure:"type"        structs:"type"        yaml:"type"`
	GeneralF     *General          `json:"general"     mapstructure:"general"     structs:"general"     yaml:"general"`
	HardwareF    *Hardware         `json:"hardware"    mapstructure:"hardware"    structs:"hardware"    yaml:"hardware"`
	NetworkF     *Network          `json:"network"     mapstructure:"network"     structs:"network"     yaml:"network"`
	InjectionsF  []*Injection      `json:"injections"  mapstructure:"injections"  structs:"injections"  yaml:"injections"`
	DeletionsF   []*Deletion       `json:"deletions"   mapstructure:"deletions"   structs:"deletions"   yaml:"deletions"`
}

func (n Node) Annotations() map[string]any {
	return n.AnnotationsF
}

func (n Node) Labels() map[string]string {
	return n.LabelsF
}

func (n Node) Type() string {
	return n.TypeF
}

func (n Node) General() ifaces.NodeGeneral { //nolint:ireturn // interface
	return n.GeneralF
}

func (n Node) Hardware() ifaces.NodeHardware { //nolint:ireturn // interface
	return n.HardwareF
}

func (n Node) Network() ifaces.NodeNetwork { //nolint:ireturn // interface
	return n.NetworkF
}

func (n Node) Injections() []ifaces.NodeInjection {
	injects := make([]ifaces.NodeInjection, len(n.InjectionsF))

	for i, j := range n.InjectionsF {
		injects[i] = j
	}

	return injects
}

func (n Node) Deletions() []ifaces.NodeDeletion {
	deletions := make([]ifaces.NodeDeletion, len(n.DeletionsF))

	for i, j := range n.DeletionsF {
		deletions[i] = j
	}

	return deletions
}

func (n Node) Delay() ifaces.NodeDelay { //nolint:ireturn // interface
	return new(Delay)
}

func (Node) Advanced() map[string]string {
	return nil
}

func (Node) Overrides() map[string]string {
	return nil
}

func (Node) Commands() []string {
	return nil
}

func (n Node) External() bool {
	return false
}

func (n *Node) SetInjections(injections []ifaces.NodeInjection) {
	injects := make([]*Injection, len(injections))

	for i, j := range injections {
		inj, _ := j.(*Injection)
		injects[i] = inj
	}

	n.InjectionsF = injects
}

func (n *Node) SetDeletions(deletions []ifaces.NodeDeletion) {
	deletionList := make([]*Deletion, len(deletions))

	for i, j := range deletions {
		del, _ := j.(*Deletion)
		deletionList[i] = del
	}

	n.DeletionsF = deletionList
}

func (n *Node) SetType(t string) {
	n.TypeF = t
}

func (n *Node) SetLabels(m map[string]string) {
	n.LabelsF = m
}

func (n *Node) AddAnnotation(k string, i any) {
	if n.AnnotationsF == nil {
		n.AnnotationsF = make(map[string]any)
	}

	n.AnnotationsF[k] = i
}

func (n *Node) AddTimerDelay(string) {}

func (n *Node) AddUserDelay(bool) {}

func (n *Node) AddC2Delay(string, bool) {}

func (n *Node) AddLabel(k, v string) {
	n.LabelsF[k] = v
}

func (n *Node) AddHardware(os string, vcpu, memory int) ifaces.NodeHardware { //nolint:ireturn // interface
	h := &Hardware{ //nolint:exhaustruct // partial initialization
		OSTypeF: os,
		VCPUF:   vcpu,
		MemoryF: memory,
	}

	n.HardwareF = h

	return h
}

func (n *Node) AddNetworkInterface(typ, name, vlan string) ifaces.NodeNetworkInterface { //nolint:ireturn // interface
	i := &Interface{ //nolint:exhaustruct // partial initialization
		TypeF: typ,
		NameF: name,
		VLANF: vlan,
	}

	if n.NetworkF == nil {
		n.NetworkF = new(Network)
	}

	n.NetworkF.InterfacesF = append(n.NetworkF.InterfacesF, i)

	return i
}

func (n *Node) AddNetworkRoute(dest, next string, cost int) {
	r := Route{
		DestinationF: dest,
		NextF:        next,
		CostF:        &cost,
	}

	if n.NetworkF == nil {
		n.NetworkF = new(Network)
	}

	n.NetworkF.RoutesF = append(n.NetworkF.RoutesF, r)
}

func (n *Node) AddNetworkNAT([]map[string][]string) {}

func (n *Node) AddNetworkOSPF(routerID string, dead, hello, retrans int, areas map[int][]string) {
	n.NetworkF.OSPFF = new(OSPF)
	n.NetworkF.OSPFF.RouterIDF = routerID
	n.NetworkF.OSPFF.DeadIntervalF = &dead
	n.NetworkF.OSPFF.HelloIntervalF = &hello
	n.NetworkF.OSPFF.RetransmissionIntervalF = &retrans

	for id, networks := range areas {
		area := new(Area)
		area.AreaIDF = &id

		for _, net := range networks {
			areaNetwork := AreaNetwork{NetworkF: net}
			area.AreaNetworksF = append(area.AreaNetworksF, areaNetwork)
		}

		n.NetworkF.OSPFF.AreasF = append(n.NetworkF.OSPFF.AreasF, *area)
	}
}

func (n *Node) AddInject(src, dst, perms, desc string) {
	n.InjectionsF = append(n.InjectionsF, &Injection{
		SrcF:         src,
		DstF:         dst,
		PermissionsF: perms,
		DescriptionF: desc,
	})
}

func (n *Node) AddDeletion(path, desc string) {
	n.DeletionsF = append(n.DeletionsF, &Deletion{
		PathF:        path,
		DescriptionF: desc,
	})
}

func (Node) SetAdvanced(map[string]string) {}
func (Node) AddAdvanced(string, string)    {}
func (Node) AddOverride(string, string)    {}
func (Node) AddCommand(string)             {}

func (n Node) GetAnnotation(a string) (any, bool) {
	if n.AnnotationsF == nil {
		return nil, false
	}

	for k := range n.AnnotationsF {
		if k == a {
			return n.AnnotationsF[k], true
		}
	}

	return nil, false
}

func (Node) Delayed() string {
	return ""
}

type General struct {
	HostnameF    string `json:"hostname"    mapstructure:"hostname"    structs:"hostname"    yaml:"hostname"`
	DescriptionF string `json:"description" mapstructure:"description" structs:"description" yaml:"description"`
	VMTypeF      string `json:"vm_type"     mapstructure:"vm_type"     structs:"vm_type"     yaml:"vm_type"`
	SnapshotF    *bool  `json:"snapshot"    mapstructure:"snapshot"    structs:"snapshot"    yaml:"snapshot"`
	DoNotBootF   *bool  `json:"do_not_boot" mapstructure:"do_not_boot" structs:"do_not_boot" yaml:"do_not_boot"`
}

func (g General) Hostname() string {
	return g.HostnameF
}

func (g General) Description() string {
	return g.DescriptionF
}

func (g General) VMType() string {
	return g.VMTypeF
}

func (g General) Snapshot() *bool {
	return g.SnapshotF
}

func (g *General) SetSnapshot(b bool) {
	g.SnapshotF = &b
}

func (g General) DoNotBoot() *bool {
	return g.DoNotBootF
}

func (g *General) SetDoNotBoot(b bool) {
	g.DoNotBootF = &b
}

type Hardware struct {
	CPUF    string   `json:"cpu"           mapstructure:"cpu"     structs:"cpu"     yaml:"cpu"`
	VCPUF   int      `json:"vcpus,string"  mapstructure:"vcpus"   structs:"vcpus"   yaml:"vcpus"`
	MemoryF int      `json:"memory,string" mapstructure:"memory"  structs:"memory"  yaml:"memory"`
	OSTypeF string   `json:"os_type"       mapstructure:"os_type" structs:"os_type" yaml:"os_type"`
	DrivesF []*Drive `json:"drives"        mapstructure:"drives"  structs:"drives"  yaml:"drives"`
}

func (h Hardware) CPU() string {
	return h.CPUF
}

func (h Hardware) VCPU() int {
	return h.VCPUF
}

func (h Hardware) Memory() int {
	return h.MemoryF
}

func (h Hardware) OSType() string {
	return h.OSTypeF
}

func (h Hardware) Drives() []ifaces.NodeDrive {
	drives := make([]ifaces.NodeDrive, len(h.DrivesF))

	for i, d := range h.DrivesF {
		drives[i] = d
	}

	return drives
}

func (h *Hardware) SetVCPU(v int) {
	h.VCPUF = v
}

func (h *Hardware) SetMemory(m int) {
	h.MemoryF = m
}

func (h *Hardware) AddDrive(disk string, part int) ifaces.NodeDrive { //nolint:ireturn // interface
	d := &Drive{ //nolint:exhaustruct // partial initialization
		ImageF:           disk,
		InjectPartitionF: &part,
	}

	h.DrivesF = append(h.DrivesF, d)

	return d
}

type Drive struct {
	ImageF           string `json:"image"                   mapstructure:"image"            structs:"image"            yaml:"image"`
	IfaceF           string `json:"interface"               mapstructure:"interface"        structs:"interface"        yaml:"interface"`
	CacheModeF       string `json:"cache_mode"              mapstructure:"cache_mode"       structs:"cache_mode"       yaml:"cache_mode"`
	InjectPartitionF *int   `json:"inject_partition,string" mapstructure:"inject_partition" structs:"inject_partition" yaml:"inject_partition"`
}

func (d Drive) Image() string {
	return d.ImageF
}

func (d Drive) Interface() string {
	return d.IfaceF
}

func (d Drive) CacheMode() string {
	return d.CacheModeF
}

func (d Drive) InjectPartition() *int {
	if d.InjectPartitionF != nil {
		return d.InjectPartitionF
	}

	part := 1

	return &part
}

func (d *Drive) SetImage(i string) {
	d.ImageF = i
}

func (d *Drive) SetInjectPartition(p *int) {
	d.InjectPartitionF = p
}

type Injection struct {
	SrcF         string `json:"src"         mapstructure:"src"         structs:"src"         yaml:"src"`
	DstF         string `json:"dst"         mapstructure:"dst"         structs:"dst"         yaml:"dst"`
	DescriptionF string `json:"description" mapstructure:"description" structs:"description" yaml:"description"`
	PermissionsF string `json:"permissions" mapstructure:"permissions" structs:"permissions" yaml:"permissions"`
}

func (i Injection) Src() string {
	return i.SrcF
}

func (i Injection) Dst() string {
	return i.DstF
}

func (i Injection) Description() string {
	return i.DescriptionF
}

func (i Injection) Permissions() string {
	return i.PermissionsF
}

type Deletion struct {
	PathF        string `json:"path"        mapstructure:"path"        structs:"path"        yaml:"path"`
	DescriptionF string `json:"description" mapstructure:"description" structs:"description" yaml:"description"`
}

func (d Deletion) Path() string {
	return d.PathF
}

func (d Deletion) Description() string {
	return d.DescriptionF
}

type Delay struct{}

func (d Delay) Timer() time.Duration {
	return 0
}

func (d Delay) User() bool {
	return false
}

func (d Delay) C2() []ifaces.NodeC2Delay {
	return nil
}

func (n *Node) SetDefaults() {
	if n.GeneralF.VMTypeF == "" {
		n.GeneralF.VMTypeF = "kvm"
	}

	if n.GeneralF.SnapshotF == nil {
		snapshot := true
		n.GeneralF.SnapshotF = &snapshot
	}

	if n.GeneralF.DoNotBootF == nil {
		dnb := false
		n.GeneralF.DoNotBootF = &dnb
	}

	if n.HardwareF.CPUF == "" {
		n.HardwareF.CPUF = "Broadwell"
	}

	if n.HardwareF.VCPUF == 0 {
		n.HardwareF.VCPUF = 1
	}

	if n.HardwareF.MemoryF == 0 {
		n.HardwareF.MemoryF = 512
	}

	if n.HardwareF.OSTypeF == "" {
		n.HardwareF.OSTypeF = "linux"
	}

	n.NetworkF.SetDefaults()
}

func (n Node) FileInjects(baseDir string) string {
	injects := make([]string, len(n.InjectionsF))

	for i, inject := range n.InjectionsF {
		if strings.HasPrefix(inject.SrcF, "/") {
			injects[i] = fmt.Sprintf(`"%s":"%s"`, inject.SrcF, inject.DstF)
		} else {
			injects[i] = fmt.Sprintf(`"%s/%s":"%s"`, baseDir, inject.SrcF, inject.DstF)
		}

		if inject.PermissionsF != "" && len(inject.PermissionsF) <= 4 {
			if perms, err := strconv.ParseInt(inject.PermissionsF, 8, 64); err == nil {
				// Update file permissions on local disk before it gets injected into
				// disk image.
				_ = os.Chmod(inject.SrcF, os.FileMode(perms)) //nolint:gosec // integer overflow conversion int64 -> uint32
			}
		}
	}

	return strings.Join(injects, " ")
}

func (n Node) FileDeletions() string {
	deletions := make([]string, len(n.DeletionsF))

	for i, deletion := range n.DeletionsF {
		deletions[i] = fmt.Sprintf(`"%s"`, deletion.PathF)
	}

	return strings.Join(deletions, ",")
}

func (n Node) RouterName() string {
	if !strings.EqualFold(n.TypeF, "router") {
		return n.GeneralF.HostnameF
	}

	name := strings.ToLower(n.GeneralF.HostnameF)
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")

	return name
}

func (h Hardware) DiskConfig(snapshot string) string {
	configs := make([]string, len(h.DrivesF))

	for i, d := range h.DrivesF {
		config := []string{d.ImageF}

		if i == 0 && snapshot != "" {
			config[0] = snapshot
		}

		if d.IfaceF != "" {
			config = append(config, d.IfaceF)
		}

		if d.CacheModeF != "" {
			config = append(config, d.CacheModeF)
		}

		configs[i] = strings.Join(config, ",")
	}

	return strings.Join(configs, " ")
}

func (d Drive) GetInjectPartition() int {
	if d.InjectPartitionF == nil {
		return 1
	}

	return *d.InjectPartitionF
}
