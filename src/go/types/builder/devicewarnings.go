package builder

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The labels and annotations of a node spec that phenix's apps read.
const (
	labelNTPServer        = "ntp-server"
	annotationDefaultApps = "phenix/default-apps"
	annotationAutotunnel  = "phenix/startup-autotunnel"
	annotationEnableSSH   = "vrouter/enable-ssh"
)

// The values of a node spec that the device checks compare with.
const (
	osTypeLinux      = "linux"
	osTypeMinirouter = "minirouter"
	protoOSPF        = "ospf"
	vlanMgmt         = "mgmt"
	serialType       = "serial"
	maxTunnelPort    = 65535
	tunnelVNC        = "VNC"
	ipv4MaskBits     = 32
)

// Lists of os_type values, in lowercase, as phenix's apps compare them.
//
//nolint:gochecknoglobals // immutable lists
var (
	// vyattaOSTypes are those of a router that phenix's vrouter app
	// configures with its vyatta template, linux as a deprecated vyatta.
	vyattaOSTypes = []string{"vyatta", "vyos", osTypeLinux}
	// routerOSTypes are those of a router that phenix configures.
	routerOSTypes = []string{"vyatta", "vyos", osTypeMinirouter}
	// startupOSTypes are those phenix's startup app writes interface
	// configuration for.
	startupOSTypes = []string{osTypeLinux, "rhel", "centos", "windows"}
)

// The forms of a phenix/startup-autotunnel entry, by their parts: "port",
// "port:dport" and "port:host:dport".
const (
	tunnelPortOnly = iota + 1
	tunnelPortAndDport
	tunnelPortHostDport
)

// tunnelPort is a port of a phenix/startup-autotunnel entry.
var tunnelPort = regexp.MustCompile(`^[0-9]{1,5}$`)

// specMap reads the object at keys of a spec, or nil.
func specMap(spec map[string]any, keys ...string) map[string]any {
	current := spec

	for _, key := range keys {
		current, _ = current[key].(map[string]any)
	}

	return current
}

// specList reads the list at keys of a spec, or nil.
func specList(spec map[string]any, keys ...string) []any {
	last := len(keys) - 1
	list, _ := specMap(spec, keys[:last]...)[keys[last]].([]any)

	return list
}

// defaultAppsOn reports whether phenix runs its default apps on a node: unless
// its phenix/default-apps annotation is false.
func defaultAppsOn(spec map[string]any) bool {
	enabled, ok := specMap(spec, "annotations")[annotationDefaultApps].(bool)

	return !ok || enabled
}

// doNotBoot reports whether a node's general.do_not_boot is true.
func doNotBoot(spec map[string]any) bool {
	return specMap(spec, specGeneral)["do_not_boot"] == true
}

// osPhrase names a node's os_type in a message.
func osPhrase(osType string) string {
	if osType == "" {
		return "The device has no os_type"
	}

	return fmt.Sprintf("The os_type of the device is %q", osType)
}

// interfacesPhrase names interfaces in a message by their labels (see
// [interfaceLabels]).
func interfacesPhrase(labels []string) string {
	if len(labels) == 1 {
		return "interface " + labels[0]
	}

	return "interfaces " + strings.Join(labels, ", ")
}

// interfaceByName returns the index of the first spec interface whose name
// is name in any case, as phenix finds an interface by name, or -1.
func interfaceByName(ifaces []any, name string) int {
	return slices.IndexFunc(ifaces, func(iface any) bool {
		_, isMap := iface.(map[string]any)

		return isMap && strings.EqualFold(interfaceName(iface), name)
	})
}

// interfaceAddress reads the address of a spec interface, as written.
func interfaceAddress(iface any) string {
	asMap, _ := iface.(map[string]any)
	address, _ := asMap[specAddressKey].(string)

	return address
}

// fieldIssue is an issue of code about the device node at index among the
// document's nodes, at the spec field of a dotted data path (spec.labels.x).
func fieldIssue(code Code, node *Node, index int, field, message string) Issue {
	var path strings.Builder

	path.WriteString("device")

	for part := range strings.SplitSeq(field, ".") {
		if _, err := strconv.Atoi(part); err == nil {
			path.WriteString("[" + part + "]")
		} else {
			path.WriteString("." + part)
		}
	}

	issue := deviceIssue(code, node, index, path.String(), message)
	issue.Field = field

	return issue
}

// deviceWarnings warns about the settings of the devices the document starts
// that phenix's apps do not apply as the diagram shows them.
func (d *Document) deviceWarnings() []Issue {
	var warnings []Issue

	for i := range d.Nodes {
		node := &d.Nodes[i]
		if !ownDevice(node) {
			continue
		}

		warnings = append(warnings, deviceSetupWarnings(node, i)...)
		warnings = append(warnings, ospfWarnings(node, i)...)
		warnings = append(warnings, annotationWarnings(node, i)...)
	}

	_, ntp := d.ntpIssues()

	return append(warnings, ntp...)
}

// deviceSetupWarnings warns about what phenix's default apps do not configure
// as drawn. With default apps off, only the injections warning applies.
func deviceSetupWarnings(node *Node, index int) []Issue {
	var (
		spec       = node.Device.Spec
		hostname   = node.Device.Hostname
		deviceType = specString(spec, "type")
		osType     = specString(spec, "hardware", "os_type")
		lower      = strings.ToLower(osType)
		ifaces     = specNodeInterfaces(spec)
		labels     = interfaceLabels(ifaces)
		warnings   []Issue
	)

	osIssue := func(code Code, message string) {
		warnings = append(warnings, fieldIssue(code, node, index, "spec.hardware.os_type", message))
	}

	if defaultAppsOn(spec) {
		var static, serial []string

		for k, entry := range ifaces {
			iface, _ := entry.(map[string]any)
			kind, _ := iface["type"].(string)
			proto, _ := iface["proto"].(string)

			switch {
			case iface == nil:
			case kind == serialType:
				serial = append(serial, labels[k])
			case slices.Contains([]string{"static", protoOSPF}, strings.ToLower(proto)):
				static = append(static, labels[k])
			}
		}

		switch routing := isRoutingType(deviceType); {
		case routing && lower == osTypeLinux:
			osIssue(CodeDeviceRouterOSUnsupported, fmt.Sprintf(
				"device %q is a %s with os_type %q. phenix configures this os_type as vyatta, "+
					"but phenix deprecated it for routers. "+
					"Set hardware.os_type to vyatta, vyos or minirouter",
				hostname, deviceType, osType,
			))
		case routing && !slices.Contains(routerOSTypes, lower):
			osIssue(CodeDeviceRouterOSUnsupported, fmt.Sprintf(
				"device %q is a %s, but phenix configures a router only when its os_type is vyatta, vyos or minirouter. "+
					"%s. Thus phenix does not configure its routing or firewall. "+
					"Set hardware.os_type to vyatta, vyos or minirouter",
				hostname, deviceType, osPhrase(osType),
			))
		case !routing && osType != "" && !slices.Contains(startupOSTypes, lower) && len(static) > 0:
			osIssue(CodeDeviceInterfacesUnset, fmt.Sprintf(
				"device %q has os_type %q, for which phenix writes no interface configuration. Thus phenix gives its "+
					"static %s no address. Set hardware.os_type to linux, rhel, centos or windows, "+
					"or set the addresses in its image",
				hostname, osType, interfacesPhrase(static),
			))
		}

		if osType != osTypeLinux && len(serial) > 0 {
			osIssue(CodeDeviceSerialUnset, fmt.Sprintf(
				"device %q has serial %s, but phenix configures serial interfaces only on a device whose os_type is "+
					"exactly %q. %s. Set hardware.os_type to linux",
				hostname, interfacesPhrase(serial), osTypeLinux, osPhrase(osType),
			))
		}
	}

	if issue, ok := injectionsWarning(node, index); ok {
		warnings = append(warnings, issue)
	}

	return warnings
}

// injectionsWarning is the warning about file injections and deletions that
// minimega's script does not apply, and whether there is one.
func injectionsWarning(node *Node, index int) (Issue, bool) {
	spec := node.Device.Spec
	injections, deletions := len(specList(spec, "injections")), len(specList(spec, "deletions"))

	if injections+deletions == 0 || doNotBoot(spec) {
		return Issue{}, false
	}

	files := "file injections and deletions"

	switch {
	case deletions == 0:
		files = "file injections"
	case injections == 0:
		files = "file deletions"
	}

	if snapshot, ok := specMap(spec, specGeneral)["snapshot"].(bool); ok && !snapshot {
		return fieldIssue(CodeDeviceInjectionsUnapplied, node, index, "spec.general.snapshot", fmt.Sprintf(
			"device %q has %s, but its general.snapshot is false. Thus phenix neither injects nor deletes its files. "+
				"Set general.snapshot to true",
			node.Device.Hostname, files,
		)), true
	}

	drives := specList(spec, "hardware", "drives")
	if len(drives) == 0 {
		return Issue{}, false
	}

	drive, _ := drives[0].(map[string]any)
	if !isZero(drive["inject_partition"]) {
		return Issue{}, false
	}

	return fieldIssue(CodeDeviceInjectionsUnapplied, node, index, "spec.hardware.drives.0.inject_partition", fmt.Sprintf(
		"device %q has %s, but the inject_partition of its first drive is 0. Thus phenix neither injects nor deletes "+
			"its files. Set hardware.drives[0].inject_partition to the partition that holds the file system",
		node.Device.Hostname, files,
	)), true
}

// isZero reports whether a spec value is the number 0.
func isZero(value any) bool {
	switch number := value.(type) {
	case int:
		return number == 0
	case float64:
		return number == 0
	}

	return false
}

// ospfRouter is what [ospfWarnings] and [ospfError] read of a router.
type ospfRouter struct {
	ifaces []any
	labels []string
}

// routerOf returns a Router or Firewall whose default apps are on, with its
// lowercase os_type, and reports false for another device.
func routerOf(node *Node) (ospfRouter, string, bool) {
	spec := node.Device.Spec
	if !isRoutingType(specString(spec, "type")) || !defaultAppsOn(spec) {
		return ospfRouter{}, "", false
	}

	ifaces := specNodeInterfaces(spec)

	return ospfRouter{ifaces: ifaces, labels: interfaceLabels(ifaces)},
		strings.ToLower(specString(spec, "hardware", "os_type")), true
}

// ospfError is the error about an ospf interface of a vyatta-template router
// with no network.ospf, which the template reads, and whether there is one.
func ospfError(node *Node, index int) (Issue, bool) {
	router, osType, ok := routerOf(node)
	if !ok || !slices.Contains(vyattaOSTypes, osType) || specMap(node.Device.Spec, "network")[protoOSPF] != nil {
		return Issue{}, false
	}

	for k, entry := range router.ifaces {
		iface, _ := entry.(map[string]any)
		if proto, _ := iface["proto"].(string); proto != protoOSPF {
			continue
		}

		return fieldIssue(CodeDeviceOSPFMissing, node, index, fmt.Sprintf("spec.network.interfaces.%d.proto", k),
			fmt.Sprintf(
				"interface %s of %q has proto ospf, but the device has no network.ospf. Thus phenix cannot write its "+
					"router configuration. Thus the experiment fails to start. Add network.ospf to the device, "+
					"or set the proto of the interface to static",
				router.labels[k], node.Device.Hostname,
			)), true
	}

	return Issue{}, false
}

// areaNetwork is an IPv4 network of an OSPF area of a router: the network
// at network.ospf.areas[area].area_networks[index].
type areaNetwork struct {
	area, index int
	text        string
	network     *net.IPNet
}

// areaNetworks returns the IPv4 networks of the OSPF areas of a spec, as
// [net.ParseCIDR] reads them, which phenix uses. It leaves out the others.
func areaNetworks(ospf map[string]any) []areaNetwork {
	var networks []areaNetwork

	for a, area := range specList(ospf, "areas") {
		areaMap, _ := area.(map[string]any)

		for n, entry := range specList(areaMap, "area_networks") {
			entryMap, _ := entry.(map[string]any)
			text, _ := entryMap["network"].(string)

			_, network, err := net.ParseCIDR(text)
			if err != nil {
				continue
			}

			if _, bits := network.Mask.Size(); bits == ipv4MaskBits {
				networks = append(networks, areaNetwork{area: a, index: n, text: text, network: network})
			}
		}
	}

	return networks
}

// ospfWarnings warns about the OSPF settings of a minirouter, which runs OSPF
// only on ospf interfaces with an address in an area network.
func ospfWarnings(node *Node, index int) []Issue {
	router, osType, ok := routerOf(node)
	if !ok || osType != osTypeMinirouter || doNotBoot(node.Device.Spec) {
		return nil
	}

	var (
		hostname = node.Device.Hostname
		ospf     = specMap(node.Device.Spec, "network")[protoOSPF]
		ospfMap  = specMap(node.Device.Spec, "network", protoOSPF)
		indexes  []int
		warnings []Issue
	)

	for k, entry := range router.ifaces {
		iface, _ := entry.(map[string]any)
		if proto, _ := iface["proto"].(string); strings.EqualFold(proto, protoOSPF) {
			indexes = append(indexes, k)
		}
	}

	outside := func(k int, message string) {
		warnings = append(warnings, fieldIssue(
			CodeInterfaceOSPFOutside, node, index, fmt.Sprintf("spec.network.interfaces.%d.address", k), message,
		))
	}

	if ospf == nil {
		for _, k := range indexes {
			outside(k, fmt.Sprintf(
				"interface %s of %q has proto ospf, but the device has no network.ospf. Thus the interface is in no "+
					"OSPF area. Add network.ospf with an area network that holds the address of the interface",
				router.labels[k], hostname,
			))
		}

		return warnings
	}

	if len(indexes) == 0 {
		return []Issue{fieldIssue(CodeDeviceOSPFUnused, node, index, "spec.network.ospf", fmt.Sprintf(
			"device %q has network.ospf, but none of its interfaces has proto ospf. Thus minirouter runs OSPF on no "+
				"interface. Set the proto of the interfaces that must run OSPF to ospf, or remove network.ospf",
			hostname,
		))}
	}

	networks := areaNetworks(ospfMap)
	used := make([]bool, len(networks))

	for _, k := range indexes {
		address := net.ParseIP(interfaceAddress(router.ifaces[k])).To4()
		if address == nil {
			continue
		}

		held := false

		for n, network := range networks {
			if network.network.Contains(address) {
				used[n], held = true, true
			}
		}

		if !held {
			outside(k, fmt.Sprintf(
				"IP address %s of interface %s of %q is in no area network of its network.ospf. Thus the interface "+
					"is in no OSPF area. Add an area network that holds the address",
				address, router.labels[k], hostname,
			))
		}
	}

	for n, network := range networks {
		if used[n] {
			continue
		}

		warnings = append(warnings, fieldIssue(CodeDeviceOSPFNetworkUnused, node, index,
			fmt.Sprintf("spec.network.ospf.areas.%d.area_networks.%d.network", network.area, network.index),
			fmt.Sprintf(
				"OSPF area network %s of %q holds the address of no interface with proto ospf. Thus minirouter does "+
					"not advertise the area network. Give an ospf interface of the device an address in the area network, "+
					"or remove the area network",
				network.text, hostname,
			)))
	}

	return warnings
}

// annotationWarnings warns about the values of the vrouter/enable-ssh,
// phenix/startup-autotunnel and phenix/default-apps annotations of a device.
func annotationWarnings(node *Node, index int) []Issue {
	var (
		annotations = specMap(node.Device.Spec, "annotations")
		hostname    = node.Device.Hostname
		warnings    []Issue
	)

	warn := func(code Code, key, message string) {
		warnings = append(warnings, fieldIssue(code, node, index, "spec.annotations."+key, message))
	}

	// Only the vrouter app reads vrouter/enable-ssh, for a vyatta-template router.
	_, osType, router := routerOf(node)

	if value, ok := annotations[annotationEnableSSH]; ok && router && slices.Contains(vyattaOSTypes, osType) {
		if message := enableSSHProblem(node, value); message != "" {
			warn(CodeDeviceEnableSSHInvalid, annotationEnableSSH, message)
		}
	}

	if value, ok := annotations[annotationAutotunnel]; ok {
		for _, message := range autotunnelProblems(hostname, value) {
			warn(CodeDeviceAutotunnelInvalid, annotationAutotunnel, message)
		}
	}

	if value, ok := annotations[annotationDefaultApps]; ok {
		if _, isBool := value.(bool); !isBool {
			warn(CodeDeviceDefaultAppsInvalid, annotationDefaultApps, fmt.Sprintf(
				"the phenix/default-apps annotation of %q is not true or false. Thus phenix runs its default apps on "+
					"the device. To disable them, set the annotation to false, not the text \"false\"",
				hostname,
			))
		}
	}

	return warnings
}

// enableSSHProblem says why the vrouter app turns on no SSH for a
// vrouter/enable-ssh value, or "". It takes an IP address or an interface name.
func enableSSHProblem(node *Node, value any) string {
	hostname := node.Device.Hostname
	fix := "Set the annotation to an IP address or to the name of an interface of the device"

	text, ok := value.(string)
	if !ok {
		return fmt.Sprintf("the vrouter/enable-ssh annotation of %q is not text. Thus phenix does not enable SSH. %s",
			hostname, fix)
	}

	if net.ParseIP(text) != nil {
		return ""
	}

	ifaces := specNodeInterfaces(node.Device.Spec)

	switch k := interfaceByName(ifaces, text); {
	case k < 0:
		return fmt.Sprintf("the vrouter/enable-ssh annotation of %q is %q, which is neither an IP address nor the "+
			"name of an interface of the device. Thus phenix does not enable SSH. %s", hostname, text, fix)
	case interfaceAddress(ifaces[k]) == "":
		return fmt.Sprintf("the vrouter/enable-ssh annotation of %q names interface %s, which has no IP address. "+
			"Thus phenix does not enable SSH. Give the interface an address, or name another interface",
			hostname, interfaceLabels(ifaces)[k])
	}

	return ""
}

// autotunnelProblems says what of a phenix/startup-autotunnel value the
// startup app does not open: one problem for each entry it skips.
func autotunnelProblems(hostname string, value any) []string {
	const forms = `"port", "port:dport" or "port:host:dport"`

	if value == nil {
		return nil
	}

	entries, ok := value.([]any)
	if !ok {
		return []string{fmt.Sprintf(
			"the phenix/startup-autotunnel annotation of %q is not a list. Thus phenix opens none of its tunnels. "+
				`Set the annotation to a list of texts such as "8080", "2222:22" or "9090:10.0.0.5:80"`,
			hostname,
		)}
	}

	for n, entry := range entries {
		if _, isText := entry.(string); !isText && entry != nil {
			return []string{fmt.Sprintf(
				"entry %d of the phenix/startup-autotunnel annotation of %q is not text. Thus phenix opens none of its "+
					"tunnels. Write each tunnel as %s",
				n+1, hostname, forms,
			)}
		}
	}

	var problems []string

	for n, entry := range entries {
		text, _ := entry.(string)
		if validTunnel(text) {
			continue
		}

		shown := strconv.Quote(text)
		if entry == nil {
			shown = "null"
		}

		problems = append(problems, fmt.Sprintf(
			"entry %d of the phenix/startup-autotunnel annotation of %q is %s, which is not %s. "+
				"Thus phenix does not open that tunnel",
			n+1, hostname, shown, forms,
		))
	}

	return problems
}

// validTunnel reports whether a phenix/startup-autotunnel entry is "port",
// "port:dport" or "port:host:dport", with ports from 1 to 65535. A dport can
// also be VNC, in any case, which needs no host.
func validTunnel(text string) bool {
	parts := strings.Split(text, ":")

	port := func(part string) bool {
		number, err := strconv.Atoi(part)

		return tunnelPort.MatchString(part) && err == nil && number >= 1 && number <= maxTunnelPort
	}
	vnc := func(part string) bool { return strings.EqualFold(part, tunnelVNC) }

	switch len(parts) {
	case tunnelPortOnly:
		return port(parts[0])
	case tunnelPortAndDport:
		return port(parts[0]) && (port(parts[1]) || vnc(parts[1]))
	case tunnelPortHostDport:
		return port(parts[0]) && (vnc(parts[2]) || parts[1] != "" && port(parts[2]))
	}

	return false
}

// ntpServer is a device with the ntp-server label, and the label's value.
type ntpServer struct {
	node  *Node
	index int
	value any
}

// ntpIssues returns the errors, then the warnings, about the ntp-server label,
// which the ntp app reads from the first node with it, and vrouter with mgmt.
func (d *Document) ntpIssues() ([]Issue, []Issue) {
	var servers []ntpServer

	for i := range d.Nodes {
		node := &d.Nodes[i]
		if !projectedDevice(node) {
			continue
		}

		if value, ok := specMap(node.Device.Spec, "labels")[labelNTPServer]; ok {
			servers = append(servers, ntpServer{node: node, index: i, value: value})
		}
	}

	if len(servers) == 0 {
		return nil, nil
	}

	var (
		first          = servers[0]
		hostname       = first.node.Device.Hostname
		errs, warnings []Issue
		field          = "spec.labels." + labelNTPServer
		ifaces         = specNodeInterfaces(first.node.Device.Spec)
		name, isText   = first.value.(string)
		named          = interfaceByName(ifaces, name)
	)

	for _, other := range servers[1:] {
		warnings = append(warnings, fieldIssue(CodeDeviceNTPServerIgnored, other.node, other.index, field, fmt.Sprintf(
			"device %q has the ntp-server label, but phenix uses only the first device with it, %q",
			other.node.Device.Hostname, hostname,
		)))
	}

	switch {
	case !isText:
	case named < 0:
		errs = append(errs, fieldIssue(CodeDeviceNTPServerInvalid, first.node, first.index, field, fmt.Sprintf(
			"the ntp-server label of %q is %q, which names no interface of the device. Thus the experiment fails to "+
				"start. Set the label to the name of an interface with an IP address",
			hostname, name,
		)))
	case interfaceAddress(ifaces[named]) == "":
		errs = append(errs, fieldIssue(CodeDeviceNTPServerInvalid, first.node, first.index, field, fmt.Sprintf(
			"the ntp-server label of %q names interface %s, which has no IP address. Thus the experiment fails to "+
				"start. Give the interface an address, or name another interface",
			hostname, interfaceLabels(ifaces)[named],
		)))
	}

	if d.hasVyattaRouter() && !slices.ContainsFunc(d.publishedVLANs(first.node), func(vlan string) bool {
		return strings.EqualFold(vlan, vlanMgmt)
	}) {
		warnings = append(warnings, fieldIssue(CodeDeviceNTPServerNoMgmt, first.node, first.index, field, fmt.Sprintf(
			"routers take their NTP server address from an interface of %q on VLAN mgmt, but the device has no such "+
				"interface. Thus phenix gives the routers no NTP server, unless an ntp app of the scenario names one. "+
				"Connect an interface of %q to "+
				"a network named mgmt",
			hostname, hostname,
		)))
	}

	return errs, warnings
}

// hasVyattaRouter reports whether the document has a router that phenix's
// vrouter app configures with its vyatta template.
func (d *Document) hasVyattaRouter() bool {
	for i := range d.Nodes {
		if node := &d.Nodes[i]; ownDevice(node) {
			if _, osType, ok := routerOf(node); ok && slices.Contains(vyattaOSTypes, osType) {
				return true
			}
		}
	}

	return false
}

// publishedVLANs returns the VLAN each spec interface of a device is published
// with (see [connectInterfaces]).
func (d *Document) publishedVLANs(node *Node) []string {
	ifaces := specNodeInterfaces(node.Device.Spec)
	vlans := make([]string, len(ifaces))

	for k, iface := range ifaces {
		vlans[k] = interfaceVLAN(iface)
	}

	handleNetworks := d.handleNetworks()

	for _, handle := range node.Device.Interfaces {
		network := handleNetworks[handle.ID]
		if network == nil {
			continue
		}

		if k := findInterfaceIndex(ifaces, handle.Name); k >= 0 {
			vlans[k] = network.Name
		}
	}

	return vlans
}

// drivesError is the error about a device with no drive, which the phenix
// schema refuses, and whether there is one.
func drivesError(node *Node, index int) (Issue, bool) {
	drives := specMap(node.Device.Spec, "hardware")["drives"]
	if list, ok := drives.([]any); drives != nil && (!ok || len(list) > 0) {
		return Issue{}, false
	}

	return fieldIssue(CodeDeviceDrivesNone, node, index, "spec.hardware.drives", fmt.Sprintf(
		"device %q has no drive, which phenix needs to start it. Add a drive to hardware.drives",
		node.Device.Hostname,
	)), true
}

// setupErrors returns the device settings that make the experiment fail to
// start (see [drivesError], [ospfError] and [Document.ntpIssues]), or nil.
func (d *Document) setupErrors() *DeviceSetupError {
	var issues []Issue

	for i := range d.Nodes {
		node := &d.Nodes[i]
		if !ownDevice(node) {
			continue
		}

		if issue, ok := drivesError(node, i); ok {
			issues = append(issues, issue)
		}

		if issue, ok := ospfError(node, i); ok {
			issues = append(issues, issue)
		}
	}

	ntp, _ := d.ntpIssues()
	issues = append(issues, ntp...)

	if len(issues) == 0 {
		return nil
	}

	return &DeviceSetupError{Problems: IssueMessages(issues), issues: issues}
}
