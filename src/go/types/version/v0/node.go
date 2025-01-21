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
	AnnotationsF map[string]interface{} `json:"annotations" yaml:"annotations" structs:"annotations" mapstructure:"annotations"`
	LabelsF      map[string]string      `json:"labels" yaml:"labels" structs:"labels" mapstructure:"labels"`
	TypeF        string                 `json:"type" yaml:"type" structs:"type" mapstructure:"type"`
	GeneralF     *General               `json:"general" yaml:"general" structs:"general" mapstructure:"general"`
	HardwareF    *Hardware              `json:"hardware" yaml:"hardware" structs:"hardware" mapstructure:"hardware"`
	NetworkF     *Network               `json:"network" yaml:"network" structs:"network" mapstructure:"network"`
	InjectionsF  []*Injection           `json:"injections" yaml:"injections" structs:"injections" mapstructure:"injections"`
}

func (this Node) Annotations() map[string]interface{} {
	return this.AnnotationsF
}

func (this Node) Labels() map[string]string {
	return this.LabelsF
}

func (this Node) Type() string {
	return this.TypeF
}

func (this Node) General() ifaces.NodeGeneral {
	return this.GeneralF
}

func (this Node) Hardware() ifaces.NodeHardware {
	return this.HardwareF
}

func (this Node) Network() ifaces.NodeNetwork {
	return this.NetworkF
}

func (this Node) Injections() []ifaces.NodeInjection {
	injects := make([]ifaces.NodeInjection, len(this.InjectionsF))

	for i, j := range this.InjectionsF {
		injects[i] = j
	}

	return injects
}

func (this Node) Delay() ifaces.NodeDelay {
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

func (this Node) External() bool {
	return false
}

func (this *Node) SetInjections(injections []ifaces.NodeInjection) {
	injects := make([]*Injection, len(injections))

	for i, j := range injections {
		injects[i] = j.(*Injection)
	}

	this.InjectionsF = injects
}

func (this *Node) AddLabel(k, v string) {
	this.LabelsF[k] = v
}

func (this *Node) AddHardware(os string, vcpu, memory int) ifaces.NodeHardware {
	h := &Hardware{
		OSTypeF: os,
		VCPUF:   vcpu,
		MemoryF: memory,
	}

	this.HardwareF = h

	return h
}

func (this *Node) AddNetworkInterface(typ, name, vlan string) ifaces.NodeNetworkInterface {
	i := &Interface{
		TypeF: typ,
		NameF: name,
		VLANF: vlan,
	}

	if this.NetworkF == nil {
		this.NetworkF = new(Network)
	}

	this.NetworkF.InterfacesF = append(this.NetworkF.InterfacesF, i)

	return i
}

func (this *Node) AddNetworkRoute(dest, next string, cost int) {
	r := Route{
		DestinationF: dest,
		NextF:        next,
		CostF:        &cost,
	}

	if this.NetworkF == nil {
		this.NetworkF = new(Network)
	}

	this.NetworkF.RoutesF = append(this.NetworkF.RoutesF, r)
}

func (this *Node) AddInject(src, dst, perms, desc string) {
	this.InjectionsF = append(this.InjectionsF, &Injection{
		SrcF:         src,
		DstF:         dst,
		PermissionsF: perms,
		DescriptionF: desc,
	})
}

func (Node) SetAdvanced(map[string]string) {}
func (Node) AddAdvanced(string, string)    {}
func (Node) AddOverride(string, string)    {}
func (Node) AddCommand(string)             {}

func (this Node) GetAnnotation(a string) (interface{}, bool) {
	if this.AnnotationsF == nil {
		return nil, false
	}

	for k := range this.AnnotationsF {
		if k == a {
			return this.AnnotationsF[k], true
		}
	}

	return nil, false
}

func (Node) Delayed() string {
	return ""
}

type General struct {
	HostnameF    string `json:"hostname" yaml:"hostname" structs:"hostname" mapstructure:"hostname"`
	DescriptionF string `json:"description" yaml:"description" structs:"description" mapstructure:"description"`
	VMTypeF      string `json:"vm_type" yaml:"vm_type" structs:"vm_type" mapstructure:"vm_type"`
	SnapshotF    *bool  `json:"snapshot" yaml:"snapshot" structs:"snapshot" mapstructure:"snapshot"`
	DoNotBootF   *bool  `json:"do_not_boot" yaml:"do_not_boot" structs:"do_not_boot" mapstructure:"do_not_boot"`
	VncHostF     string `json:"vnc_host" yaml:"vnc_host" structs:"vnc_host" mapstructure:"vnc_host"`
	VncPortF     int    `json:"vnc_port" yaml:"vnc_port" structs:"vnc_port" mapstructure:"vnc_port"`
}

func (this General) Hostname() string {
	return this.HostnameF
}

func (this General) Description() string {
	return this.DescriptionF
}

func (this General) VMType() string {
	return this.VMTypeF
}

func (this General) Snapshot() *bool {
	return this.SnapshotF
}
func (this *General) SetSnapshot(b bool) {
	this.SnapshotF = &b
}

func (this General) DoNotBoot() *bool {
	return this.DoNotBootF
}

func (this *General) SetDoNotBoot(b bool) {
	this.DoNotBootF = &b
}

type Hardware struct {
	CPUF    string   `json:"cpu" yaml:"cpu" structs:"cpu" mapstructure:"cpu"`
	VCPUF   int      `json:"vcpus,string" yaml:"vcpus" structs:"vcpus" mapstructure:"vcpus"`
	MemoryF int      `json:"memory,string" yaml:"memory" structs:"memory" mapstructure:"memory"`
	OSTypeF string   `json:"os_type" yaml:"os_type" structs:"os_type" mapstructure:"os_type"`
	DrivesF []*Drive `json:"drives" yaml:"drives" structs:"drives" mapstructure:"drives"`
}

func (this Hardware) CPU() string {
	return this.CPUF
}

func (this Hardware) VCPU() int {
	return this.VCPUF
}

func (this Hardware) Memory() int {
	return this.MemoryF
}

func (this Hardware) OSType() string {
	return this.OSTypeF
}

func (this Hardware) Drives() []ifaces.NodeDrive {
	drives := make([]ifaces.NodeDrive, len(this.DrivesF))

	for i, d := range this.DrivesF {
		drives[i] = d
	}

	return drives
}

func (this *Hardware) SetVCPU(v int) {
	this.VCPUF = v
}

func (this *Hardware) SetMemory(m int) {
	this.MemoryF = m
}

func (this *Hardware) AddDrive(disk string, part int) ifaces.NodeDrive {
	d := &Drive{
		ImageF:           disk,
		InjectPartitionF: &part,
	}

	this.DrivesF = append(this.DrivesF, d)

	return d
}

type Drive struct {
	ImageF           string `json:"image" yaml:"image" structs:"image" mapstructure:"image"`
	IfaceF           string `json:"interface" yaml:"interface" structs:"interface" mapstructure:"interface"`
	CacheModeF       string `json:"cache_mode" yaml:"cache_mode" structs:"cache_mode" mapstructure:"cache_mode"`
	InjectPartitionF *int   `json:"inject_partition,string" yaml:"inject_partition" structs:"inject_partition" mapstructure:"inject_partition"`
}

func (this Drive) Image() string {
	return this.ImageF
}

func (this Drive) Interface() string {
	return this.IfaceF
}

func (this Drive) CacheMode() string {
	return this.CacheModeF
}

func (this Drive) InjectPartition() *int {
	if this.InjectPartitionF != nil {
		return this.InjectPartitionF
	}

	part := 1
	return &part
}

func (this *Drive) SetImage(i string) {
	this.ImageF = i
}

func (this *Drive) SetInjectPartition(p *int) {
	this.InjectPartitionF = p
}

type Injection struct {
	SrcF         string `json:"src" yaml:"src" structs:"src" mapstructure:"src"`
	DstF         string `json:"dst" yaml:"dst" structs:"dst" mapstructure:"dst"`
	DescriptionF string `json:"description" yaml:"description" structs:"description" mapstructure:"description"`
	PermissionsF string `json:"permissions" yaml:"permissions" structs:"permissions" mapstructure:"permissions"`
}

func (this Injection) Src() string {
	return this.SrcF
}

func (this Injection) Dst() string {
	return this.DstF
}

func (this Injection) Description() string {
	return this.DescriptionF
}

func (this Injection) Permissions() string {
	return this.PermissionsF
}

type Delay struct{}

func (this Delay) Timer() time.Duration {
	return 0
}

func (this Delay) User() bool {
	return false
}

func (this Delay) C2() []ifaces.NodeC2Delay {
	return nil
}

func (this *Node) SetDefaults() {
	if this.GeneralF.VMTypeF == "" {
		this.GeneralF.VMTypeF = "kvm"
	}

	if this.GeneralF.SnapshotF == nil {
		snapshot := true
		this.GeneralF.SnapshotF = &snapshot
	}

	if this.GeneralF.DoNotBootF == nil {
		dnb := false
		this.GeneralF.DoNotBootF = &dnb
	}

	if this.HardwareF.CPUF == "" {
		this.HardwareF.CPUF = "Broadwell"
	}

	if this.HardwareF.VCPUF == 0 {
		this.HardwareF.VCPUF = 1
	}

	if this.HardwareF.MemoryF == 0 {
		this.HardwareF.MemoryF = 512
	}

	if this.HardwareF.OSTypeF == "" {
		this.HardwareF.OSTypeF = "linux"
	}

	if this.GeneralF.VncPortF == 0 {
		this.GeneralF.VncPortF = 5900
	}

	this.NetworkF.SetDefaults()
}

func (this Node) FileInjects(baseDir string) string {
	injects := make([]string, len(this.InjectionsF))

	for i, inject := range this.InjectionsF {
		if strings.HasPrefix(inject.SrcF, "/") {
			injects[i] = fmt.Sprintf(`"%s":"%s"`, inject.SrcF, inject.DstF)
		} else {
			injects[i] = fmt.Sprintf(`"%s/%s":"%s"`, baseDir, inject.SrcF, inject.DstF)
		}

		if inject.PermissionsF != "" && len(inject.PermissionsF) <= 4 {
			if perms, err := strconv.ParseInt(inject.PermissionsF, 8, 64); err == nil {
				// Update file permissions on local disk before it gets injected into
				// disk image.
				os.Chmod(inject.SrcF, os.FileMode(perms))
			}
		}
	}

	return strings.Join(injects, " ")
}

func (this Node) RouterName() string {
	if !strings.EqualFold(this.TypeF, "router") {
		return this.GeneralF.HostnameF
	}

	name := strings.ToLower(this.GeneralF.HostnameF)
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, "_", "-")

	return name
}

func (this Hardware) DiskConfig(snapshot string) string {
	configs := make([]string, len(this.DrivesF))

	for i, d := range this.DrivesF {
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

func (this Drive) GetInjectPartition() int {
	if this.InjectPartitionF == nil {
		return 1
	}

	return *this.InjectPartitionF
}

func (this *General) VncHost() string {
	if this == nil {
		return ""
	}

	return this.VncHostF
}

func (this *General) VncPort() int {
	return this.VncPortF
}
