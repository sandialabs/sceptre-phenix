package vm

import (
	"context"
	"errors"
	"fmt"

	"phenix/types"
	"phenix/util/mm"
	"phenix/util/plog"
)

// ErrNoInterface is the error of SetQoS and ClearQoS for an interface index past the last interface of the VM.
var ErrNoInterface = errors.New("the VM has no interface with that index")

// GetQoS returns the running experiment expName and the link quality of each interface of its VM vmName, in
// interface order. The error wraps the errors of CheckTarget, and mm.ErrVMNotFound when minimega does not have the VM.
func GetQoS(ctx context.Context, expName, vmName string) (*types.Experiment, []mm.InterfaceQoS, error) {
	exp, err := CheckTarget(expName, vmName, true)
	if err != nil {
		return nil, nil, err
	}

	ifaces, err := mm.GetVMQoS(ctx, expName, vmName)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the link quality of VM %s: %w", vmName, err)
	}

	return exp, ifaces, nil
}

// qosInterfaces is GetQoS, with an error that wraps ErrNoInterface when the VM has no interface index.
func qosInterfaces(ctx context.Context, expName, vmName string, index int) (*types.Experiment, []mm.InterfaceQoS, error) {
	exp, ifaces, err := GetQoS(ctx, expName, vmName)
	if err != nil {
		return nil, nil, err
	}

	if index < 0 || index >= len(ifaces) {
		return nil, nil, fmt.Errorf("VM %s has no interface %d: it has %d interfaces: %w",
			vmName, index, len(ifaces), ErrNoInterface)
	}

	return exp, ifaces, nil
}

// SetQoS replaces the link quality of the interface q.Interface of the VM vmName in the running experiment expName
// with q, and returns the experiment and the link quality that minimega then reports for the interface. The error
// wraps mm.ErrInvalidQoS for a q that mm.ValidateQoS refuses or minimega does not apply, ErrNoInterface, and the
// errors of GetQoS.
func SetQoS(ctx context.Context, expName, vmName string, q mm.InterfaceQoS) (*types.Experiment, mm.InterfaceQoS, error) {
	if err := mm.ValidateQoS(q); err != nil {
		return nil, q, fmt.Errorf("checking the link quality: %w", err)
	}

	exp, ifaces, err := qosInterfaces(ctx, expName, vmName, q.Interface)
	if err != nil {
		return nil, q, err
	}

	if err := mm.SetInterfaceQoS(ctx, expName, vmName, q.Interface, q, ifaces[q.Interface]); err != nil {
		return nil, q, fmt.Errorf("setting the link quality of VM %s: %w", vmName, err)
	}

	// the change is made, so its result is read whatever ctx says
	after, err := mm.GetVMQoS(context.WithoutCancel(ctx), expName, vmName)
	if err == nil && q.Interface < len(after) {
		return exp, after[q.Interface], nil
	}

	plog.Warn(plog.TypeSystem, "reading back vm link quality", "exp", expName, "vm", vmName, "err", err)

	return exp, q, nil
}

// ClearQoS removes the link quality of the interface index of the VM vmName in the running experiment expName, and
// returns the experiment. The error wraps ErrNoInterface and the errors of GetQoS.
func ClearQoS(ctx context.Context, expName, vmName string, index int) (*types.Experiment, error) {
	exp, _, err := qosInterfaces(ctx, expName, vmName, index)
	if err != nil {
		return nil, err
	}

	if err := mm.ClearInterfaceQoS(ctx, expName, vmName, index); err != nil {
		return nil, fmt.Errorf("clearing the link quality of VM %s: %w", vmName, err)
	}

	return exp, nil
}
