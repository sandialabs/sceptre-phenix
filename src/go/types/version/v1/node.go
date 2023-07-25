package v1

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
	AdvancedF    map[string]string      `json:"advanced" yaml:"advanced" structs:"advanced" mapstructure:"advanced"`
	OverridesF   map[string]string      `json:"overrides" yaml:"overrides" structs:"overrides" mapstructure:"overrides"`
	DelayF       *Delay                 `json:"delay" yaml:"delay" structs:"delay" mapstructure:"delay"`
	CommandsF    []string               `json:"commands" yaml:"commands" structs:"commands" mapstructure:"commands"`
	ExternalF    *bool                  `json:"external" yaml:"external" structs:"external" mapstructure:"external"`
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
	if this.DelayF == nil {
		return new(Delay)
	}

	return this.DelayF
}

func (this Node) Advanced() map[string]string {
	return this.AdvancedF
}

func (this Node) Overrides() map[string]string {
	return this.OverridesF
}

func (this Node) Commands() []string {
	return this.CommandsF
}

func (this Node) External() bool {
	// The topology schema uses the `external` key as a way to determine which of
	// the two node schemas to apply to a configuration. The value is ignored, but
	// the key must be provided in order to use the less stringent node schema.
	// Here, if the key is provided, even if the value is false, we consider it to
	// be an external node.
	//
	// NOTE: the `Node.validate` function should error out if external was
	// intentionally set to false by a user.

	return this.ExternalF != nil
}

func (this *Node) SetInjections(injections []ifaces.NodeInjection) {
	injects := make([]*Injection, len(injections))

	for i, j := range injections {
		injects[i] = j.(*Injection)
	}

	this.InjectionsF = injects
}

func (this *Node) AddLabel(k, v string) {
	if this.LabelsF == nil {
		this.LabelsF = make(map[string]string)
	}

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
	if _, ok := this.LabelsF["disable-injects"]; ok {
		return
	}

	var exists bool

	for _, inject := range this.InjectionsF {
		if inject.DstF == dst {
			inject.SrcF = src
			inject.PermissionsF = perms
			inject.DescriptionF = desc

			exists = true
			break
		}
	}

	if !exists {
		this.InjectionsF = append(this.InjectionsF, &Injection{
			SrcF:         src,
			DstF:         dst,
			PermissionsF: perms,
			DescriptionF: desc,
		})
	}
}

func (this *Node) SetAdvanced(adv map[string]string) {
	this.AdvancedF = adv
}

func (this *Node) AddAdvanced(config, value string) {
	if this.AdvancedF == nil {
		this.AdvancedF = make(map[string]string)
	}

	this.AdvancedF[config] = value
}

func (this *Node) AddOverride(match, replace string) {
	if this.OverridesF == nil {
		this.OverridesF = make(map[string]string)
	}

	this.OverridesF[match] = replace
}

func (this *Node) AddCommand(cmd string) {
	// avoid duplicates
	for _, c := range this.CommandsF {
		if c == cmd {
			return
		}
	}

	this.CommandsF = append(this.CommandsF, cmd)
}

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

func (this Node) Delayed() string {
	if this.DelayF == nil {
		return ""
	}

	if this.DelayF.TimerF != "" {
		return fmt.Sprintf("timer:%s", this.DelayF.TimerF)
	}

	if this.DelayF.UserF {
		return "user"
	}

	if len(this.DelayF.C2F) > 0 {
		hosts := make([]string, len(this.DelayF.C2F))

		for i, host := range this.DelayF.C2F {
			hosts[i] = host.Hostname()
		}

		return fmt.Sprintf("cc:%s", strings.Join(hosts, ","))
	}

	return ""
}

type General struct {
	HostnameF    string `json:"hostname" yaml:"hostname" structs:"hostname" mapstructure:"hostname"`
	DescriptionF string `json:"description" yaml:"description" structs:"description" mapstructure:"description"`
	VMTypeF      string `json:"vm_type" yaml:"vm_type" structs:"vm_type" mapstructure:"vm_type"`
	SnapshotF    *bool  `json:"snapshot" yaml:"snapshot" structs:"snapshot" mapstructure:"snapshot"`
	DoNotBootF   *bool  `json:"do_not_boot" yaml:"do_not_boot" structs:"do_not_boot" mapstructure:"do_not_boot"`
}

func (this *General) Hostname() string {
	if this == nil {
		return ""
	}

	return this.HostnameF
}

func (this *General) Description() string {
	if this == nil {
		return ""
	}

	return this.DescriptionF
}

func (this *General) VMType() string {
	if this == nil {
		return ""
	}

	return this.VMTypeF
}

func (this *General) Snapshot() *bool {
	if this == nil {
		return nil
	}

	if this.SnapshotF == nil {
		snapshot := false
		return &snapshot
	}

	return this.SnapshotF
}

func (this *General) DoNotBoot() *bool {
	if this == nil {
		return nil
	}

	if this.DoNotBootF == nil {
		dnb := false
		return &dnb
	}

	return this.DoNotBootF
}

func (this *General) SetDoNotBoot(b bool) {
	this.DoNotBootF = &b
}

type Hardware struct {
	CPUF    string   `json:"cpu" yaml:"cpu" structs:"cpu" mapstructure:"cpu"`
	VCPUF   int      `json:"vcpus" yaml:"vcpus" structs:"vcpus" mapstructure:"vcpus"`
	MemoryF int      `json:"memory" yaml:"memory" structs:"memory" mapstructure:"memory"`
	OSTypeF string   `json:"os_type" yaml:"os_type" structs:"os_type" mapstructure:"os_type"`
	DrivesF []*Drive `json:"drives" yaml:"drives" structs:"drives" mapstructure:"drives"`
}

func (this *Hardware) CPU() string {
	if this == nil {
		return ""
	}

	return this.CPUF
}

func (this *Hardware) VCPU() int {
	if this == nil {
		return 0
	}

	return this.VCPUF
}

func (this *Hardware) Memory() int {
	if this == nil {
		return 0
	}

	return this.MemoryF
}

func (this *Hardware) OSType() string {
	if this == nil {
		return ""
	}

	return this.OSTypeF
}

func (this *Hardware) Drives() []ifaces.NodeDrive {
	if this == nil {
		return nil
	}

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
	InjectPartitionF *int   `json:"inject_partition" yaml:"inject_partition" structs:"inject_partition" mapstructure:"inject_partition"`
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

func (this Node) validate() error {
	if this.ExternalF == nil {
		return nil
	}

	if external := *this.ExternalF; !external {
		return fmt.Errorf("the external key should not be included for internal nodes (even if set to false)")
	}

	return nil
}

func (this *Node) setDefaults() {
	if this.External() {
		return
	}

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

	if this.AdvancedF == nil {
		this.AdvancedF = make(map[string]string)
	}

	if this.OverridesF == nil {
		this.OverridesF = make(map[string]string)
	}

	if this.NetworkF != nil {
		this.NetworkF.SetDefaults()
	}
}

type Delay struct {
	TimerF string    `json:"timer" yaml:"timer" structs:"timer" mapstructure:"timer"`
	UserF  bool      `json:"user" yaml:"user" structs:"user" mapstructure:"user"`
	C2F    []C2Delay `json:"c2" yaml:"c2" structs:"c2" mapstructure:"c2"`
}

func (this Delay) Timer() time.Duration {
	if this.TimerF == "" {
		return 0
	}

	delay, _ := time.ParseDuration(this.TimerF)
	return delay
}

func (this Delay) User() bool {
	return this.UserF
}

func (this Delay) C2() []ifaces.NodeC2Delay {
	delays := make([]ifaces.NodeC2Delay, len(this.C2F))

	for i, d := range this.C2F {
		delays[i] = d
	}

	return delays
}

type C2Delay struct {
	HostnameF string `json:"hostname" yaml:"hostname" structs:"hostname" mapstructure:"hostname"`
	UseUUIDF  bool   `json:"useUUID" yaml:"useUUID" structs:"useUUID" mapstructure:"useUUID"`
}

func (this C2Delay) Hostname() string {
	return this.HostnameF
}

func (this C2Delay) UseUUID() bool {
	return this.UseUUIDF
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

		if d.CacheModeF == "" {
			if snapshot != "" {
				config = append(config, "writeback")
			}
		} else {
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
