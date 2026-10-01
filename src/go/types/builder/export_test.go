package builder

import "slices"

// IconKeyForSpec exposes iconKeyForSpec to the external tests.
func IconKeyForSpec(spec map[string]any) string {
	return iconKeyForSpec(spec)
}

// The functions below are used only by tests.

// NetworkByName returns the network with the given name, or nil. Names are
// matched exactly, as minimega matches VLAN names.
func (d *Document) NetworkByName(name string) *Network {
	for i := range d.Networks {
		if d.Networks[i].Name == name {
			return &d.Networks[i]
		}
	}

	return nil
}

// DeviceNodes returns all device nodes in document order.
func (d *Document) DeviceNodes() []*Node {
	return d.nodesOfKind(NodeKindDevice)
}

// SwitchNodes returns all switch nodes in document order.
func (d *Document) SwitchNodes() []*Node {
	return d.nodesOfKind(NodeKindSwitch)
}

func (d *Document) nodesOfKind(kind NodeKind) []*Node {
	var nodes []*Node

	for i := range d.Nodes {
		if d.Nodes[i].Kind == kind {
			nodes = append(nodes, &d.Nodes[i])
		}
	}

	return nodes
}

// IconKeys returns the sorted registry of icon keys a device may declare.
func IconKeys() []string {
	return slices.Clone(iconKeys)
}
