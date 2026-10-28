package web

import (
	"fmt"
	"slices"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/util/plog"
	"phenix/web/rbac"
)

// autotunnelAnnotation makes the startup app create port forwards to the VM
// when the experiment starts, so setting it requires permission to create them.
const autotunnelAnnotation = "phenix/startup-autotunnel"

// forwardDeniedVM returns the first of the experiment's VMs the role may not
// create port forwards for, or "" when it may for all of them.
func forwardDeniedVM(role rbac.Role, exp string, vms []string) string {
	for _, vm := range vms {
		if !role.Allowed("vms/forwards", "create", fmt.Sprintf("%s/%s", exp, vm)) {
			return vm
		}
	}

	return ""
}

// topologyVMs returns the names of the VMs a topology deploys.
func topologyVMs(topology string) ([]string, error) {
	c, _ := store.NewConfig("topology/" + topology)

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("topology %s does not exist", topology)
	}

	topo, err := types.DecodeTopologyFromConfig(*c)
	if err != nil {
		return nil, fmt.Errorf("decoding topology %s: %w", topology, err)
	}

	var vms []string

	for _, node := range topo.Nodes() {
		if !node.External() {
			vms = append(vms, node.General().Hostname())
		}
	}

	return vms, nil
}

// changesAutotunnel reports whether replacing the VM's annotations with
// annotations sets an autotunnel annotation the VM does not already have.
func changesAutotunnel(expName, vmName string, annotations map[string]any) bool {
	value, ok := annotations[autotunnelAnnotation]
	if !ok {
		return false
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		plog.Error(plog.TypeSystem, "getting experiment", "exp", expName, "err", err)

		return true
	}

	node := exp.Spec.Topology().FindNodeByName(vmName)
	if node == nil {
		return true
	}

	current, ok := node.GetAnnotation(autotunnelAnnotation)
	if !ok {
		return true
	}

	newList, ok1 := stringList(value)
	oldList, ok2 := stringList(current)

	return !ok1 || !ok2 || !slices.Equal(newList, oldList)
}

func stringList(value any) ([]string, bool) {
	switch list := value.(type) {
	case []string:
		return list, true
	case []any:
		strs := make([]string, 0, len(list))

		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}

			strs = append(strs, s)
		}

		return strs, true
	default:
		return nil, false
	}
}
