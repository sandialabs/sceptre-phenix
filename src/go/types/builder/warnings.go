package builder

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// rulesetKeys are the keys of a spec interface that name a firewall ruleset,
// in the order they are checked, with the traffic each applies to.
//
//nolint:gochecknoglobals // immutable list
var rulesetKeys = []struct{ key, direction string }{
	{key: "ruleset_in", direction: "inbound"},
	{key: "ruleset_out", direction: "outbound"},
}

// ipv4Bits is the length of an IPv4 address in bits.
const ipv4Bits = 32

// DesignWarnings returns the warnings about settings of a valid document that phenix accepts but does not apply as drawn.
// The editor gives the same warnings (collectWarnings in validate.js), and publishing lists them.
func (d *Document) DesignWarnings() []Issue {
	warnings := append(d.rulesetWarnings(), d.addressingWarnings()...)

	return append(warnings, d.deviceWarnings()...)
}

// ownDevice reports whether the node is a device that this document starts
// and configures: not from an included topology, which its own topology
// must fix, and not external, which phenix neither starts nor configures.
func ownDevice(node *Node) bool {
	return node.Kind == NodeKindDevice && node.Device != nil && node.Device.IncludedFrom == "" &&
		node.Device.Spec["external"] == nil
}

// isRoutingType reports whether a device of the spec type applies firewall
// rulesets: phenix's vrouter app configures Router and Firewall nodes only,
// in any case.
func isRoutingType(deviceType string) bool {
	return strings.EqualFold(deviceType, "router") || strings.EqualFold(deviceType, "firewall")
}

// rulesetRef is an interface's use of a ruleset by name.
type rulesetRef struct {
	index     int
	key       string
	direction string
	name      string
}

// specRulesets returns the names of the rulesets a device spec defines, and
// whether it defines any.
func specRulesets(spec map[string]any) (map[string]bool, bool) {
	network, _ := spec["network"].(map[string]any)
	rulesets, _ := network["rulesets"].([]any)
	names := make(map[string]bool, len(rulesets))

	for _, entry := range rulesets {
		ruleset, _ := entry.(map[string]any)
		if name, ok := ruleset["name"].(string); ok {
			names[name] = true
		}
	}

	return names, len(rulesets) > 0
}

// rulesetRefs returns each ruleset that an interface of the spec names, in
// interface order, inbound first. A blank name names none, as in phenix.
func rulesetRefs(ifaces []any) []rulesetRef {
	var refs []rulesetRef

	for index, entry := range ifaces {
		iface, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		for _, use := range rulesetKeys {
			if name, _ := iface[use.key].(string); name != "" {
				refs = append(refs, rulesetRef{index: index, key: use.key, direction: use.direction, name: name})
			}
		}
	}

	return refs
}

// rulesetWarnings warns about firewall rulesets that phenix's vrouter app does not apply.
// The app applies rulesets only on a Router or a Firewall, and skips an interface ruleset that names no ruleset of the device.
func (d *Document) rulesetWarnings() []Issue {
	var warnings []Issue

	for i := range d.Nodes {
		node := &d.Nodes[i]
		if !ownDevice(node) {
			continue
		}

		spec := node.Device.Spec
		ifaces := specNodeInterfaces(spec)
		names, defined := specRulesets(spec)
		refs := rulesetRefs(ifaces)

		if deviceType, _ := spec["type"].(string); !isRoutingType(deviceType) {
			if defined || len(refs) > 0 {
				warnings = append(warnings, rulesetsIgnored(node, i, deviceType))
			}

			continue
		}

		labels := interfaceLabels(ifaces)

		for _, ref := range refs {
			if names[ref.name] {
				continue
			}

			issue := deviceIssue(
				CodeInterfaceRulesetUnknown, node, i,
				fmt.Sprintf("device.spec.network.interfaces[%d].%s", ref.index, ref.key), fmt.Sprintf(
					"interface %s of %q names ruleset %q for %s traffic, but the device has no ruleset of that name. "+
						"Thus phenix applies no %s firewall to the interface",
					labels[ref.index], node.Device.Hostname, ref.name, ref.direction, ref.direction,
				),
			)
			issue.Field = fmt.Sprintf("spec.network.interfaces.%d.%s", ref.index, ref.key)
			warnings = append(warnings, issue)
		}
	}

	return warnings
}

// rulesetsIgnored is the warning about a device at index whose rulesets
// phenix ignores, because its spec type is deviceType.
func rulesetsIgnored(node *Node, index int, deviceType string) Issue {
	kind := "The device has no type"
	if deviceType != "" {
		kind = fmt.Sprintf("The type of the device is %q", deviceType)
	}

	issue := deviceIssue(CodeDeviceRulesetsIgnored, node, index, "device.spec.type", fmt.Sprintf(
		"device %q has firewall rulesets, but phenix applies them only to a device of type Router or Firewall. %s",
		node.Device.Hostname, kind,
	))
	issue.Field = "spec.type"

	return issue
}

// networkMember is a spec interface of a device on a network: the device at
// node among the document's nodes, and the interface at index in its spec.
type networkMember struct {
	node  int
	index int
	iface map[string]any
}

// networkMembers returns the spec interfaces on each network, by network ID, in node then interface order.
// It finds them as the editor does (interfacesOnNetwork in addressing.js): by connection first, then by exact VLAN name.
func (d *Document) networkMembers() map[string][]networkMember {
	connected := make(map[string]string, len(d.Edges))

	for i := range d.Edges {
		edge := &d.Edges[i]

		for _, handle := range []string{edge.SourceHandleID, edge.TargetHandleID} {
			if handle != "" {
				connected[handle] = edge.NetworkID
			}
		}
	}

	exact := make(map[string]string, len(d.Networks))
	folded := make(map[string]string, len(d.Networks))

	for i := range d.Networks {
		network := &d.Networks[i]
		if _, ok := exact[network.Name]; !ok {
			exact[network.Name] = network.ID
		}

		if _, ok := folded[strings.ToLower(network.Name)]; !ok {
			folded[strings.ToLower(network.Name)] = network.ID
		}
	}

	members := make(map[string][]networkMember)

	for i := range d.Nodes {
		node := &d.Nodes[i]
		if node.Kind != NodeKindDevice || node.Device == nil {
			continue
		}

		for index, entry := range specNodeInterfaces(node.Device.Spec) {
			iface, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			id := memberNetwork(node.Device.Interfaces, iface, connected, exact, folded)
			if id != "" {
				members[id] = append(members[id], networkMember{node: i, index: index, iface: iface})
			}
		}
	}

	return members
}

// memberNetwork is the ID of the network a spec interface is on (see
// [Document.networkMembers]), or "" for none.
func memberNetwork(handles []InterfaceHandle, iface map[string]any, connected, exact, folded map[string]string) string {
	if name, ok := iface["name"].(string); ok {
		for _, handle := range handles {
			if handle.Name != name {
				continue
			}

			if id, found := connected[handle.ID]; found {
				return id
			}

			break
		}
	}

	vlan, _ := iface["vlan"].(string)

	vlan = strings.TrimSpace(vlan)
	if vlan == "" {
		return ""
	}

	if id, ok := exact[vlan]; ok {
		return id
	}

	return folded[strings.ToLower(vlan)]
}

// hostIPv4 reads the IPv4 address a spec interface has as its own, as the
// editor reads it (hostAddress in addressing.js): the address before a
// prefix length, without the ASCII white space around it. An interface that
// asks DHCP for its address or starts with none (proto manual) has none.
func hostIPv4(iface map[string]any) (netip.Addr, bool) {
	proto, _ := iface["proto"].(string)

	switch strings.ToLower(trimASCIISpace(proto)) {
	case "dhcp", "manual":
		return netip.Addr{}, false
	}

	text, _ := iface[specAddressKey].(string)
	text, _, _ = strings.Cut(text, "/")

	addr, err := netip.ParseAddr(trimASCIISpace(text))
	if err != nil || !addr.Is4() {
		return netip.Addr{}, false
	}

	return addr, true
}

// addressingWarnings warns about interface addresses outside their subnet, gateways that no
// interface owns, and overlapping subnets.
// A subnet or a gateway that validation refuses gets no warning.
func (d *Document) addressingWarnings() []Issue {
	members := d.networkMembers()

	var warnings []Issue

	for i := range d.Networks {
		network := &d.Networks[i]
		subnet, subnetOK := ParseSubnet(network.Subnet)

		if subnetOK {
			warnings = append(warnings, d.outsideSubnet(network, subnet, members[network.ID])...)
		}

		if issue, ok := unownedGateway(network, i, subnet, subnetOK, members[network.ID]); ok {
			warnings = append(warnings, issue)
		}
	}

	return append(warnings, d.overlappingSubnets()...)
}

// outsideSubnet warns about each interface on the network whose address is
// not a host address of its subnet.
func (d *Document) outsideSubnet(network *Network, subnet netip.Prefix, members []networkMember) []Issue {
	var warnings []Issue

	for _, member := range members {
		node := &d.Nodes[member.node]
		if node.Device.IncludedFrom != "" {
			continue
		}

		addr, ok := hostIPv4(member.iface)
		if !ok || IsSubnetHost(subnet, addr) {
			continue
		}

		labels := interfaceLabels(specNodeInterfaces(node.Device.Spec))
		issue := deviceIssue(
			CodeInterfaceIPOutside, node, member.node,
			fmt.Sprintf("device.spec.network.interfaces[%d].%s", member.index, specAddressKey), fmt.Sprintf(
				"IP address %s of interface %s of %q is not a host address of subnet %s of network %q",
				addr, labels[member.index], node.Device.Hostname, network.Subnet, network.Name,
			),
		)
		issue.Field = fmt.Sprintf("spec.network.interfaces.%d.%s", member.index, specAddressKey)
		warnings = append(warnings, issue)
	}

	return warnings
}

// unownedGateway is the warning about the gateway of the network at index,
// when no interface on it has the gateway as its address, and whether there
// is one.
func unownedGateway(
	network *Network, index int, subnet netip.Prefix, subnetOK bool, members []networkMember,
) (Issue, bool) {
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil || !gateway.Is4() || (network.Subnet != "" && (!subnetOK || !IsSubnetHost(subnet, gateway))) {
		return Issue{}, false
	}

	for _, member := range members {
		if addr, ok := hostIPv4(member.iface); ok && addr == gateway {
			return Issue{}, false
		}
	}

	issue := NewIssue(CodeNetworkGatewayUnowned, fmt.Sprintf("networks[%d].gateway", index), fmt.Sprintf(
		"no interface on network %q has its gateway address %s. Give that address to a router interface on the network, "+
			"or change the gateway",
		network.Name, network.Gateway,
	))
	issue.NetworkID = network.ID

	return issue, true
}

// subnetRange is the subnet of the network at index among the document's
// networks, as its first address and the address after its last.
type subnetRange struct {
	index      int
	base, end  uint64
	subnetText string
}

// overlappingSubnets warns about each network whose subnet overlaps another, as overlappingSubnets in validate.js does.
// Two subnets overlap only when one holds the other, so a sort by first address, largest first, finds each holder.
func (d *Document) overlappingSubnets() []Issue {
	var ranges []subnetRange

	for i := range d.Networks {
		subnet, ok := ParseSubnet(d.Networks[i].Subnet)
		if !ok {
			continue
		}

		base := uint64(ipv4Value(subnet.Addr()))
		ranges = append(ranges, subnetRange{
			index: i, base: base, end: base + 1<<(ipv4Bits-subnet.Bits()), subnetText: d.Networks[i].Subnet,
		})
	}

	slices.SortStableFunc(ranges, func(a, b subnetRange) int {
		return cmp.Or(cmp.Compare(a.base, b.base), cmp.Compare(b.end, a.end), cmp.Compare(a.index, b.index))
	})

	var (
		warnings []Issue
		holders  []subnetRange
	)

	for position, current := range ranges {
		for len(holders) > 0 && holders[len(holders)-1].end <= current.base {
			holders = holders[:len(holders)-1]
		}

		var other *subnetRange

		switch {
		case len(holders) > 0:
			other = &holders[len(holders)-1]
		case position+1 < len(ranges) && ranges[position+1].base < current.end:
			other = &ranges[position+1]
		}

		if other != nil {
			warnings = append(warnings, d.subnetOverlap(current, *other))
		}

		holders = append(holders, current)
	}

	return warnings
}

// subnetOverlap is the warning about the subnet of one network that
// overlaps the subnet of another.
func (d *Document) subnetOverlap(subnet, other subnetRange) Issue {
	network := &d.Networks[subnet.index]
	issue := NewIssue(CodeNetworkSubnetOverlap, fmt.Sprintf("networks[%d].subnet", subnet.index), fmt.Sprintf(
		"subnet %s of network %q overlaps subnet %s of network %q. Thus a router cannot route between them. "+
			"Ignore this warning for networks that never connect, such as isolated enclaves that reuse a subnet on purpose",
		subnet.subnetText, network.Name, other.subnetText, d.Networks[other.index].Name,
	))
	issue.NetworkID = network.ID

	return issue
}
