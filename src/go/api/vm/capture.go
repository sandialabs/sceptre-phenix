package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/util/file"
	"phenix/util/mm"
)

var (
	ErrNoCaptures = errors.New("no captures exist")

	// ErrNoCaptureFile is the error of StartCapture for an empty output file name.
	ErrNoCaptureFile = errors.New("no output file provided")

	// ErrInvalidInterface is the error of StartCapture and StopCapture for an interface index that the VM does not
	// have.
	ErrInvalidInterface = errors.New("invalid interface provided for capture")

	// ErrDisconnectedInterface is the error of StartCapture for an interface that is not connected to a VLAN.
	ErrDisconnectedInterface = errors.New("cannot capture on a disconnected interface")

	// ErrUnknownInterface is wrapped by the error of ResolveInterface for an
	// identifier that names no interface of the VM.
	ErrUnknownInterface = errors.New("unknown interface")
)

// interfaceError is an error of ResolveInterface: its message, which wraps
// ErrUnknownInterface.
type interfaceError struct {
	msg string
}

func (e *interfaceError) Error() string { return e.msg }

func (e *interfaceError) Unwrap() error { return ErrUnknownInterface }

// ResolveInterface resolves the given interface identifier - either a
// zero-based index (e.g. "0") or the interface name as declared in the
// experiment topology (e.g. "IF0") - to the interface's zero-based index for
// the given VM. Name matching is case-insensitive. It returns an error that
// wraps ErrUnknownInterface if the identifier doesn't match a valid interface
// index or name for the VM.
func ResolveInterface(v *mm.VM, iface string) (int, error) {
	if v == nil {
		return 0, errors.New("no VM details provided")
	}

	if iface == "" {
		return 0, errors.New("no interface identifier provided")
	}

	if idx, err := strconv.Atoi(iface); err == nil {
		if idx < 0 || idx >= len(v.Networks) {
			return 0, &interfaceError{msg: fmt.Sprintf("interface index %d is out of range for VM %s", idx, v.Name)}
		}

		return idx, nil
	}

	for idx, name := range v.IfaceNames {
		if strings.EqualFold(name, iface) {
			return idx, nil
		}
	}

	return 0, &interfaceError{msg: fmt.Sprintf("no interface named %q found for VM %s", iface, v.Name)}
}

// StartCapture starts a packet capture on the given interface for the given VM
// in the given experiment. The captured packets are written to the experiment's
// files directory using the base name of the provided output file in PCAP
// format. It returns any errors encountered while starting the packet capture.
// Once ctx is done, minimega commands not yet sent give up (see mm.Context).
func StartCapture(ctx context.Context, expName, vmName string, iface int, out string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if out == "" {
		return ErrNoCaptureFile
	}

	v, err := Get(ctx, expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM details: %w", err)
	}

	return StartCaptureForVM(ctx, v, expName, vmName, iface, out)
}

// StartCaptureForVM behaves like StartCapture, but accepts an already-fetched
// VM instead of looking it up itself. Callers that already have the VM's
// details on hand (e.g. because they resolved an interface name to an index
// via ResolveInterface, which also requires the VM's details) should use this
// to avoid an extra, redundant VM lookup.
func StartCaptureForVM(ctx context.Context, v *mm.VM, expName, vmName string, iface int, out string) error {
	if v == nil {
		return errors.New("no VM details provided")
	}

	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if out == "" {
		return ErrNoCaptureFile
	}

	if !v.Running {
		return ErrVMNotRunning
	}

	if iface < 0 || iface >= len(v.Networks) {
		return ErrInvalidInterface
	}

	if v.Networks[iface] == "disconnected" {
		return ErrDisconnectedInterface
	}

	if ext := filepath.Ext(out); ext != ".pcap" {
		out += ".pcap"
	}

	out = fmt.Sprintf("%s/files/%s", expName, filepath.Base(out))

	if err := mm.StartVMCapture(
		mm.NS(expName),
		mm.VMName(vmName),
		mm.CaptureInterface(iface),
		mm.CaptureFile(out),
		mm.Context(ctx),
	); err != nil {
		return fmt.Errorf(
			"starting VM capture for interface %d on VM %s in experiment %s: %w",
			iface,
			vmName,
			expName,
			err,
		)
	}

	return nil
}

// StopCaptures stops all currently running packet captures for the given VM in
// the given experiment. It returns any errors encountered while stopping the
// packet captures. Once ctx is done, minimega commands not yet sent give up
// (see mm.Context).
func StopCaptures(ctx context.Context, expName, vmName string) error {
	return stopCaptures(ctx, expName, vmName, nil)
}

// StopCapture stops the packet capture running on the given interface for the
// given VM in the given experiment, leaving any other running captures for
// the VM untouched. It returns any errors encountered while stopping the
// packet capture. Once ctx is done, minimega commands not yet sent give up
// (see mm.Context).
func StopCapture(ctx context.Context, expName, vmName string, iface int) error {
	if iface < 0 {
		return ErrInvalidInterface
	}

	return stopCaptures(ctx, expName, vmName, &iface)
}

// stopCaptures stops packet captures for the given VM in the given
// experiment. If iface is non-nil, only the capture running on that
// interface index is stopped; otherwise all captures for the VM are stopped.
func stopCaptures(ctx context.Context, expName, vmName string, iface *int) error {
	captures, err := listVMCaptures(ctx, expName, vmName)
	if err != nil {
		return err
	}

	return stopListedCaptures(ctx, expName, vmName, iface, captures)
}

// listVMCaptures returns the captures that minimega runs for the VM vmName of the experiment expName.
func listVMCaptures(ctx context.Context, expName, vmName string) ([]mm.Capture, error) {
	if expName == "" {
		return nil, errors.New("no experiment name provided")
	}

	if vmName == "" {
		return nil, errors.New("no VM name provided")
	}

	captures := mm.GetVMCaptures(mm.NS(expName), mm.VMName(vmName), mm.Context(ctx))
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing captures of VM %s in experiment %s: %w", vmName, expName, err)
	}

	return captures, nil
}

// CapturesToStop returns the captures that StopCaptures (iface nil) or StopCapture (the interface index iface)
// stops for the VM vmName of the experiment expName, and stops none of them. Its error wraps ErrNoCaptures when the
// VM, or the interface, has no capture.
func CapturesToStop(ctx context.Context, expName, vmName string, iface *int) ([]mm.Capture, error) {
	if iface != nil && *iface < 0 {
		return nil, ErrInvalidInterface
	}

	captures, err := listVMCaptures(ctx, expName, vmName)
	if err != nil {
		return nil, err
	}

	return selectCaptures(expName, vmName, iface, captures)
}

// selectCaptures returns the captures of the VM that a stop acts on: all of them, or the one on the interface
// iface. Its error wraps ErrNoCaptures when there is none.
func selectCaptures(expName, vmName string, iface *int, captures []mm.Capture) ([]mm.Capture, error) {
	if len(captures) == 0 {
		return nil, fmt.Errorf("vm %s in experiment %s: %w", vmName, expName, ErrNoCaptures)
	}

	if iface == nil {
		return captures, nil
	}

	for _, capture := range captures {
		if capture.Interface == *iface {
			return []mm.Capture{capture}, nil
		}
	}

	return nil, fmt.Errorf("interface %d on VM %s in experiment %s: %w", *iface, vmName, expName, ErrNoCaptures)
}

// stopListedCaptures is stopCaptures for the VM's captures as already listed.
func stopListedCaptures(ctx context.Context, expName, vmName string, iface *int, captures []mm.Capture) error {
	if _, err := selectCaptures(expName, vmName, iface, captures); err != nil {
		return err
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	dir := exp.Spec.BaseDir() + "/captures"

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating files directory for experiment %s: %w", expName, err)
	}

	opts := []mm.Option{mm.NS(expName), mm.VMName(vmName), mm.Context(ctx), mm.KnownCaptures(captures)}

	if iface != nil {
		opts = append(opts, mm.CaptureInterface(*iface))
	}

	if err := mm.StopVMCapture(opts...); err != nil {
		if iface != nil {
			return fmt.Errorf(
				"stopping VM capture for interface %d on VM %s in experiment %s: %w",
				*iface,
				vmName,
				expName,
				err,
			)
		}

		return fmt.Errorf(
			"stopping VM captures for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	return nil
}

// ErrNoVLAN is the error of CaptureVLAN and StopCaptureVLAN for an empty VLAN alias.
var ErrNoVLAN = errors.New("no VLAN provided")

// vlanCaptureTarget is a capture that StopCaptureVLAN stops, with its VM.
type vlanCaptureTarget struct {
	vm      mm.VM
	capture mm.Capture
}

// vlanCaptureTargets returns the captures on the VLAN with the alias vlan of every running VM of the experiment
// expName (of vmList, if given), in the VMs' order.
func vlanCaptureTargets(ctx context.Context, expName, vlan string, vmList []string) ([]vlanCaptureTarget, error) {
	if vlan == "" {
		return nil, ErrNoVLAN
	}

	vms, err := runningCaptureVMs(ctx, expName, "stopped", vmList)
	if err != nil {
		return nil, err
	}

	var (
		targets []vlanCaptureTarget
		match   = onVLAN(vlan)
	)

	for _, v := range vms {
		for _, capture := range v.Captures {
			if match(v, capture.Interface) {
				targets = append(targets, vlanCaptureTarget{vm: v, capture: capture})
			}
		}
	}

	return targets, nil
}

// VLANCapturesToStop returns the captures that StopCaptureVLAN stops, and stops none of them. Its errors are those of
// StopCaptureVLAN before it stops a capture.
func VLANCapturesToStop(ctx context.Context, expName, vlan string, vmList []string) ([]mm.Capture, error) {
	targets, err := vlanCaptureTargets(ctx, expName, vlan, vmList)
	if err != nil {
		return nil, err
	}

	captures := make([]mm.Capture, 0, len(targets))
	for _, target := range targets {
		captures = append(captures, target.capture)
	}

	return captures, nil
}

// captureKey names the capture on the interface at index iface of the VM vm.
func captureKey(vm string, iface int) string {
	return vm + ":" + strconv.Itoa(iface)
}

// ifaceMatcher reports whether the interface at index iface of the VM v is one that a capture call acts on.
type ifaceMatcher func(v mm.VM, iface int) bool

// runningCaptureVMs returns the VMs of the experiment expName that minimega runs, only those of vmList when it is not
// empty. It is an error when the experiment does not run; verb ("started" or "stopped") names the capture call.
func runningCaptureVMs(ctx context.Context, expName, verb string, vmList []string) ([]mm.VM, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	if !exp.Running() {
		return nil, fmt.Errorf("packet captures can only be %s for a running experiment", verb)
	}

	vms, err := List(ctx, expName)
	if err != nil {
		return nil, fmt.Errorf("getting vm list for %s failed", expName)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("listing VMs of experiment %s: %w", expName, err)
	}

	running := make([]mm.VM, 0, len(vms))

	for _, v := range vms {
		// List already has the state from minimega, and none for a VM that minimega does not have.
		if v.State != vmStateRunning {
			continue
		}

		if len(vmList) > 0 && !slices.Contains(vmList, v.Name) {
			continue
		}

		running = append(running, v)
	}

	return running, nil
}

// startMatchingCaptures starts a capture on each interface of the VMs that match accepts. It returns the VMs with a
// capture that started, in order, and the interfaces it started them on. A capture that minimega fails to start is
// left out. ctx is checked only until the first capture starts; scope names the interfaces in the error.
func startMatchingCaptures(
	ctx context.Context, expName, scope string, vms []mm.VM, match ifaceMatcher,
) ([]string, map[string]struct{}, error) {
	var (
		matchedVMs []string
		started    = make(map[string]struct{})
		committed  = context.WithoutCancel(ctx)
	)

	for _, v := range vms {
		var vmStarted bool

		for iface := range max(len(v.IPv4), len(v.Networks)) {
			if !match(v, iface) {
				continue
			}

			filename := fmt.Sprintf("%s_%d_%s.pcap", v.Name, iface, getTimestamp(time.Now()))

			// once one is started, start the rest
			run := ctx
			if len(started) > 0 {
				run = committed
			}

			err := StartCaptureForVM(run, &v, expName, v.Name, iface, filename)
			if err == nil {
				started[captureKey(v.Name, iface)] = struct{}{}
				vmStarted = true

				continue
			}

			if err := run.Err(); err != nil {
				return nil, nil, fmt.Errorf("starting captures in %s: %w", scope, err)
			}
		}

		if vmStarted {
			matchedVMs = append(matchedVMs, v.Name)
		}
	}

	// captures write to the experiment's files directory
	file.InvalidateExperimentFiles(expName)

	return matchedVMs, started, nil
}

// vlanAlias is the alias of the VLAN that minimega reports for an interface, such as EXP_1 for "EXP_1 (101)". A
// network without an ID in parentheses is its own alias.
func vlanAlias(network string) string {
	if match := vlanAliasRegex.FindStringSubmatch(network); match != nil {
		return match[1]
	}

	return network
}

// onVLAN matches the interfaces on the VLAN with the alias vlan, in the case that minimega reports it.
func onVLAN(vlan string) ifaceMatcher {
	return func(v mm.VM, iface int) bool {
		return iface < len(v.Networks) && vlanAlias(v.Networks[iface]) == vlan
	}
}

// CaptureVLAN starts a capture on each interface on the VLAN with the alias vlan of every running VM (of vmList, if
// given). It returns the captures that it started, in the VMs' order. A capture that minimega fails to start is left
// out. ctx is checked only until the first capture starts (see mm.Context).
func CaptureVLAN(ctx context.Context, expName, vlan string, vmList []string) ([]mm.Capture, error) {
	if vlan == "" {
		return nil, ErrNoVLAN
	}

	vms, err := runningCaptureVMs(ctx, expName, "started", vmList)
	if err != nil {
		return nil, err
	}

	matchedVMs, started, err := startMatchingCaptures(ctx, expName, "VLAN "+vlan, vms, onVLAN(vlan))
	if err != nil {
		return nil, err
	}

	var captures []mm.Capture

	for _, capture := range capturesForVMs(expName, matchedVMs) {
		if _, ok := started[captureKey(capture.VM, capture.Interface)]; ok {
			captures = append(captures, capture)
		}
	}

	return captures, nil
}

// StopCaptureVLAN stops the capture on each interface on the VLAN with the alias vlan of every running VM (of
// vmList, if given). Captures on the VMs' other interfaces continue. It returns the captures that it stopped. A
// capture that minimega fails to stop is left out. ctx is checked only until the first capture stops.
func StopCaptureVLAN(ctx context.Context, expName, vlan string, vmList []string) ([]mm.Capture, error) {
	targets, err := vlanCaptureTargets(ctx, expName, vlan, vmList)
	if err != nil {
		return nil, err
	}

	var (
		stopped   []mm.Capture
		committed = context.WithoutCancel(ctx)
	)

	for _, target := range targets {
		// once one capture is stopped, stop the rest
		run := ctx
		if len(stopped) > 0 {
			run = committed
		}

		iface := target.capture.Interface

		err := stopListedCaptures(run, expName, target.vm.Name, &iface, target.vm.Captures)
		if err == nil {
			stopped = append(stopped, target.capture)

			continue
		}

		if err := run.Err(); err != nil {
			return nil, fmt.Errorf("stopping captures on VLAN %s: %w", vlan, err)
		}
	}

	// the stopped captures' files are now complete
	file.InvalidateExperimentFiles(expName)

	return stopped, nil
}
